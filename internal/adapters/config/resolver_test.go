package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/andocodes/cassie/internal/adapters/config"
	"github.com/andocodes/cassie/internal/ports"
)

type fakeInspector struct {
	repository ports.Repository
}

func (f fakeInspector) Inspect(context.Context, string) (ports.Repository, error) {
	return f.repository, nil
}

func TestRepositoryConfigOverridesUserAppAndInheritsDefaults(t *testing.T) {
	root := t.TempDir()
	userPath := filepath.Join(t.TempDir(), "config.yaml")
	write(t, userPath, `
version: 1
defaults:
  grace: 15s
  secrets:
    environment: dev
    path: /
apps:
  atlas-ui:
    match:
      repo: github.com/acme/atlas-ui
    domain: atlas-user
    port: 3000
    commands:
      - pnpm dev
    cleanup:
      - stop-user
    secrets:
      project: atlas
`)
	write(t, filepath.Join(root, ".cassie.yaml"), `
version: 1
name: atlas-ui
domain: atlas-repo
commands:
  - docker compose up
cleanup: null
secrets:
  environment: local
`)

	resolver := config.Resolver{
		UserPath: userPath,
		Git: fakeInspector{repository: ports.Repository{
			Root:   root,
			Remote: "github.com/acme/atlas-ui",
		}},
	}
	resolved, err := resolver.Resolve(context.Background(), root, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	app := resolved.Application
	if app.Domain != "atlas-repo" {
		t.Fatalf("expected repository domain, got %q", app.Domain)
	}
	if app.Port != 3000 {
		t.Fatalf("expected inherited port 3000, got %d", app.Port)
	}
	if len(app.Commands) != 1 || app.Commands[0].Run != "docker compose up" {
		t.Fatalf("expected repository commands to replace user commands, got %#v", app.Commands)
	}
	if app.Cleanup != nil {
		t.Fatalf("expected null to clear inherited cleanup, got %#v", app.Cleanup)
	}
	if app.Secrets.Project != "atlas" || app.Secrets.Environment != "local" || app.Secrets.Path != "/" {
		t.Fatalf("unexpected merged secret binding: %#v", app.Secrets)
	}
	if app.Grace.String() != "15s" {
		t.Fatalf("expected global grace, got %s", app.Grace)
	}
	if app.Root != root {
		t.Fatalf("expected root %q, got %q", root, app.Root)
	}
	if resolved.Trust == nil || resolved.Trust.Path != filepath.Join(root, ".cassie.yaml") {
		t.Fatalf("expected repository commands to require trust, got %#v", resolved.Trust)
	}
}

func TestUserAppWorksWithoutRepositoryFile(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(t.TempDir(), "config.yaml")
	write(t, userPath, `
version: 1
apps:
  web:
    match:
      repo: github.com/acme/platform
      dir: apps/web
    domain: atlas-ui
    commands:
      - run: bun run dev
        dir: frontend
`)

	resolver := config.Resolver{
		UserPath: userPath,
		Git: fakeInspector{repository: ports.Repository{
			Root:   root,
			Remote: "github.com/acme/platform",
		}},
	}
	resolved, err := resolver.Resolve(context.Background(), appDir, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if resolved.Application.Name != "web" {
		t.Fatalf("expected map key as inferred name, got %q", resolved.Application.Name)
	}
	if resolved.Application.Root != appDir {
		t.Fatalf("expected monorepo app root %q, got %q", appDir, resolved.Application.Root)
	}
	if len(resolved.Application.Commands) != 1 || resolved.Application.Commands[0].Dir != "frontend" {
		t.Fatalf("unexpected commands: %#v", resolved.Application.Commands)
	}
}

func TestRemoteMatchRunsFromRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(t.TempDir(), "config.yaml")
	write(t, userPath, `
version: 1
apps:
  api:
    match:
      repo: git@github.com:acme/api.git
    domain: api
    commands: [go run .]
`)
	resolver := config.Resolver{
		UserPath: userPath,
		Git: fakeInspector{repository: ports.Repository{
			Root: root, Remote: "github.com/acme/api",
		}},
	}
	resolved, err := resolver.Resolve(context.Background(), nested, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Application.Root != root {
		t.Fatalf("application root = %q, want repository root %q", resolved.Application.Root, root)
	}
}

func TestResolverRejectsUnknownApplicationFields(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".cassie.yaml"), `
version: 1
name: web
domain: web
commmands:
  - bun run dev
`)
	resolver := config.Resolver{
		UserPath: filepath.Join(t.TempDir(), "config.yaml"),
		Git:      fakeInspector{repository: ports.Repository{Root: root}},
	}
	if _, err := resolver.Resolve(context.Background(), root, ""); err == nil {
		t.Fatal("expected misspelled field to be rejected")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
