//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

func interruptManagedProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return syscall.Kill(-command.Process.Pid, syscall.SIGINT)
}

func killManagedProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
