package platform

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type backupManifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Infisical string    `json:"infisical"`
}

func (m Manager) Backup(ctx context.Context, target string) (string, error) {
	if target == "" {
		target = filepath.Join(m.DataDir, "backups", "cassie-"+time.Now().Format("20060102-150405.000")+".tar.gz")
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve backup path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("backup already exists: %s", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect backup target: %w", err)
	}
	environment, err := os.ReadFile(filepath.Join(m.DataDir, ".env"))
	if err != nil {
		return "", fmt.Errorf("read platform environment: %w", err)
	}
	database, err := os.CreateTemp(m.DataDir, ".cassie-database-*.sql")
	if err != nil {
		return "", fmt.Errorf("create database dump: %w", err)
	}
	databasePath := database.Name()
	defer os.Remove(databasePath)
	command := m.composeCommand(ctx, "exec", "-T", "db", "pg_dump", "--clean", "--if-exists", "--no-owner", "--no-privileges", "-U", "infisical", "infisical")
	command.Stdout = database
	if output, dumpErr := command.StderrPipe(); dumpErr == nil {
		if startErr := command.Start(); startErr != nil {
			_ = database.Close()
			return "", fmt.Errorf("start database backup: %w", startErr)
		}
		stderr, _ := io.ReadAll(output)
		if waitErr := command.Wait(); waitErr != nil {
			_ = database.Close()
			return "", fmt.Errorf("backup Infisical database: %w: %s", waitErr, strings.TrimSpace(string(stderr)))
		}
	} else {
		_ = database.Close()
		return "", fmt.Errorf("capture database backup errors: %w", dumpErr)
	}
	if err := database.Close(); err != nil {
		return "", err
	}
	manifest, _ := json.MarshalIndent(backupManifest{Version: 1, CreatedAt: time.Now().UTC(), Infisical: ServerVersion}, "", "  ")
	if err := createArchive(target, map[string]archiveSource{
		"manifest.json": {Content: append(manifest, '\n')},
		"platform.env":  {Content: environment},
		"database.sql":  {Path: databasePath},
	}); err != nil {
		return "", err
	}
	return target, nil
}

func (m Manager) Restore(ctx context.Context, source string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve backup path: %w", err)
	}
	dir, err := os.MkdirTemp(m.DataDir, ".cassie-restore-*")
	if err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := extractArchive(source, dir); err != nil {
		return err
	}
	manifestContent, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("backup manifest is missing: %w", err)
	}
	var manifest backupManifest
	if err := json.Unmarshal(manifestContent, &manifest); err != nil || manifest.Version != 1 {
		return fmt.Errorf("unsupported or invalid Cassie backup")
	}
	environment, err := os.ReadFile(filepath.Join(dir, "platform.env"))
	if err != nil {
		return fmt.Errorf("backup environment is missing: %w", err)
	}
	database, err := os.Open(filepath.Join(dir, "database.sql"))
	if err != nil {
		return fmt.Errorf("backup database is missing: %w", err)
	}
	defer database.Close()

	if err := m.compose(ctx, "stop", "backend"); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(m.DataDir, ".env"), environment, 0o600); err != nil {
		return err
	}
	if err := m.compose(ctx, "up", "-d", "db", "redis"); err != nil {
		return err
	}
	if err := m.waitForPostgres(ctx, time.Minute); err != nil {
		return err
	}
	for _, statement := range []string{
		"DROP DATABASE IF EXISTS infisical WITH (FORCE);",
		"CREATE DATABASE infisical;",
	} {
		command := m.composeCommand(ctx, "exec", "-T", "db", "psql", "-v", "ON_ERROR_STOP=1", "-U", "infisical", "-d", "postgres", "-c", statement)
		if output, commandErr := command.CombinedOutput(); commandErr != nil {
			return fmt.Errorf("prepare Infisical restore: %w: %s", commandErr, strings.TrimSpace(string(output)))
		}
	}
	restore := m.composeCommand(ctx, "exec", "-T", "db", "psql", "-v", "ON_ERROR_STOP=1", "-U", "infisical", "-d", "infisical")
	restore.Stdin = database
	restore.Stdout = m.Stdout
	restore.Stderr = m.Stderr
	if err := restore.Run(); err != nil {
		return fmt.Errorf("restore Infisical database: %w", err)
	}
	if err := m.compose(ctx, "up", "-d", "backend"); err != nil {
		return err
	}
	if err := m.waitForInfisical(ctx, time.Minute); err != nil {
		return err
	}
	return (Portless{Binary: m.Binary("portless"), Env: m.Env}).Alias(ctx, "infisical", infisicalPort)
}

func (m Manager) waitForPostgres(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if m.composeCommand(ctx, "exec", "-T", "db", "pg_isready", "-U", "infisical", "-d", "infisical").Run() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("PostgreSQL did not become ready within %s", timeout)
		case <-ticker.C:
		}
	}
}

func (m Manager) composeCommand(ctx context.Context, args ...string) *exec.Cmd {
	commandArgs := []string{"compose", "--project-name", "cassie", "--file", filepath.Join(m.DataDir, "compose.yaml"), "--env-file", filepath.Join(m.DataDir, ".env")}
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, "docker", commandArgs...)
	command.Env = m.environment()
	return command
}

type archiveSource struct {
	Content []byte
	Path    string
}

func createArchive(target string, files map[string]archiveSource) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".cassie-backup-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	gzipWriter := gzip.NewWriter(temporary)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{"manifest.json", "platform.env", "database.sql"} {
		source := files[name]
		var content []byte
		if source.Path != "" {
			content, err = os.ReadFile(source.Path)
		} else {
			content = source.Content
		}
		if err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			_ = temporary.Close()
			return fmt.Errorf("read backup source %s: %w", name, err)
		}
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), ModTime: time.Now()}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write(content); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("finish backup: %w", err)
	}
	return nil
}

func extractArchive(source, target string) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	allowed := map[string]bool{"manifest.json": true, "platform.env": true, "database.sql": true}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read backup archive: %w", err)
		}
		if !allowed[header.Name] || header.Typeflag != tar.TypeReg {
			return fmt.Errorf("backup contains unexpected entry %q", header.Name)
		}
		if header.Size > 2<<30 {
			return fmt.Errorf("backup entry %q is too large", header.Name)
		}
		path := filepath.Join(target, header.Name)
		output, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(output, tarReader, header.Size)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		delete(allowed, header.Name)
	}
	if len(allowed) != 0 {
		return fmt.Errorf("backup is incomplete")
	}
	return nil
}
