//go:build windows

package daemon

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

type instanceLock struct {
	handle windows.Handle
}

func acquireInstanceLock(path string) (*instanceLock, error) {
	digest := sha256.Sum256([]byte(path))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("Local\\CassieDaemon-%x", digest[:8]))
	if err != nil {
		return nil, fmt.Errorf("name daemon lock: %w", err)
	}
	// A named event provides kernel-managed single-instance ownership without
	// thread affinity. A Windows mutex cannot be safely released after a Go
	// goroutine migrates to another operating-system thread.
	handle, err := windows.CreateEvent(nil, 1, 0, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("create daemon lock: %w", err)
	}
	return &instanceLock{handle: handle}, nil
}

func (l *instanceLock) Close() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	if err := windows.CloseHandle(l.handle); err != nil {
		return fmt.Errorf("close daemon lock: %w", err)
	}
	return nil
}
