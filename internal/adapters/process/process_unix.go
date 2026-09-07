//go:build !windows

package process

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func wait(ctx context.Context, command *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if command.Process != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGINT)
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case err := <-done:
			return err
		case <-timer.C:
			if command.Process != nil {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			}
			return <-done
		}
	}
}
