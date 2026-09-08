package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andocodes/cassie/internal/adapters/system"
)

func TestDoctorSuggestsFixForMissingManagedTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executables")
	}
	a, output := doctorTestApp(t, false)

	err := a.doctor(context.Background(), false)
	var exit exitError
	if !errors.As(err, &exit) {
		t.Fatalf("doctor error = %v, want exit error", err)
	}
	for _, want := range []string{
		"Portless         not installed",
		"Infisical CLI    not installed",
		"Run cassie doctor --fix",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("doctor output does not contain %q:\n%s", want, output.String())
		}
	}
}

func TestDoctorFixInstallsAndRechecksManagedTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executables")
	}
	a, output := doctorTestApp(t, true)

	if err := a.doctor(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"portless", "infisical"} {
		if !regularFile(a.platform().Binary(name)) {
			t.Fatalf("managed %s was not installed", name)
		}
	}
	for _, want := range []string{
		"Installing pinned Portless and Infisical CLI versions",
		"portless test",
		"infisical test",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("doctor output does not contain %q:\n%s", want, output.String())
		}
	}
}

func TestDoctorWarnsForDifferentToolVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executables")
	}
	a, output := doctorTestApp(t, false)
	tools := filepath.Dir(a.platform().Binary("portless"))
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, tools, "portless", "#!/bin/sh\nprintf '0.16.0\\n'\n")
	writeExecutable(t, tools, "infisical", "#!/bin/sh\nprintf 'infisical version 0.42.0\\n'\n")

	if err := a.doctor(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"!  Portless         0.16.0 (Cassie pins 0.15.6)",
		"!  Infisical CLI    0.42.0 (Cassie pins 0.43.129)",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("doctor output does not contain %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "doctor --fix") {
		t.Fatalf("version warning offered repair:\n%s", output.String())
	}
}

func TestDockerDetailShowsEngineAndActiveContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	bin := t.TempDir()
	writeExecutable(t, bin, "docker", "#!/bin/sh\nprintf 'orbstack\\n'\n")
	t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	detail := dockerDetail(context.Background(), []byte(`{"Name":"orbstack","OperatingSystem":"OrbStack","ServerVersion":"29.4.0"}`))
	if detail != "OrbStack · Engine 29.4.0 · context orbstack" {
		t.Fatalf("Docker detail = %q", detail)
	}
}

func doctorTestApp(t *testing.T, withNPM bool) (*app, *bytes.Buffer) {
	t.Helper()
	bin := t.TempDir()
	writeExecutable(t, bin, "docker", "#!/bin/sh\nprintf 'docker test\\n'\n")
	writeExecutable(t, bin, "node", "#!/bin/sh\nprintf 'v24.0.0\\n'\n")
	if withNPM {
		writeExecutable(t, bin, "npm", `#!/bin/sh
set -eu
prefix="$3"
/bin/mkdir -p "$prefix/node_modules/.bin"
printf '#!/bin/sh\nprintf "portless test\\n"\n' > "$prefix/node_modules/.bin/portless"
printf '#!/bin/sh\nprintf "infisical test\\n"\n' > "$prefix/node_modules/.bin/infisical"
/bin/chmod 0755 "$prefix/node_modules/.bin/portless" "$prefix/node_modules/.bin/infisical"
`)
	}
	t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	root := t.TempDir()
	output := &bytes.Buffer{}
	return &app{
		paths: system.Paths{
			Config: filepath.Join(root, "config.yaml"),
			Data:   filepath.Join(root, "data"),
			State:  filepath.Join(root, "state"),
		},
		stdin:  strings.NewReader(""),
		stdout: output,
		stderr: output,
	}, output
}

func writeExecutable(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
