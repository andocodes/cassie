//go:build windows

package cli

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

func detach(command *exec.Cmd) {
	command.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}
