package config

import (
	"os"
	"path/filepath"
	"reflect"
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
