package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentIsCreatedOnceWithPrivatePermissions(t *testing.T) {
	manager := Manager{DataDir: t.TempDir()}
	if err := manager.ensureEnvironment(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manager.DataDir, ".env")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "SITE_URL=https://infisical.localhost") {
		t.Fatalf("unexpected environment: %s", first)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("environment permissions = %o, want 600", info.Mode().Perm())
	}
	if err := manager.ensureEnvironment(); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("existing platform secrets were replaced")
	}
}

func TestComposePinsInfisicalAndBindsToLoopback(t *testing.T) {
	if !strings.Contains(composeFile, "infisical/infisical:"+ServerVersion) {
		t.Fatal("Infisical image is not pinned")
	}
	if !strings.Contains(composeFile, `127.0.0.1:4080:8080`) {
		t.Fatal("Infisical must only bind to loopback")
	}
}
