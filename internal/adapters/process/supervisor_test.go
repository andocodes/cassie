package process_test

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andocodes/cassie/internal/adapters/process"
	"github.com/andocodes/cassie/internal/adapters/processlog"
	"github.com/andocodes/cassie/internal/adapters/sqlite"
	domainruntime "github.com/andocodes/cassie/internal/domain/runtime"
)

func TestSupervisorOwnsProcessAfterStartContextEnds(t *testing.T) {
	state := t.TempDir()
	store, err := sqlite.Open(state + "/cassie.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logs := processlog.New(state+"/logs", 1024)
	supervisor := process.NewSupervisor(store, logs)

	startCtx, cancelStart := context.WithCancel(context.Background())
	managed, err := supervisor.Start(startCtx, domainruntime.ProcessSpec{
		ID:      "process-1",
		App:     "phoebe-ui",
		Root:    state,
		Command: longRunningCommand(),
		Grace:   100 * time.Millisecond,
		Env:     map[string]string{"CASSIE_TEST_SECRET": "never-persist-this-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelStart()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = supervisor.Shutdown(ctx)
	})

	time.Sleep(100 * time.Millisecond)
	current, err := supervisor.Process(context.Background(), managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domainruntime.StatusRunning {
		t.Fatalf("status after client cancellation = %q", current.Status)
	}
	_, err = supervisor.Start(context.Background(), domainruntime.ProcessSpec{
		ID: "process-2", App: "phoebe-ui", Root: state, Command: longRunningCommand(),
	})
	if !errors.Is(err, domainruntime.ErrApplicationRunning) {
		t.Fatalf("second application process error = %v", err)
	}
	waitForLog(t, supervisor, managed.ID, "ready")

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStop()
	if err := supervisor.Stop(stopCtx, managed.ID); err != nil {
		t.Fatal(err)
	}
	current, err = supervisor.Process(context.Background(), managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domainruntime.StatusStopped || current.ExitCode == nil || *current.ExitCode != 130 {
		t.Fatalf("stopped process = %#v", current)
	}
	database, err := os.ReadFile(state + "/cassie.db")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(database), "never-persist-this-value") {
		t.Fatal("managed process environment was persisted")
	}
}

func TestSupervisorReconcilesPreviousDaemonRecords(t *testing.T) {
	state := t.TempDir()
	store, err := sqlite.Open(state + "/cassie.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	started := time.Now().Add(-time.Minute)
	if err := store.StartProcess(context.Background(), domainruntime.Process{
		ID: "old-process", App: "phoebe-api", Root: state, PID: 999999,
		Status: domainruntime.StatusRunning, StartedAt: started, LogPath: state + "/old.log",
	}); err != nil {
		t.Fatal(err)
	}
	supervisor := process.NewSupervisor(store, processlog.New(state+"/logs", 1024))
	if err := supervisor.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := supervisor.Process(context.Background(), "old-process")
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domainruntime.StatusInterrupted || current.EndedAt == nil {
		t.Fatalf("reconciled process = %#v", current)
	}
}

func waitForLog(t *testing.T, supervisor *process.Supervisor, id, expected string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, err := supervisor.Tail(context.Background(), id, 1024)
		if err == nil && strings.Contains(string(value), expected) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("log did not contain %q", expected)
}

func longRunningCommand() string {
	if runtime.GOOS == "windows" {
		return "echo ready & ping 127.0.0.1 -n 30 > nul"
	}
	return "printf 'ready\\n'; while :; do sleep 1; done"
}
