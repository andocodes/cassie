package platform

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/andocodes/cassie/internal/domain/catalog"
)

type Portless struct {
	Binary string
	Env    map[string]string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (p Portless) Ready(ctx context.Context) (bool, error) {
	command := exec.CommandContext(ctx, p.binary(), "doctor")
	command.Env = appendEnv(os.Environ(), p.Env)
	output, err := command.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("check Portless proxy: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return !strings.Contains(string(output), "Proxy is not running"), nil
}

func (p Portless) InstallService(ctx context.Context) error {
	args := []string{"service", "install"}
	if stateDir := strings.TrimSpace(p.Env["PORTLESS_STATE_DIR"]); stateDir != "" {
		args = append(args, "--state-dir", stateDir)
	}
	command := exec.CommandContext(ctx, p.binary(), args...)
	command.Env = appendEnv(os.Environ(), p.Env)
	command.Stdin = p.Stdin
	command.Stdout = p.Stdout
	command.Stderr = p.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("install Portless service: %w", err)
	}
	return nil
}

func (p Portless) Wrap(app catalog.Application, command catalog.Command) catalog.Command {
	binary := p.Binary
	if binary == "" {
		binary = "portless"
	}
	command.Run = portlessCommand(binary, app.Domain, command.Run)
	return command
}

func (p Portless) Alias(ctx context.Context, domain string, port int) error {
	command := exec.CommandContext(ctx, p.binary(), "alias", domain, strconv.Itoa(port))
	command.Env = appendEnv(os.Environ(), p.Env)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("register Portless route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (p Portless) Remove(ctx context.Context, domain string) error {
	command := exec.CommandContext(ctx, p.binary(), "alias", "--remove", domain)
	command.Env = appendEnv(os.Environ(), p.Env)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("remove Portless route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (p Portless) binary() string {
	if p.Binary == "" {
		return "portless"
	}
	return p.Binary
}

func appendEnv(base []string, values map[string]string) []string {
	for key, value := range values {
		base = append(base, key+"="+value)
	}
	return base
}
