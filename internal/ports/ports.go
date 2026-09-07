package ports

import (
	"context"
	"io"
	"time"

	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/domain/runtime"
)

type Repository struct {
	Root   string
	Remote string
}

type RepositoryInspector interface {
	Inspect(ctx context.Context, dir string) (Repository, error)
}

type Process struct {
	Command catalog.Command
	Root    string
	Env     map[string]string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

type ProcessRunner interface {
	Run(ctx context.Context, process Process) error
}

type SecretProvider interface {
	Load(ctx context.Context, binding catalog.SecretBinding) (map[string]string, error)
}

type PreparedEnvironment struct {
	Values  map[string]string
	Cleanup func() error
}

type EnvironmentPreparer interface {
	Prepare(ctx context.Context, sessionID string, app catalog.Application, values map[string]string) (PreparedEnvironment, error)
}

type SessionStore interface {
	Start(ctx context.Context, session runtime.Session) error
	Finish(ctx context.Context, id string, status runtime.Status, exitCode int, endedAt time.Time) error
	Recent(ctx context.Context, limit int) ([]runtime.Session, error)
}

type TrustStore interface {
	Trusted(ctx context.Context, path, digest string) (bool, error)
	Trust(ctx context.Context, path, digest string, trustedAt time.Time) error
}

type Router interface {
	Wrap(app catalog.Application, command catalog.Command) catalog.Command
	Alias(ctx context.Context, domain string, port int) error
	Remove(ctx context.Context, domain string) error
}
