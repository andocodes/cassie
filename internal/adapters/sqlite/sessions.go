package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/andocodes/cassie/internal/domain/runtime"
	_ "modernc.org/sqlite"
)

type Sessions struct {
	db *sql.DB
}

func Open(path string) (*Sessions, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open session database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Sessions{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Sessions) Close() error {
	return s.db.Close()
}

func (s *Sessions) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    app TEXT NOT NULL,
    root TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    exit_code INTEGER
);
CREATE INDEX IF NOT EXISTS sessions_started_at ON sessions(started_at DESC);
CREATE TABLE IF NOT EXISTS trusted_configs (
    path TEXT PRIMARY KEY,
    digest TEXT NOT NULL,
    trusted_at TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("migrate session database: %w", err)
	}
	return nil
}

func (s *Sessions) Trusted(ctx context.Context, path, digest string) (bool, error) {
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT digest FROM trusted_configs WHERE path = ?`, path).Scan(&stored)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query trusted configuration: %w", err)
	}
	return stored == digest, nil
}

func (s *Sessions) Trust(ctx context.Context, path, digest string, trustedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO trusted_configs(path, digest, trusted_at) VALUES(?, ?, ?)
ON CONFLICT(path) DO UPDATE SET digest = excluded.digest, trusted_at = excluded.trusted_at`,
		path, digest, trustedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store trusted configuration: %w", err)
	}
	return nil
}

func (s *Sessions) Start(ctx context.Context, session runtime.Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(id, app, root, status, started_at) VALUES(?, ?, ?, ?, ?)`,
		session.ID, session.App, session.Root, session.Status, session.StartedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store session start: %w", err)
	}
	return nil
}

func (s *Sessions) Finish(ctx context.Context, id string, status runtime.Status, exitCode int, endedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, exit_code = ?, ended_at = ? WHERE id = ?`,
		status, exitCode, endedAt.UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("store session finish: %w", err)
	}
	return nil
}

func (s *Sessions) Recent(ctx context.Context, limit int) ([]runtime.Session, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, app, root, status, started_at, ended_at, exit_code
FROM sessions
ORDER BY started_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []runtime.Session
	for rows.Next() {
		var session runtime.Session
		var started string
		var ended sql.NullString
		var exitCode sql.NullInt64
		if err := rows.Scan(&session.ID, &session.App, &session.Root, &session.Status, &started, &ended, &exitCode); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		session.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return nil, fmt.Errorf("parse session start: %w", err)
		}
		if ended.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, ended.String)
			if parseErr != nil {
				return nil, fmt.Errorf("parse session end: %w", parseErr)
			}
			session.EndedAt = &value
		}
		if exitCode.Valid {
			value := int(exitCode.Int64)
			session.ExitCode = &value
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}
