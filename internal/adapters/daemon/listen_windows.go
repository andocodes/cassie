//go:build windows

package daemon

import (
	"fmt"
	"net"
)

// The protocol depends only on net.Conn, so it can move to a named pipe
// without changing clients or handlers. Loopback plus the endpoint token is
// the dependency-free Windows transport for the initial daemon foundation.
func listenLocal(string) (net.Listener, endpoint, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, endpoint{}, nil, fmt.Errorf("listen for local daemon clients: %w", err)
	}
	cleanup := func() { _ = listener.Close() }
	return listener, endpoint{Network: "tcp", Address: listener.Addr().String()}, cleanup, nil
}
