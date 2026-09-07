package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupArchiveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "database.sql")
	if err := os.WriteFile(database, []byte("select 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "backup.tar.gz")
	err := createArchive(archive, map[string]archiveSource{
		"manifest.json": {Content: []byte(`{"version":1}`)},
		"platform.env":  {Content: []byte("AUTH_SECRET=value\n")},
		"database.sql":  {Path: database},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(archive)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("archive permissions = %v, err=%v", info.Mode().Perm(), err)
	}
	restored := filepath.Join(dir, "restored")
	if err := os.Mkdir(restored, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(archive, restored); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(restored, "database.sql"))
	if err != nil || string(content) != "select 1;\n" {
		t.Fatalf("restored database = %q, err=%v", content, err)
	}
}
