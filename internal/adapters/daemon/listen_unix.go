//go:build !windows

package daemon

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

func listenLocal(stateDir string) (net.Listener, endpoint, func(), error) {
	address := filepath.Join(stateDir, "daemon.sock")
	// Unix-domain socket paths are commonly limited to roughly 100 bytes. Test
	// directories and custom CASSIE_STATE values can be longer than that.
	if len(address) > 96 {
		digest := sha256.Sum256([]byte(stateDir))
		address = filepath.Join(os.TempDir(), fmt.Sprintf("cassie-%x.sock", digest[:8]))
	}
	if err := os.Remove(address); err != nil && !os.IsNotExist(err) {
		return nil, endpoint{}, nil, fmt.Errorf("remove stale daemon socket: %w", err)
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, endpoint{}, nil, fmt.Errorf("listen on daemon socket: %w", err)
	}
	if err := os.Chmod(address, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(address)
		return nil, endpoint{}, nil, fmt.Errorf("protect daemon socket: %w", err)
	}
	cleanup := func() {
		_ = listener.Close()
		_ = os.Remove(address)
	}
	return listener, endpoint{Network: "unix", Address: address}, cleanup, nil
}
