package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/andocodes/cassie/internal/adapters/config"
	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/ports"
)

func TestWriterSavesRepoFreeUserApplication(t *testing.T) {
	root := t.TempDir()
	userPath := filepath.Join(t.TempDir(), "config.yaml")
	writer := config.Writer{UserPath: userPath}
	app := catalog.Application{
		Name:     "atlas",
		Domain:   "atlas",
		Commands: []catalog.Command{{Run: "bun run dev"}},
	}
	if err := writer.SaveUser("atlas", config.Match{Repo: "github.com/acme/atlas"}, app); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(userPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("user config permissions = %o, want 600", info.Mode().Perm())
	}
	resolver := config.Resolver{
		UserPath: userPath,
		Git: fakeInspector{repository: ports.Repository{
			Root: root, Remote: "github.com/acme/atlas",
		}},
	}
	resolved, err := resolver.Resolve(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Application.Name != "atlas" || len(resolved.Application.Commands) != 1 {
		t.Fatalf("unexpected resolved app: %#v", resolved.Application)
	}
	if resolved.Trust != nil {
		t.Fatal("user-owned commands must not require repository trust")
	}
}
