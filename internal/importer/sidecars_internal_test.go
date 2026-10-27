package importer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveSidecarsLeavesTheDatabase(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "fdc.sqlite")

	for _, name := range []string{"fdc.sqlite", "fdc.sqlite-wal", "fdc.sqlite-shm", "fdc.sqlite-journal"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removeSidecars(database)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 || entries[0].Name() != "fdc.sqlite" {
		t.Fatalf("want only the database left, got %v", entries)
	}

	removeDB(database)

	if _, err := os.Stat(database); !os.IsNotExist(err) {
		t.Fatalf("removeDB must delete the database too: %v", err)
	}
}
