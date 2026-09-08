//go:build !windows

package platform

import (
	"os"
	"strings"
)

func portlessCommand(binary, domain, command string) string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shellWord(binary) + " --name " + shellWord(domain) + " -- " + shellWord(shell) + " -lc " + shellWord(command)
}

func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
