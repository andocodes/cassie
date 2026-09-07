//go:build windows

package process

import (
	"context"
	"os/exec"
)

func configureProcess(*exec.Cmd) {}

func wait(ctx context.Context, command *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		return <-done
	}
}
