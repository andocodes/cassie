package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/andocodes/cassie/internal/ports"
)

type Runner struct{}

func (Runner) Run(ctx context.Context, process ports.Process) error {
	if process.Command.Run == "" {
		return fmt.Errorf("run command is empty")
	}
	dir := process.Root
	if process.Command.Dir != "" {
		dir = filepath.Join(process.Root, process.Command.Dir)
	}
	shell, args := shellCommand(process.Command.Run)
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.Command(shell, args...)
	command.Dir = dir
	command.Env = mergeEnv(os.Environ(), process.Env)
	command.Stdin = process.Stdin
	command.Stdout = process.Stdout
	command.Stderr = process.Stderr
	configureProcess(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %q: %w", process.Command.Run, err)
	}
	if err := wait(ctx, command); err != nil {
		return fmt.Errorf("run %q: %w", process.Command.Run, err)
	}
	return nil
}

func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		if shell := os.Getenv("COMSPEC"); shell != "" {
			return shell, []string{"/D", "/S", "/C", command}
		}
		return "cmd.exe", []string{"/D", "/S", "/C", command}
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell, []string{"-lc", command}
	}
	return "/bin/sh", []string{"-lc", command}
}

func mergeEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		return base
	}
	values := make(map[string]string, len(base)+len(overlay))
	for _, item := range base {
		for index := 0; index < len(item); index++ {
			if item[index] == '=' {
				values[item[:index]] = item[index+1:]
				break
			}
		}
	}
	for key, value := range overlay {
		values[key] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
