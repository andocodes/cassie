package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/ports"
	"gopkg.in/yaml.v3"
)

type Environment struct {
	RuntimeDir string
}

func (e Environment) Prepare(_ context.Context, sessionID string, app catalog.Application, values map[string]string) (ports.PreparedEnvironment, error) {
	prepared := ports.PreparedEnvironment{Values: copyValues(values), Cleanup: func() error { return nil }}
	if len(app.Compose.Services) == 0 || len(values) == 0 {
		return prepared, nil
	}
	base, err := composeFiles(app.Root)
	if err != nil {
		return ports.PreparedEnvironment{}, err
	}
	dir := filepath.Join(e.RuntimeDir, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ports.PreparedEnvironment{}, fmt.Errorf("create Compose runtime directory: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(dir) }

	envPath := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(envPath, dotenv(values), 0o600); err != nil {
		_ = cleanup()
		return ports.PreparedEnvironment{}, fmt.Errorf("write Compose secrets: %w", err)
	}
	overridePath := filepath.Join(dir, "compose.yaml")
	override, err := overrideFile(app.Compose.Services, envPath)
	if err != nil {
		_ = cleanup()
		return ports.PreparedEnvironment{}, err
	}
	if err := os.WriteFile(overridePath, override, 0o600); err != nil {
		_ = cleanup()
		return ports.PreparedEnvironment{}, fmt.Errorf("write Compose override: %w", err)
	}
	separator := ":"
	if runtime.GOOS == "windows" {
		separator = ";"
	}
	prepared.Values["COMPOSE_FILE"] = strings.Join(append(base, overridePath), separator)
	prepared.Cleanup = cleanup
	return prepared, nil
}

func composeFiles(root string) ([]string, error) {
	if configured := os.Getenv("COMPOSE_FILE"); configured != "" {
		separator := ":"
		if runtime.GOOS == "windows" {
			separator = ";"
		}
		files := strings.Split(configured, separator)
		for index, file := range files {
			if !filepath.IsAbs(file) {
				files[index] = filepath.Join(root, file)
			}
		}
		return files, nil
	}
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return []string{path}, nil
		}
	}
	return nil, fmt.Errorf("compose.services is configured but no Compose file was found in %s", root)
}

func overrideFile(services []string, envPath string) ([]byte, error) {
	serviceMap := make(map[string]any, len(services))
	for _, service := range services {
		service = strings.TrimSpace(service)
		if service == "" {
			continue
		}
		serviceMap[service] = map[string]any{"env_file": []string{envPath}}
	}
	if len(serviceMap) == 0 {
		return nil, fmt.Errorf("compose.services must contain at least one service")
	}
	encoded, err := yaml.Marshal(map[string]any{"services": serviceMap})
	if err != nil {
		return nil, fmt.Errorf("encode Compose override: %w", err)
	}
	return encoded, nil
}

func dotenv(values map[string]string) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var output strings.Builder
	for _, key := range keys {
		value := strings.ReplaceAll(values[key], "\\", "\\\\")
		value = strings.ReplaceAll(value, "\"", "\\\"")
		value = strings.ReplaceAll(value, "\n", "\\n")
		_, _ = fmt.Fprintf(&output, "%s=\"%s\"\n", key, value)
	}
	return []byte(output.String())
}

func copyValues(values map[string]string) map[string]string {
	result := make(map[string]string, len(values)+1)
	for key, value := range values {
		result[key] = value
	}
	return result
}
