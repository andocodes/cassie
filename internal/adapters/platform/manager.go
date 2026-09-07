package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	PortlessVersion  = "0.15.6"
	InfisicalVersion = "0.43.129"
	ServerVersion    = "v0.165.3"
	InfisicalURL     = "https://infisical.localhost"
	infisicalPort    = 4080
)

type Manager struct {
	DataDir string
	Tools   string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Env     map[string]string
}

func (m Manager) InstallTools(ctx context.Context) error {
	if err := os.MkdirAll(m.Tools, 0o700); err != nil {
		return fmt.Errorf("create tools directory: %w", err)
	}
	if err := requireNode(ctx); err != nil {
		return err
	}
	if m.toolsCurrent() {
		return nil
	}
	command := exec.CommandContext(ctx, "npm", "install", "--prefix", m.Tools, "--no-save", "--no-package-lock",
		"portless@"+PortlessVersion, "@infisical/cli@"+InfisicalVersion)
	command.Stdin = m.Stdin
	command.Stdout = m.Stdout
	command.Stderr = m.Stderr
	command.Env = m.environment()
	if err := command.Run(); err != nil {
		return fmt.Errorf("install managed tools: %w", err)
	}
	manifest := fmt.Sprintf("portless=%s\ninfisical=%s\n", PortlessVersion, InfisicalVersion)
	if err := os.WriteFile(filepath.Join(m.Tools, ".cassie-versions"), []byte(manifest), 0o600); err != nil {
		return fmt.Errorf("write tool manifest: %w", err)
	}
	return nil
}

func (m Manager) Up(ctx context.Context) error {
	if err := m.InstallTools(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(m.DataDir, 0o700); err != nil {
		return fmt.Errorf("create platform directory: %w", err)
	}
	if err := writeFile(filepath.Join(m.DataDir, "compose.yaml"), []byte(composeFile), 0o600); err != nil {
		return err
	}
	if err := m.ensureEnvironment(); err != nil {
		return err
	}
	if err := m.compose(ctx, "up", "-d", "--remove-orphans"); err != nil {
		return err
	}
	if err := m.waitForInfisical(ctx, time.Minute); err != nil {
		return err
	}
	return (Portless{Binary: m.Binary("portless"), Env: m.Env}).Alias(ctx, "infisical", infisicalPort)
}

func (m Manager) Down(ctx context.Context) error {
	var result error
	if err := (Portless{Binary: m.Binary("portless"), Env: m.Env}).Remove(ctx, "infisical"); err != nil {
		result = errors.Join(result, err)
	}
	if err := m.compose(ctx, "down", "--remove-orphans"); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

func (m Manager) Logs(ctx context.Context, follow bool) error {
	args := []string{"logs", "--tail", "200"}
	if follow {
		args = append(args, "--follow")
	}
	return m.compose(ctx, args...)
}

func (m Manager) Prune(ctx context.Context) error {
	binary := m.Binary("portless")
	command := exec.CommandContext(ctx, binary, "prune")
	command.Stdin = m.Stdin
	command.Stdout = m.Stdout
	command.Stderr = m.Stderr
	command.Env = m.environment()
	if err := command.Run(); err != nil {
		return fmt.Errorf("prune Portless routes: %w", err)
	}
	return nil
}

func (m Manager) Binary(name string) string {
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".cmd"
	}
	return filepath.Join(m.Tools, "node_modules", ".bin", filename)
}

func (m Manager) LoginStatus(ctx context.Context) bool {
	command := exec.CommandContext(ctx, m.Binary("infisical"), "login", "status", "--json")
	command.Env = append(m.environment(), "INFISICAL_API_URL="+InfisicalURL+"/api")
	return command.Run() == nil
}

func (m Manager) Login(ctx context.Context) error {
	command := exec.CommandContext(ctx, m.Binary("infisical"), "login", "--domain="+InfisicalURL)
	command.Stdin = m.Stdin
	command.Stdout = m.Stdout
	command.Stderr = m.Stderr
	command.Env = append(m.environment(), "INFISICAL_API_URL="+InfisicalURL+"/api")
	if err := command.Run(); err != nil {
		return fmt.Errorf("log in to Infisical: %w", err)
	}
	return nil
}

func (m Manager) compose(ctx context.Context, args ...string) error {
	command := m.composeCommand(ctx, args...)
	command.Stdin = m.Stdin
	command.Stdout = m.Stdout
	command.Stderr = m.Stderr
	command.Env = m.environment()
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (m Manager) environment() []string {
	values := os.Environ()
	for key, value := range m.Env {
		values = append(values, key+"="+value)
	}
	return values
}

func (m Manager) toolsCurrent() bool {
	content, err := os.ReadFile(filepath.Join(m.Tools, ".cassie-versions"))
	if err != nil {
		return false
	}
	want := fmt.Sprintf("portless=%s\ninfisical=%s\n", PortlessVersion, InfisicalVersion)
	return string(content) == want && regular(m.Binary("portless")) && regular(m.Binary("infisical"))
}

func (m Manager) ensureEnvironment() error {
	path := filepath.Join(m.DataDir, ".env")
	if regular(path) {
		return nil
	}
	encryptionKey, err := randomHex(32)
	if err != nil {
		return err
	}
	authSecret, err := randomHex(32)
	if err != nil {
		return err
	}
	password, err := randomHex(24)
	if err != nil {
		return err
	}
	content := strings.Join([]string{
		"ENCRYPTION_KEY=" + encryptionKey,
		"AUTH_SECRET=" + authSecret,
		"POSTGRES_USER=infisical",
		"POSTGRES_PASSWORD=" + password,
		"POSTGRES_DB=infisical",
		"DB_CONNECTION_URI=postgres://infisical:" + password + "@db:5432/infisical",
		"REDIS_URL=redis://redis:6379",
		"SITE_URL=" + InfisicalURL,
		"TELEMETRY_ENABLED=false",
		"OTEL_TELEMETRY_COLLECTION_ENABLED=false",
		"",
	}, "\n")
	return writeFile(path, []byte(content), 0o600)
}

func (m Manager) waitForInfisical(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(infisicalPort), nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode < http.StatusInternalServerError {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Infisical did not become ready within %s", timeout)
		case <-ticker.C:
		}
	}
}

func requireNode(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, "node", "--version").Output()
	if err != nil {
		return fmt.Errorf("Node.js 24 or newer is required to install Portless")
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(output)), "v")
	major, _ := strconv.Atoi(strings.Split(version, ".")[0])
	if major < 24 {
		return fmt.Errorf("Node.js 24 or newer is required; found %s", version)
	}
	return nil
}

func randomHex(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate platform secret: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func writeFile(path string, content []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cassie-*")
	if err != nil {
		return fmt.Errorf("create temporary platform file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace platform file: %w", err)
	}
	return nil
}

func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

const composeFile = `services:
  backend:
    image: infisical/infisical:` + ServerVersion + `
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy
      redis:
        condition: service_started
    env_file: .env
    ports:
      - "127.0.0.1:4080:8080"
    environment:
      NODE_ENV: production
    networks: [cassie]

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    networks: [cassie]
    volumes:
      - redis_data:/data

  db:
    image: postgres:14-alpine
    restart: unless-stopped
    env_file: .env
    volumes:
      - pg_data:/var/lib/postgresql/data
    networks: [cassie]
    healthcheck:
      test: [CMD-SHELL, "pg_isready --username=$${POSTGRES_USER} --dbname=$${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 12

volumes:
  pg_data:
  redis_data:

networks:
  cassie:
`
