//go:build windows

package platform

import (
	"os"
	"strings"
)

func portlessCommand(binary, domain, command string) string {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return windowsWord(binary) + " --name " + windowsWord(domain) + " -- " + windowsWord(shell) + " /D /S /C " + windowsWord(command)
}

func windowsWord(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
