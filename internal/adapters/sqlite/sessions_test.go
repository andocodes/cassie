package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/andocodes/cassie/internal/adapters/sqlite"
	"github.com/andocodes/cassie/internal/domain/runtime"
)

func TestSessionsRoundTrip(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "cassie.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	started := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	session := runtime.Session{
		ID: "session-1", App: "atlas", Root: "/work/atlas",
		Status: runtime.StatusRunning, StartedAt: started,
	}
	if err := store.Start(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(context.Background(), session.ID, runtime.StatusSucceeded, 0, started.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	recent, err := store.Recent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Status != runtime.StatusSucceeded || recent[0].ExitCode == nil || *recent[0].ExitCode != 0 {
		t.Fatalf("unexpected sessions: %#v", recent)
	}

	trusted, err := store.Trusted(context.Background(), "/work/atlas/.cassie.yaml", "digest")
	if err != nil || trusted {
		t.Fatalf("expected untrusted config, got trusted=%v err=%v", trusted, err)
	}
	if err := store.Trust(context.Background(), "/work/atlas/.cassie.yaml", "digest", started); err != nil {
		t.Fatal(err)
	}
	trusted, err = store.Trusted(context.Background(), "/work/atlas/.cassie.yaml", "digest")
	if err != nil || !trusted {
		t.Fatalf("expected trusted config, got trusted=%v err=%v", trusted, err)
	}
	trusted, err = store.Trusted(context.Background(), "/work/atlas/.cassie.yaml", "changed")
	if err != nil || trusted {
		t.Fatalf("changed digest must not remain trusted, got trusted=%v err=%v", trusted, err)
	}
}

func TestManagedProcessesRoundTripWithoutEnvironment(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "cassie.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	started := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	process := runtime.Process{
		ID: "process-1", App: "phoebe-ui", Root: "/work/phoebe-ui", PID: 42,
		Status: runtime.StatusRunning, StartedAt: started, LogPath: "/state/logs/process-1.log",
	}
	if err := store.StartProcess(context.Background(), process); err != nil {
		t.Fatal(err)
	}

	running, err := store.RunningProcesses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0] != process {
		t.Fatalf("running processes = %#v, want %#v", running, []runtime.Process{process})
	}

	ended := started.Add(time.Minute)
	if err := store.FinishProcess(context.Background(), process.ID, runtime.StatusStopped, 130, ended); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Process(context.Background(), process.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != runtime.StatusStopped || stored.ExitCode == nil || *stored.ExitCode != 130 || stored.EndedAt == nil || !stored.EndedAt.Equal(ended) {
		t.Fatalf("finished process = %#v", stored)
	}

	running, err = store.RunningProcesses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 0 {
		t.Fatalf("running processes after finish = %#v", running)
	}
}

func TestWorkspaceIndexReplacesTheCachedSnapshot(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "cassie.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if payload, exists, err := store.WorkspaceIndex(ctx, "/work"); err != nil || exists || payload != nil {
		t.Fatalf("empty index = %q, %v, %v", payload, exists, err)
	}
	if err := store.SaveWorkspaceIndex(ctx, "/work", []byte("first"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkspaceIndex(ctx, "/work", []byte("second"), time.Now()); err != nil {
		t.Fatal(err)
	}
	payload, exists, err := store.WorkspaceIndex(ctx, "/work")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || string(payload) != "second" {
		t.Fatalf("index = %q, %v", payload, exists)
	}
}
