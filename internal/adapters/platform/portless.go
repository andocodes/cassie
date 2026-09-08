package platform

import (
	"bytes"
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

func (p Portless) Start(ctx context.Context) error {
	return p.start(ctx, "proxy", "start")
}

func (p Portless) StartOn(ctx context.Context, port int) error {
	return p.start(ctx, "proxy", "start", "--port", strconv.Itoa(port), "--https")
}

func (p Portless) start(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, p.binary(), args...)
	command.Env = appendEnv(os.Environ(), p.Env)
	command.Stdin = p.Stdin
	var diagnostics bytes.Buffer
	command.Stdout = outputWriter(p.Stdout, &diagnostics)
	command.Stderr = outputWriter(p.Stderr, &diagnostics)
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(diagnostics.String())
		if detail != "" {
			return fmt.Errorf("start Portless proxy: %w: %s", err, detail)
		}
		return fmt.Errorf("start Portless proxy: %w", err)
	}
	return nil
}

func (p Portless) Stop(ctx context.Context) error {
	command := exec.CommandContext(ctx, p.binary(), "proxy", "stop")
	command.Env = appendEnv(os.Environ(), p.Env)
	command.Stdin = p.Stdin
	command.Stdout = p.Stdout
	command.Stderr = p.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("stop Portless proxy: %w", err)
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
	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[key] = struct{}{}
	}
	result := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := keys[key]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func outputWriter(destination io.Writer, diagnostics io.Writer) io.Writer {
	if destination == nil {
		return diagnostics
	}
	return io.MultiWriter(destination, diagnostics)
}
