package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/andocodes/cassie/internal/adapters/config"
	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/ui/cli"
)

func TestRunFromUserConfigWithoutRepositoryFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake Portless executable")
	}
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	dataDir := filepath.Join(t.TempDir(), "data")
	stateDir := filepath.Join(t.TempDir(), "state")
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	portless := filepath.Join(binDir, "portless")
	if err := os.WriteFile(portless, []byte("#!/bin/sh\nif [ \"$1\" = \"--name\" ]; then shift 2; fi\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CASSIE_CONFIG", configPath)
	t.Setenv("CASSIE_DATA", dataDir)
	t.Setenv("CASSIE_STATE", stateDir)

	writer := config.Writer{UserPath: configPath}
	if err := writer.SaveUser("smoke", config.Match{Path: root}, catalog.Application{
		Name: "smoke", Domain: "smoke",
		Commands: []catalog.Command{{Run: "printf ready > ran.txt"}},
	}); err != nil {
		t.Fatal(err)
	}
	command, err := cli.New("test")
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--dir", root, "run"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "ran.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "ready" {
		t.Fatalf("output = %q, want ready", content)
	}
}
