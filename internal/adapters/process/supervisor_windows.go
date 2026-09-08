//go:build windows

package process

import "os/exec"

func interruptManagedProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

func killManagedProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
