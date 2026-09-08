//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

type instanceLock struct {
	file *os.File
}

func acquireInstanceLock(path string) (*instanceLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock daemon instance: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("reset daemon lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write daemon lock: %w", err)
	}
	return &instanceLock{file: file}, nil
}

func (l *instanceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		return fmt.Errorf("unlock daemon instance: %w", err)
	}
	return l.file.Close()
}
