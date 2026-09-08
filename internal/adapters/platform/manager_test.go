package platform

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnvironmentIsCreatedOnceWithPrivatePermissions(t *testing.T) {
	manager := Manager{DataDir: t.TempDir()}
	if err := manager.ensureEnvironment(DefaultProxyPort); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manager.DataDir, ".env")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "SITE_URL=https://infisical.localhost:1355") {
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
	if err := manager.ensureEnvironment(DefaultProxyPort); err != nil {
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
	if err := manager.ensureEnvironment(DefaultProxyPort); err != nil {
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

func TestEnvironmentUpdatesInfisicalURLWithoutReplacingSecrets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".env")
	existing := "ENCRYPTION_KEY=0123456789abcdef0123456789abcdef\nPOSTGRES_PASSWORD=keep-me\nSITE_URL=https://infisical.localhost\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := Manager{DataDir: root}
	if err := manager.ensureEnvironment(1372); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "POSTGRES_PASSWORD=keep-me") {
		t.Fatalf("environment replaced an existing secret: %s", content)
	}
	if !strings.Contains(string(content), "SITE_URL=https://infisical.localhost:1372") {
		t.Fatalf("environment did not update SITE_URL: %s", content)
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

func TestBootstrapCreatesAndStoresAUserSessionWithoutBrowser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	tools := t.TempDir()
	bin := filepath.Join(tools, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	script := `#!/bin/sh
case "$1" in
  bootstrap)
    [ "$INFISICAL_ADMIN_EMAIL" = "admin@example.com" ] || exit 11
    [ "$INFISICAL_ADMIN_PASSWORD" = "correct horse battery" ] || exit 12
    [ "$INFISICAL_ADMIN_ORGANIZATION" = "Cassie" ] || exit 13
    printf '{"organization":{"id":"org-123"},"identity":{"credentials":{"token":"do-not-persist"}}}'
    ;;
  login)
    [ "$INFISICAL_EMAIL" = "admin@example.com" ] || exit 21
    [ "$INFISICAL_PASSWORD" = "correct horse battery" ] || exit 22
    [ "$INFISICAL_ORGANIZATION_ID" = "org-123" ] || exit 23
    printf '%s\n' "$*" > "$CASSIE_CALLS"
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "infisical"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := Manager{
		Tools:  tools,
		Stderr: io.Discard,
		Env:    map[string]string{"CASSIE_CALLS": calls},
	}
	credentials := BootstrapCredentials{
		Email:        "admin@example.com",
		Password:     "correct horse battery",
		Organization: "Cassie",
	}
	if err := manager.Bootstrap(context.Background(), credentials); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"login", "--method=user", "--domain=" + manager.InfisicalURL(), "--silent"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("login command %q does not contain %q", content, want)
		}
	}
	if strings.Contains(string(content), "do-not-persist") {
		t.Fatal("bootstrap identity token was passed to the login command")
	}
}

func TestLoginUsesTerminalFlow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	tools := t.TempDir()
	bin := filepath.Join(tools, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$CASSIE_CALLS\"\n"
	if err := os.WriteFile(filepath.Join(bin, "infisical"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Tools: tools, Env: map[string]string{"CASSIE_CALLS": calls}}
	if err := manager.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "login --interactive --domain=" + manager.InfisicalURL() + "\n"
	if string(content) != want {
		t.Fatalf("login command = %q, want %q", content, want)
	}
}
