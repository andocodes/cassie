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
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure session database: %w", err)
	}
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
CREATE TABLE IF NOT EXISTS daemon_processes (
    id TEXT PRIMARY KEY,
    app TEXT NOT NULL,
    root TEXT NOT NULL,
    pid INTEGER NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    exit_code INTEGER,
    log_path TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS daemon_processes_started_at ON daemon_processes(started_at DESC);
CREATE INDEX IF NOT EXISTS daemon_processes_status ON daemon_processes(status);
CREATE UNIQUE INDEX IF NOT EXISTS daemon_processes_running_app
ON daemon_processes(app, root)
WHERE status = 'running';
CREATE TABLE IF NOT EXISTS workspace_indexes (
    root TEXT PRIMARY KEY,
    payload BLOB NOT NULL,
    updated_at TEXT NOT NULL
);
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

func (s *Sessions) WorkspaceIndex(ctx context.Context, root string) ([]byte, bool, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM workspace_indexes WHERE root = ?`, root).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query workspace index: %w", err)
	}
	return payload, true, nil
}

func (s *Sessions) SaveWorkspaceIndex(ctx context.Context, root string, payload []byte, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO workspace_indexes(root, payload, updated_at) VALUES(?, ?, ?)
ON CONFLICT(root) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		root,
		payload,
		updatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store workspace index: %w", err)
	}
	return nil
}

func (s *Sessions) StartProcess(ctx context.Context, process runtime.Process) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO daemon_processes(id, app, root, pid, status, started_at, log_path)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		process.ID,
		process.App,
		process.Root,
		process.PID,
		process.Status,
		process.StartedAt.UTC().Format(time.RFC3339Nano),
		process.LogPath,
	)
	if err != nil {
		return fmt.Errorf("store managed process start: %w", err)
	}
	return nil
}

func (s *Sessions) FinishProcess(ctx context.Context, id string, status runtime.Status, exitCode int, endedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE daemon_processes
SET status = ?, exit_code = ?, ended_at = ?
WHERE id = ?`,
		status,
		exitCode,
		endedAt.UTC().Format(time.RFC3339Nano),
		id,
	)
	if err != nil {
		return fmt.Errorf("store managed process finish: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count managed process updates: %w", err)
	}
	if updated == 0 {
		return runtime.ErrProcessNotFound
	}
	return nil
}

func (s *Sessions) Process(ctx context.Context, id string) (runtime.Process, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, app, root, pid, status, started_at, ended_at, exit_code, log_path
FROM daemon_processes
WHERE id = ?`, id)
	process, err := scanProcess(row.Scan)
	if err == sql.ErrNoRows {
		return runtime.Process{}, runtime.ErrProcessNotFound
	}
	if err != nil {
		return runtime.Process{}, fmt.Errorf("query managed process: %w", err)
	}
	return process, nil
}

func (s *Sessions) Processes(ctx context.Context, limit int) ([]runtime.Process, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, app, root, pid, status, started_at, ended_at, exit_code, log_path
FROM daemon_processes
ORDER BY started_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query managed processes: %w", err)
	}
	defer rows.Close()
	return scanProcesses(rows)
}

func (s *Sessions) RunningProcesses(ctx context.Context) ([]runtime.Process, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, app, root, pid, status, started_at, ended_at, exit_code, log_path
FROM daemon_processes
WHERE status = ?
ORDER BY started_at`, runtime.StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("query running managed processes: %w", err)
	}
	defer rows.Close()
	return scanProcesses(rows)
}

type rowScanner func(dest ...any) error

func scanProcess(scan rowScanner) (runtime.Process, error) {
	var process runtime.Process
	var started string
	var ended sql.NullString
	var exitCode sql.NullInt64
	if err := scan(
		&process.ID,
		&process.App,
		&process.Root,
		&process.PID,
		&process.Status,
		&started,
		&ended,
		&exitCode,
		&process.LogPath,
	); err != nil {
		return runtime.Process{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return runtime.Process{}, fmt.Errorf("parse managed process start: %w", err)
	}
	process.StartedAt = parsed
	if ended.Valid {
		value, err := time.Parse(time.RFC3339Nano, ended.String)
		if err != nil {
			return runtime.Process{}, fmt.Errorf("parse managed process end: %w", err)
		}
		process.EndedAt = &value
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		process.ExitCode = &value
	}
	return process, nil
}

func scanProcesses(rows *sql.Rows) ([]runtime.Process, error) {
	var processes []runtime.Process
	for rows.Next() {
		process, err := scanProcess(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan managed process: %w", err)
		}
		processes = append(processes, process)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed processes: %w", err)
	}
	return processes, nil
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
