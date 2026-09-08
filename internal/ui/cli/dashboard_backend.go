package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/andocodes/cassie/internal/adapters/config"
	daemonadapter "github.com/andocodes/cassie/internal/adapters/daemon"
	"github.com/andocodes/cassie/internal/adapters/sqlite"
	"github.com/andocodes/cassie/internal/domain/catalog"
	runtimeDomain "github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/andocodes/cassie/internal/ui/tui"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type dashboardBackend struct {
	owner  *app
	root   string
	client dashboardProcessClient

	mu       sync.RWMutex
	resolved map[string]config.Resolved
	ensureMu sync.Mutex
	platform tui.Platform
}

type dashboardProcessClient interface {
	Start(context.Context, runtimeDomain.ProcessSpec) (runtimeDomain.Process, error)
	Stop(context.Context, string) error
	Watch(context.Context, string, int64) ([]byte, <-chan []byte, <-chan error, error)
	WatchProcesses(context.Context, int) ([]runtimeDomain.Process, <-chan runtimeDomain.ProcessEvent, <-chan error, error)
}

func newDashboardBackend(owner *app, root string, client dashboardProcessClient) *dashboardBackend {
	return &dashboardBackend{
		owner:    owner,
		root:     root,
		client:   client,
		resolved: make(map[string]config.Resolved),
	}
}

func (b *dashboardBackend) Refresh(ctx context.Context) ([]tui.Entry, error) {
	discovery, err := b.owner.resolver().Discover(ctx, b.root)
	if err != nil {
		return nil, err
	}
	if err := b.owner.paths.Ensure(); err != nil {
		return nil, err
	}
	store, err := sqlite.Open(b.owner.paths.Database())
	if err != nil {
		return nil, err
	}
	defer store.Close()

	entries, err := b.entries(ctx, store, discovery.Applications)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeWorkspaceIndex(discovery.Applications)
	if err != nil {
		return nil, err
	}
	if err := store.SaveWorkspaceIndex(ctx, b.root, encoded, time.Now()); err != nil {
		return nil, err
	}
	b.replaceResolved(discovery.Applications)
	return entries, nil
}

func (b *dashboardBackend) Cached(ctx context.Context) ([]tui.Entry, error) {
	if err := b.owner.paths.Ensure(); err != nil {
		return nil, err
	}
	store, err := sqlite.Open(b.owner.paths.Database())
	if err != nil {
		return nil, err
	}
	defer store.Close()
	payload, exists, err := store.WorkspaceIndex(ctx, b.root)
	if err != nil || !exists {
		return nil, err
	}
	resolved, err := decodeWorkspaceIndex(payload)
	if err != nil {
		return nil, nil
	}
	entries, err := b.entries(ctx, store, resolved)
	if err != nil {
		return nil, err
	}
	b.replaceResolved(resolved)
	return entries, nil
}

func (b *dashboardBackend) entries(ctx context.Context, store *sqlite.Sessions, applications []config.Resolved) ([]tui.Entry, error) {
	entries := make([]tui.Entry, 0, len(applications))
	for _, resolved := range applications {
		entry := tui.Entry{Application: resolved.Application, Linked: resolved.Linked, Trusted: true}
		if resolved.Trust != nil {
			entry.TrustSummary = commandSummary(resolved.Application)
			trusted, err := store.Trusted(ctx, resolved.Trust.Path, resolved.Trust.Digest)
			if err != nil {
				return nil, err
			}
			entry.Trusted = trusted
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (b *dashboardBackend) replaceResolved(applications []config.Resolved) {
	resolvedByKey := make(map[string]config.Resolved, len(applications))
	for _, resolved := range applications {
		entry := tui.Entry{Application: resolved.Application}
		resolvedByKey[dashboardEntryKey(entry)] = resolved
	}
	b.mu.Lock()
	b.resolved = resolvedByKey
	b.mu.Unlock()
}

func (b *dashboardBackend) EnsurePlatform(ctx context.Context) tui.Platform {
	b.ensureMu.Lock()
	defer b.ensureMu.Unlock()
	if b.platform.State == tui.PlatformReady || b.platform.State == tui.PlatformLogin {
		return b.platform
	}
	var output bytes.Buffer
	manager := b.owner.platform()
	manager.NonInteractive = true
	manager.Stdin = strings.NewReader("")
	manager.Stdout = &output
	manager.Stderr = &output
	if err := manager.Up(ctx); err != nil {
		b.platform = tui.Platform{State: tui.PlatformFailed, Detail: platformFailure(output.String(), err)}
		return b.platform
	}
	if !manager.LoginStatus(ctx) {
		b.platform = tui.Platform{
			State:  tui.PlatformLogin,
			Detail: "Docker is reachable. Infisical, PostgreSQL, Redis, and Portless are ready. Infisical is not signed in.",
		}
		return b.platform
	}
	b.platform = tui.Platform{
		State:  tui.PlatformReady,
		Detail: "Docker is reachable. Infisical, PostgreSQL, Redis, and Portless are ready.",
	}
	return b.platform
}

func platformFailure(output string, err error) string {
	detail := err.Error()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		lower := strings.ToLower(line)
		if line == "" || !strings.Contains(lower, "error") && !strings.Contains(lower, "failed") && !strings.Contains(lower, "cannot connect") && !strings.Contains(lower, "permission denied") && !strings.Contains(lower, "no tty") {
			continue
		}
		if !strings.Contains(detail, line) {
			detail += ": " + line
		}
		break
	}
	return detail
}

func (b *dashboardBackend) WatchProcesses(ctx context.Context, limit int) ([]runtimeDomain.Process, <-chan runtimeDomain.ProcessEvent, <-chan error, error) {
	return b.client.WatchProcesses(ctx, limit)
}

func (b *dashboardBackend) WatchLogs(ctx context.Context, id string, limit int64) ([]byte, <-chan []byte, <-chan error, error) {
	return b.client.Watch(ctx, id, limit)
}

func (b *dashboardBackend) Start(ctx context.Context, entry tui.Entry) (runtimeDomain.Process, error) {
	if platformState := b.EnsurePlatform(ctx); platformState.State == tui.PlatformFailed {
		return runtimeDomain.Process{}, errors.New(platformState.Detail)
	}
	resolved, err := b.application(entry)
	if err != nil {
		return runtimeDomain.Process{}, err
	}
	if resolved.Trust != nil {
		store, openErr := sqlite.Open(b.owner.paths.Database())
		if openErr != nil {
			return runtimeDomain.Process{}, openErr
		}
		trusted, trustErr := store.Trusted(ctx, resolved.Trust.Path, resolved.Trust.Digest)
		_ = store.Close()
		if trustErr != nil {
			return runtimeDomain.Process{}, trustErr
		}
		if !trusted {
			return runtimeDomain.Process{}, fmt.Errorf("repository commands are not trusted")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return runtimeDomain.Process{}, fmt.Errorf("find Cassie executable: %w", err)
	}
	configPath, err := filepath.Abs(b.owner.configPath)
	if err != nil {
		return runtimeDomain.Process{}, fmt.Errorf("resolve Cassie configuration path: %w", err)
	}
	app := resolved.Application
	command := joinShellCommand(
		executable,
		"--config", configPath,
		"--dir", app.Root,
		"run", app.Name,
	)
	return b.client.Start(ctx, runtimeDomain.ProcessSpec{
		ID:      uuid.NewString(),
		App:     app.Name,
		Root:    app.Root,
		Command: command,
		Env:     b.managedEnvironment(configPath),
		Grace:   app.Grace.Duration + 5*time.Second,
	})
}

func (b *dashboardBackend) managedEnvironment(configPath string) map[string]string {
	values := b.owner.developmentEnvironment()
	values["CASSIE_CONFIG"] = configPath
	values["CASSIE_DATA"] = b.owner.paths.Data
	values["CASSIE_STATE"] = b.owner.paths.State
	for _, name := range []string{
		"PATH", "SHELL", "COMSPEC",
		"PORTLESS_PORT", "PORTLESS_APP_PORT", "PORTLESS_HTTPS", "PORTLESS_LAN",
		"PORTLESS_LAN_IP", "PORTLESS_TLD", "PORTLESS_WILDCARD", "PORTLESS_SYNC_HOSTS",
		"PORTLESS_TAILSCALE", "PORTLESS_FUNNEL", "PORTLESS_NGROK",
	} {
		if value := os.Getenv(name); value != "" {
			values[name] = value
		}
	}
	return values
}

func (b *dashboardBackend) Stop(ctx context.Context, id string) error {
	return b.client.Stop(ctx, id)
}

func (b *dashboardBackend) Trust(ctx context.Context, entry tui.Entry) error {
	resolved, err := b.application(entry)
	if err != nil {
		return err
	}
	if resolved.Trust == nil {
		return nil
	}
	store, err := sqlite.Open(b.owner.paths.Database())
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Trust(ctx, resolved.Trust.Path, resolved.Trust.Digest, time.Now())
}

func (b *dashboardBackend) Open(ctx context.Context, url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", url)
	case "windows":
		command = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.CommandContext(ctx, "xdg-open", url)
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("open %s: %w", url, err)
	}
	return nil
}

func (b *dashboardBackend) application(entry tui.Entry) (config.Resolved, error) {
	b.mu.RLock()
	resolved, exists := b.resolved[dashboardEntryKey(entry)]
	b.mu.RUnlock()
	if !exists {
		return config.Resolved{}, fmt.Errorf("application %q is no longer available", entry.Application.Name)
	}
	return resolved, nil
}

func dashboardEntryKey(entry tui.Entry) string {
	return entry.Application.Name + "\x00" + filepath.Clean(entry.Application.Root)
}

func joinShellCommand(arguments ...string) string {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, quoteShellArgument(argument))
	}
	return strings.Join(quoted, " ")
}

func quoteShellArgument(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type cachedApplication struct {
	Application catalog.Application `yaml:"application"`
	Root        string              `yaml:"root"`
	Sources     []string            `yaml:"sources,omitempty"`
	Trust       *config.Trust       `yaml:"trust,omitempty"`
	Linked      bool                `yaml:"linked"`
}

func encodeWorkspaceIndex(resolved []config.Resolved) ([]byte, error) {
	values := make([]cachedApplication, 0, len(resolved))
	for _, value := range resolved {
		values = append(values, cachedApplication{
			Application: value.Application,
			Root:        value.Application.Root,
			Sources:     value.Sources,
			Trust:       value.Trust,
			Linked:      value.Linked,
		})
	}
	encoded, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode workspace index: %w", err)
	}
	return encoded, nil
}

func decodeWorkspaceIndex(payload []byte) ([]config.Resolved, error) {
	var values []cachedApplication
	if err := yaml.Unmarshal(payload, &values); err != nil {
		return nil, fmt.Errorf("decode workspace index: %w", err)
	}
	resolved := make([]config.Resolved, 0, len(values))
	for _, value := range values {
		value.Application.Root = value.Root
		resolved = append(resolved, config.Resolved{
			Application: value.Application,
			Sources:     value.Sources,
			Trust:       value.Trust,
			Linked:      value.Linked,
		})
	}
	return resolved, nil
}

func (a *app) ensureDaemon(ctx context.Context) (*daemonadapter.Client, error) {
	if err := a.paths.Ensure(); err != nil {
		return nil, err
	}
	if client, err := daemonadapter.Dial(ctx, a.paths.State); err == nil {
		return client, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find Cassie executable: %w", err)
	}
	logPath := filepath.Join(a.paths.State, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}
	command := exec.Command(executable, "--config", a.configPath, "daemon", "serve")
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = os.Environ()
	detach(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start Cassie daemon: %w", err)
	}
	_ = command.Process.Release()
	_ = logFile.Close()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		dialCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		client, dialErr := daemonadapter.Dial(dialCtx, a.paths.State)
		cancel()
		if dialErr == nil {
			return client, nil
		}
		lastErr = dialErr
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("Cassie daemon did not become ready; see %s: %w", logPath, lastErr)
		case <-ticker.C:
		}
	}
}
