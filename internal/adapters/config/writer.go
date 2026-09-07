package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/andocodes/cassie/internal/domain/catalog"
	"gopkg.in/yaml.v3"
)

type Match struct {
	Repo string
	Path string
	Dir  string
}

type Writer struct {
	UserPath string
}

func (w Writer) SaveUser(name string, match Match, app catalog.Application) error {
	current, exists, err := readMap(w.UserPath)
	if err != nil {
		return err
	}
	if !exists {
		current = map[string]any{"version": 1}
	}
	apps := childMap(current, "apps")
	if apps == nil {
		apps = make(map[string]any)
		current["apps"] = apps
	}
	entry := applicationMap(app)
	matchMap := make(map[string]any)
	if match.Repo != "" {
		matchMap["repo"] = match.Repo
	}
	if match.Path != "" {
		matchMap["path"] = match.Path
	}
	if match.Dir != "" && match.Dir != "." {
		matchMap["dir"] = filepath.ToSlash(match.Dir)
	}
	entry["match"] = matchMap
	apps[name] = entry
	return writeMap(w.UserPath, current, 0o600)
}

func (w Writer) SaveRepo(root string, app catalog.Application) (string, error) {
	path := filepath.Join(root, repoConfigName)
	current, exists, err := readMap(path)
	if err != nil {
		return "", err
	}
	if !exists {
		current = map[string]any{"version": 1}
	}
	current = merge(current, applicationMap(app))
	if err := writeMap(path, current, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func applicationMap(app catalog.Application) map[string]any {
	result := map[string]any{
		"name":     app.Name,
		"domain":   app.Domain,
		"commands": commandsValue(app.Commands),
	}
	if app.Port > 0 {
		result["port"] = app.Port
	}
	if len(app.Cleanup) > 0 {
		result["cleanup"] = commandsValue(app.Cleanup)
	}
	if app.Grace.Duration > 0 {
		result["grace"] = app.Grace.String()
	}
	if app.Secrets.Enabled() || app.Secrets.Environment != "" || app.Secrets.Path != "" {
		secrets := make(map[string]any)
		if app.Secrets.Project != "" {
			secrets["project"] = app.Secrets.Project
		}
		if app.Secrets.Environment != "" {
			secrets["environment"] = app.Secrets.Environment
		}
		if app.Secrets.Path != "" {
			secrets["path"] = app.Secrets.Path
		}
		result["secrets"] = secrets
	}
	if len(app.Compose.Services) > 0 {
		result["compose"] = map[string]any{"services": app.Compose.Services}
	}
	return result
}

func commandsValue(commands []catalog.Command) []any {
	values := make([]any, 0, len(commands))
	for _, command := range commands {
		if command.Dir == "" {
			values = append(values, command.Run)
			continue
		}
		values = append(values, map[string]any{"run": command.Run, "dir": command.Dir})
	}
	return values
}

func writeMap(path string, value map[string]any, mode os.FileMode) error {
	content, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cassie-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary config: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
