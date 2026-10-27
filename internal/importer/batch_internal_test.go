package importer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

func TestBatchWriterCommitsEverything(t *testing.T) {
	ctx := context.Background()

	database, err := store.OpenForImport(ctx, filepath.Join(t.TempDir(), "b.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = database.Close() }()

	if _, err := database.ExecContext(ctx, `CREATE TABLE t(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	writer := newBatchWriter(database, `INSERT INTO t(id) VALUES (?)`, 3)
	for rowID := 1; rowID <= 10; rowID++ {
		if err := writer.Write(ctx, rowID); err != nil {
			t.Fatal(err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 10 {
		t.Fatalf("rows = %d, want 10 across four batches", count)
	}
}
