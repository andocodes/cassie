package process_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/andocodes/cassie/internal/adapters/process"
	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/ports"
)

func TestRunnerUsesCommandDirectoryAndEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell assertion")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := (process.Runner{}).Run(context.Background(), ports.Process{
		Root:    root,
		Command: catalog.Command{Run: `printf '%s' "$CASSIE_TEST_VALUE" > result.txt`, Dir: "web"},
		Env:     map[string]string{"CASSIE_TEST_VALUE": "ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "ready" {
		t.Fatalf("result = %q, want ready", content)
	}
}
