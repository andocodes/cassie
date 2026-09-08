package processlog_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/andocodes/cassie/internal/adapters/processlog"
)

func TestStoreBoundsPersistedLogsAndStreamsNewOutput(t *testing.T) {
	store := processlog.New(t.TempDir(), 32)
	output, path, err := store.Open("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("expected a persisted log path")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := store.Follow(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case chunk := <-stream:
		if string(chunk) != "first\n" {
			t.Fatalf("streamed chunk = %q", chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for streamed output")
	}

	if _, err := output.Write([]byte(strings.Repeat("x", 64) + "tail")); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := store.Tail("session-1", 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) > 32 || !strings.HasSuffix(string(log), "tail") {
		t.Fatalf("bounded log = %q (%d bytes)", log, len(log))
	}
}

func TestStoreRejectsPathTraversal(t *testing.T) {
	store := processlog.New(t.TempDir(), 1024)
	if _, _, err := store.Open("../outside"); err == nil {
		t.Fatal("expected invalid process ID to fail")
	}
}
