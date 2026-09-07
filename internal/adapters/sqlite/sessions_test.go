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
