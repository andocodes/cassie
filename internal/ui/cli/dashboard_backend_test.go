package cli

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andocodes/cassie/internal/adapters/config"
	"github.com/andocodes/cassie/internal/adapters/system"
	"github.com/andocodes/cassie/internal/domain/catalog"
	runtimeDomain "github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/andocodes/cassie/internal/ui/tui"
)

type recordingDaemon struct {
	spec runtimeDomain.ProcessSpec
}

func (d *recordingDaemon) Start(_ context.Context, spec runtimeDomain.ProcessSpec) (runtimeDomain.Process, error) {
	d.spec = spec
	return runtimeDomain.Process{ID: spec.ID, App: spec.App, Root: spec.Root, Status: runtimeDomain.StatusRunning}, nil
}

func (*recordingDaemon) Stop(context.Context, string) error { return nil }
func (*recordingDaemon) Watch(context.Context, string, int64) ([]byte, <-chan []byte, <-chan error, error) {
	return nil, make(chan []byte), make(chan error), nil
}
func (*recordingDaemon) WatchProcesses(context.Context, int) ([]runtimeDomain.Process, <-chan runtimeDomain.ProcessEvent, <-chan error, error) {
	return nil, make(chan runtimeDomain.ProcessEvent), make(chan error), nil
}

func TestWorkspaceIndexPreservesResolvedApplicationState(t *testing.T) {
	want := config.Resolved{
		Application: catalog.Application{
			Name: "phoebe-ui", Domain: "phoebe-ui", Root: "/work/phoebe-ui",
			Grace:    catalog.Duration{Duration: 12 * time.Second},
			Commands: []catalog.Command{{Run: "pnpm dev", Dir: "web"}},
		},
		Sources: []string{"/work/phoebe-ui/.cassie.yaml"},
		Trust:   &config.Trust{Path: "/work/phoebe-ui/.cassie.yaml", Digest: "abc"},
		Linked:  true,
	}
	payload, err := encodeWorkspaceIndex([]config.Resolved{want})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeWorkspaceIndex(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Application.Root != want.Application.Root || got[0].Application.Grace.Duration != want.Application.Grace.Duration || got[0].Trust == nil || got[0].Trust.Digest != "abc" || !got[0].Linked {
		t.Fatalf("cached application = %#v", got)
	}
}

func TestManagedRunCommandQuotesArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell quoting")
	}
	command := joinShellCommand("/Applications/Cassie's Bin/cassie", "run", "phoebe's-ui")
	for _, want := range []string{`'/Applications/Cassie'\''s Bin/cassie'`, `'phoebe'\''s-ui'`} {
		if !strings.Contains(command, want) {
			t.Fatalf("command %q does not contain %q", command, want)
		}
	}
}

func TestDashboardStartsTheCompleteCassieRunLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app with spaces")
	entry := tui.Entry{Application: catalog.Application{
		Name: "phoebe-ui", Root: root, Domain: "phoebe-ui",
		Grace:    catalog.Duration{Duration: 10 * time.Second},
		Commands: []catalog.Command{{Run: "pnpm dev"}},
	}}
	client := &recordingDaemon{}
	backend := newDashboardBackend(&app{
		configPath: filepath.Join(root, "user config.yaml"),
		paths: system.Paths{
			Config: filepath.Join(root, "user config.yaml"),
			Data:   filepath.Join(root, "data"),
			State:  filepath.Join(root, "state"),
		},
	}, root, client)
	backend.platform = tui.Platform{State: tui.PlatformReady}
	backend.resolved[dashboardEntryKey(entry)] = config.Resolved{Application: entry.Application}
	process, err := backend.Start(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if process.Status != runtimeDomain.StatusRunning || client.spec.App != "phoebe-ui" || client.spec.Root != root {
		t.Fatalf("managed process = %#v, spec = %#v", process, client.spec)
	}
	for _, want := range []string{"--config", "--at", "run", "phoebe-ui"} {
		if !strings.Contains(client.spec.Command, want) {
			t.Fatalf("managed command %q does not contain %q", client.spec.Command, want)
		}
	}
	if client.spec.Env["CASSIE_STATE"] == "" || client.spec.Env["PORTLESS_STATE_DIR"] == "" {
		t.Fatalf("managed request is missing Cassie runtime paths: %#v", client.spec.Env)
	}
	if _, exists := client.spec.Env["SECRET"]; exists {
		t.Fatalf("managed request contains a secret value: %#v", client.spec.Env)
	}
}
