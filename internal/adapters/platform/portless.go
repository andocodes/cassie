package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/andocodes/cassie/internal/domain/catalog"
)

type Portless struct {
	Binary string
	Env    map[string]string
}

func (p Portless) Wrap(app catalog.Application, command catalog.Command) catalog.Command {
	binary := p.Binary
	if binary == "" {
		binary = "portless"
	}
	command.Run = shellWord(binary) + " --name " + shellWord(app.Domain) + " " + command.Run
	return command
}

func (p Portless) Alias(ctx context.Context, domain string, port int) error {
	binary := p.Binary
	if binary == "" {
		binary = "portless"
	}
	command := exec.CommandContext(ctx, binary, "alias", domain, strconv.Itoa(port))
	command.Env = appendEnv(os.Environ(), p.Env)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("register Portless route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (p Portless) Remove(ctx context.Context, domain string) error {
	binary := p.Binary
	if binary == "" {
		binary = "portless"
	}
	command := exec.CommandContext(ctx, binary, "alias", "--remove", domain)
	command.Env = appendEnv(os.Environ(), p.Env)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("remove Portless route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func appendEnv(base []string, values map[string]string) []string {
	for key, value := range values {
		base = append(base, key+"="+value)
	}
	return base
}

func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
