package compose

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andocodes/cassie/internal/domain/catalog"
)

func TestPrepareCreatesPrivateComposeOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  web:\n    image: example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	prepared, err := (Environment{RuntimeDir: runtimeDir}).Prepare(context.Background(), "session-1", catalog.Application{
		Root: root,
		Compose: catalog.Compose{
			Services: []string{"web", "worker"},
		},
	}, map[string]string{"TOKEN": "a secret", "MULTILINE": "one\ntwo"})
	if err != nil {
		t.Fatal(err)
	}
	separator := ":"
	if runtime.GOOS == "windows" {
		separator = ";"
	}
	files := strings.Split(prepared.Values["COMPOSE_FILE"], separator)
	if len(files) != 2 || files[0] != filepath.Join(root, "compose.yaml") {
		t.Fatalf("unexpected COMPOSE_FILE: %q", prepared.Values["COMPOSE_FILE"])
	}
	override, err := os.ReadFile(files[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(override), "web:") || !strings.Contains(string(override), "worker:") {
		t.Fatalf("override does not target selected services: %s", override)
	}
	envPath := filepath.Join(runtimeDir, "session-1", "secrets.env")
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file permissions = %o, want 600", info.Mode().Perm())
	}
	if err := prepared.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "session-1")); !os.IsNotExist(err) {
		t.Fatalf("runtime directory still exists: %v", err)
	}
}
