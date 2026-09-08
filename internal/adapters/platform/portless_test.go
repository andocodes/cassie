package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andocodes/cassie/internal/domain/catalog"
)

func TestWrapPreservesCompoundShellCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command shape")
	}
	wrapped := (Portless{Binary: "/tools/portless"}).Wrap(
		catalog.Application{Domain: "phoebe"},
		catalog.Command{Run: "prepare && while true; do serve; done", Dir: "web"},
	)
	for _, want := range []string{"'/tools/portless' --name 'phoebe' --", " -lc 'prepare && while true; do serve; done'"} {
		if !strings.Contains(wrapped.Run, want) {
			t.Fatalf("wrapped command %q does not contain %q", wrapped.Run, want)
		}
	}
	if wrapped.Dir != "web" {
		t.Fatalf("wrapped command dir = %q", wrapped.Dir)
	}
}

func TestReadyReportsStoppedProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	binary := filepath.Join(t.TempDir(), "portless")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'warn  Proxy is not running on port 443.\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ready, err := (Portless{Binary: binary}).Ready(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("stopped proxy reported ready")
	}
}

func TestInstallServiceUsesCassieStateDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "portless")
	arguments := filepath.Join(root, "arguments")
	state := filepath.Join(root, "state")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CASSIE_ARGUMENTS\"\n"
	if err := os.WriteFile(binary, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	portless := Portless{
		Binary: binary,
		Env: map[string]string{
			"CASSIE_ARGUMENTS":   arguments,
			"PORTLESS_STATE_DIR": state,
		},
	}
	if err := portless.InstallService(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	want := "service\ninstall\n--state-dir\n" + state + "\n"
	if string(content) != want {
		t.Fatalf("arguments = %q, want %q", content, want)
	}
}

func TestWrapRunsCompoundCommandThroughAShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	binary := filepath.Join(t.TempDir(), "portless")
	shim := "#!/bin/sh\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\nexec \"$@\"\n"
	if err := os.WriteFile(binary, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapped := (Portless{Binary: binary}).Wrap(
		catalog.Application{Domain: "phoebe"},
		catalog.Command{Run: "printf first && printf second"},
	)
	output, err := exec.Command("/bin/sh", "-lc", wrapped.Run).CombinedOutput()
	if err != nil {
		t.Fatalf("run wrapped command: %v: %s", err, output)
	}
	if string(output) != "firstsecond" {
		t.Fatalf("wrapped output = %q", output)
	}
}
