package platform

import (
	"os"
	"path/filepath"
	"runtime"
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
	for _, line := range strings.Split(string(first), "\n") {
		if key, found := strings.CutPrefix(line, "ENCRYPTION_KEY="); found && len(key) != 32 {
			t.Fatalf("encryption key length = %d, want 32", len(key))
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
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

func TestLegacyEncryptionKeyIsRepairedWithoutReplacingOtherValues(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".env")
	legacy := "ENCRYPTION_KEY=" + strings.Repeat("a", 64) + "\nPOSTGRES_PASSWORD=keep-me\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := Manager{DataDir: root}
	if err := manager.ensureEnvironment(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "POSTGRES_PASSWORD=keep-me") {
		t.Fatalf("repair replaced unrelated values: %s", content)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if key, found := strings.CutPrefix(line, "ENCRYPTION_KEY="); found && len(key) != 32 {
			t.Fatalf("repaired encryption key length = %d, want 32", len(key))
		}
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
