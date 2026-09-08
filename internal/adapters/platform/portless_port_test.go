package platform

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestAutomaticPortlessRetriesFiveDistinctPorts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	first, last := freePortRange(t, 6)
	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	binary := filepath.Join(root, "portless")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CASSIE_CALLS\"\nprintf 'Port is already in use.\\n' >&2\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := Manager{PortFile: filepath.Join(root, "selected-port")}
	router := Portless{Binary: binary, Env: map[string]string{"CASSIE_CALLS": calls}}
	_, err := manager.ensureAutomaticPortless(context.Background(), router, proxyPortPolicy{first: first, last: last, attempts: 5})
	if err == nil || !strings.Contains(err.Error(), "after 5 attempts") {
		t.Fatalf("error = %v, want five-attempt failure", err)
	}
	content, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatal(readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	seen := make(map[string]bool)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 5 || fields[0] != "proxy" || fields[1] != "start" || fields[2] != "--port" || fields[4] != "--https" {
			t.Fatalf("unexpected call: %q", line)
		}
		seen[fields[3]] = true
	}
	if len(lines) != 5 || len(seen) != 5 {
		t.Fatalf("calls = %q, want five distinct attempts", content)
	}
}

func TestAutomaticPortlessPersistsSuccessfulPort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	first, last := freePortRange(t, 2)
	root := t.TempDir()
	binary := filepath.Join(root, "portless")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := Manager{PortFile: filepath.Join(root, "selected-port")}
	port, err := manager.ensureAutomaticPortless(context.Background(), Portless{Binary: binary}, proxyPortPolicy{first: first, last: last, attempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if port < first || port > last {
		t.Fatalf("selected port = %d, want %d-%d", port, first, last)
	}
	if stored := StoredProxyPort(manager.PortFile); stored != port {
		t.Fatalf("stored port = %d, want %d", stored, port)
	}
}

func TestAutomaticPortlessFailsWhenRangeIsOccupied(t *testing.T) {
	listeners, first, last := occupiedPortRange(t, 2)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	manager := Manager{PortFile: filepath.Join(t.TempDir(), "selected-port")}
	_, err := manager.ensureAutomaticPortless(context.Background(), Portless{}, proxyPortPolicy{first: first, last: last, attempts: 5})
	if err == nil || !strings.Contains(err.Error(), "no Portless proxy ports are available") {
		t.Fatalf("error = %v, want occupied-range failure", err)
	}
}

func freePortRange(t *testing.T, count int) (int, int) {
	t.Helper()
	listeners, first, last := occupiedPortRange(t, count)
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return first, last
}

func occupiedPortRange(t *testing.T, count int) ([]net.Listener, int, int) {
	t.Helper()
	for first := 20000; first < 60000-count; first++ {
		listeners, ok := listenPortRange(first, count)
		if ok {
			return listeners, first, first + count - 1
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}
	t.Fatal("could not reserve a port range")
	return nil, 0, 0
}

func listenPortRange(first, count int) ([]net.Listener, bool) {
	listeners := make([]net.Listener, 0, count)
	for port := first; port < first+count; port++ {
		listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			return listeners, false
		}
		listeners = append(listeners, listener)
	}
	return listeners, true
}
