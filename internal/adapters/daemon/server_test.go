package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andocodes/cassie/internal/adapters/daemon"
	"github.com/andocodes/cassie/internal/adapters/process"
	"github.com/andocodes/cassie/internal/adapters/processlog"
	"github.com/andocodes/cassie/internal/adapters/sqlite"
	domainruntime "github.com/andocodes/cassie/internal/domain/runtime"
)

func TestClientReconnectsToManagedProcessAndStreamsLogs(t *testing.T) {
	state := t.TempDir()
	store, err := sqlite.Open(state + "/cassie.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	supervisor := process.NewSupervisor(store, processlog.New(state+"/logs", 1024))
	server := daemon.NewServer(state, supervisor)
	serverCtx, stopServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(serverCtx) }()
	t.Cleanup(func() {
		stopServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("stop daemon: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	client := waitForClient(t, state)
	assertEndpointIsPrivateAndAuthenticated(t, state)
	eventsCtx, cancelEvents := context.WithCancel(context.Background())
	defer cancelEvents()
	initialProcesses, processEvents, processErrors, err := client.WatchProcesses(eventsCtx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(initialProcesses) != 0 {
		t.Fatalf("initial processes = %#v", initialProcesses)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 2*time.Second)
	managed, err := client.Start(startCtx, domainruntime.ProcessSpec{
		ID:      "process-1",
		App:     "phoebe-ui",
		Root:    state,
		Command: daemonTestCommand(),
		Grace:   100 * time.Millisecond,
	})
	cancelStart()
	if err != nil {
		t.Fatal(err)
	}
	expectProcessEvent(t, processEvents, processErrors, domainruntime.ProcessStarted)

	// A new client can reconnect after the request-scoped client has gone away.
	reconnected := waitForClient(t, state)
	current, err := reconnected.Process(context.Background(), managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domainruntime.StatusRunning {
		t.Fatalf("reconnected status = %q", current.Status)
	}

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	initial, chunks, streamErrors, err := reconnected.Watch(watchCtx, managed.ID, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(initial), "ready") {
		select {
		case chunk := <-chunks:
			if !strings.Contains(string(chunk), "ready") {
				t.Fatalf("log chunk = %q", chunk)
			}
		case err := <-streamErrors:
			t.Fatalf("watch logs: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for managed process log")
		}
	}
	cancelWatch()

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStop()
	if err := reconnected.Stop(stopCtx, managed.ID); err != nil {
		t.Fatal(err)
	}
	current, err = reconnected.Process(context.Background(), managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domainruntime.StatusStopped {
		t.Fatalf("status after stop = %q", current.Status)
	}
	expectProcessEvent(t, processEvents, processErrors, domainruntime.ProcessFinished)
}

func TestServerRejectsSecondInstance(t *testing.T) {
	state := t.TempDir()
	store, err := sqlite.Open(state + "/cassie.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	supervisor := process.NewSupervisor(store, processlog.New(state+"/logs", 1024))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.NewServer(state, supervisor).Serve(ctx) }()
	_ = waitForClient(t, state)

	err = daemon.NewServer(state, supervisor).Serve(context.Background())
	if !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("second server error = %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitForClient(t *testing.T, state string) *daemon.Client {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		client, err := daemon.Dial(ctx, state)
		cancel()
		if err == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
	return nil
}

func daemonTestCommand() string {
	if runtime.GOOS == "windows" {
		return "ping 127.0.0.1 -n 2 > nul & echo ready & ping 127.0.0.1 -n 30 > nul"
	}
	return "sleep 0.1; printf 'ready\\n'; while :; do sleep 1; done"
}

func expectProcessEvent(t *testing.T, events <-chan domainruntime.ProcessEvent, streamErrors <-chan error, eventType domainruntime.ProcessEventType) {
	t.Helper()
	select {
	case event := <-events:
		if event.Type != eventType || event.Process.ID != "process-1" {
			t.Fatalf("process event = %#v", event)
		}
	case err := <-streamErrors:
		t.Fatalf("watch process events: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q process event", eventType)
	}
}

func assertEndpointIsPrivateAndAuthenticated(t *testing.T, state string) {
	t.Helper()
	path := state + "/daemon.endpoint.json"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("endpoint permissions = %o, want 600", info.Mode().Perm())
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var endpoint struct {
		Network string `json:"network"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(encoded, &endpoint); err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial(endpoint.Network, endpoint.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(map[string]any{
		"version": 1,
		"id":      "unauthorized",
		"token":   "wrong-token",
		"method":  "daemon.ping",
	}); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || !strings.Contains(response.Error.Message, "authentication") {
		t.Fatalf("unauthorized response = %#v", response)
	}
}
