package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

func TestDiscoverRepositoriesIsBoundedAndIgnoresConfiguredPaths(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "api", ".git"),
		filepath.Join(root, "nested", "web", ".git"),
		filepath.Join(root, "nested", "too", "deep", ".git"),
		filepath.Join(root, "archive", "old", ".git"),
		filepath.Join(root, "node_modules", "dependency", ".git"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	repositories, err := discoverRepositories(root, Workspace{Depth: 2, Ignore: []string{"archive"}})
	if err != nil {
		t.Fatal(err)
	}
	for index := range repositories {
		repositories[index], _ = filepath.Rel(root, repositories[index])
	}
	sort.Strings(repositories)
	want := []string{"api", filepath.Join("nested", "web")}
	if !reflect.DeepEqual(repositories, want) {
		t.Fatalf("repositories = %#v, want %#v", repositories, want)
	}
}

func TestDiscoverRepositoriesSkipsUnreadableDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, ".Trash")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("directory permissions are not enforced")
	}
	repository := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	repositories, err := discoverRepositories(root, Workspace{Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{repository}
	if !reflect.DeepEqual(repositories, want) {
		t.Fatalf("repositories = %#v, want %#v", repositories, want)
	}
}
