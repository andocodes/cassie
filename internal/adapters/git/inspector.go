package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andocodes/cassie/internal/ports"
)

var ErrNotRepository = errors.New("not a git repository")

type Inspector struct{}

func (Inspector) Inspect(ctx context.Context, dir string) (ports.Repository, error) {
	root, err := output(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ports.Repository{}, ErrNotRepository
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return ports.Repository{}, fmt.Errorf("resolve repository root: %w", err)
	}
	remote, _ := output(ctx, root, "config", "--get", "remote.origin.url")
	return ports.Repository{Root: root, Remote: NormalizeRemote(remote)}, nil
}

func output(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	value, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

func NormalizeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if strings.HasPrefix(remote, "git@") {
		remote = strings.TrimPrefix(remote, "git@")
		remote = strings.Replace(remote, ":", "/", 1)
	} else if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		remote = parsed.Host + parsed.Path
	}
	remote = strings.TrimPrefix(remote, "ssh://")
	remote = strings.TrimSuffix(remote, ".git")
	remote = strings.Trim(remote, "/")
	return strings.ToLower(remote)
}
