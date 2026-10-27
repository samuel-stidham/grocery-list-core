// Package store owns the SQLite schema and every query the API runs.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver for database/sql
	sqlite3 "modernc.org/sqlite/lib"
)

// LibraryVersion is the SQLite engine version linked into the binary, which
// is what fdc version reports beside the Go module version of the driver.
func LibraryVersion() string {
	return sqlite3.SQLITE_VERSION
}

// Page cache for the bulk load, in KiB with the negative sign SQLite uses.
const importCacheSizeKiB = 262144

// maxOpenConns bounds the serving pool. Every connection maps the whole
// database file, and four is plenty for one frontend on loopback. All four
// are opened at startup and never recycled, so they all hold the same inode.
// An import that renames a new file over the served one then changes
// nothing for the running server, which keeps serving the release it opened
// until it is restarted. Without this, connections opened after the rename
// would read the new file and one response could mix two releases.
const maxOpenConns = 4

// DSN builds the SQLite URI for a file path and a query string. The three
// characters that would end or split a URI early are escaped, so a database in
// a directory with a question mark or a hash in its name still opens.
func DSN(path, query string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	if query == "" {
		return "file:" + escaped
	}

	return "file:" + escaped + "?" + query
}

// OpenReadOnly opens an existing database for serving. mode=ro refuses writes
// and immutable=1 skips locking, which is correct because the file is replaced
// atomically by the importer and never modified in place. A missing or
// unreadable file comes back wrapped in ErrOpen.
func OpenReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOpen, path, err)
	}

	database, err := sql.Open("sqlite", DSN(path, "mode=ro&immutable=1"))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOpen, path, err)
	}

	database.SetMaxOpenConns(maxOpenConns)
	database.SetMaxIdleConns(maxOpenConns)
	database.SetConnMaxLifetime(0)
	database.SetConnMaxIdleTime(0)

	if err := warmPool(ctx, database); err != nil {
		_ = database.Close()

		return nil, fmt.Errorf("%w: %s: %w", ErrOpen, path, err)
	}

	return database, nil
}

// warmPool opens every connection the pool will ever use and hands them
// back idle. Holding all of them at once is what forces the pool to open
// maxOpenConns distinct connections rather than reuse one.
func warmPool(ctx context.Context, database *sql.DB) error {
	held := make([]*sql.Conn, 0, maxOpenConns)

	defer func() {
		for _, conn := range held {
			_ = conn.Close()
		}
	}()

	for range maxOpenConns {
		conn, err := database.Conn(ctx)
		if err != nil {
			return fmt.Errorf("open connection: %w", err)
		}

		held = append(held, conn)

		if err := conn.PingContext(ctx); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
	}

	return nil
}

// OpenForImport creates or opens a database for the bulk load. WAL with
// synchronous=OFF is only safe because the importer writes to a temp file and
// a crash loses nothing that cannot be rebuilt by rerunning the import.
func OpenForImport(ctx context.Context, path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", DSN(path, ""))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	// The import is single threaded. One connection keeps every PRAGMA on the
	// connection that does the writing.
	database.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=OFF",
		"PRAGMA temp_store=MEMORY",
		fmt.Sprintf("PRAGMA cache_size=-%d", importCacheSizeKiB),
		"PRAGMA foreign_keys=OFF",
	}
	for _, pragma := range pragmas {
		if _, err := database.ExecContext(ctx, pragma); err != nil {
			_ = database.Close()

			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	return database, nil
}

// Finalize switches a freshly imported database to its serving state: durable
// writes, a single file with no WAL sidecars, and fresh planner statistics.
func Finalize(ctx context.Context, database *sql.DB) error {
	steps := []string{
		"ANALYZE",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA wal_checkpoint(TRUNCATE)",
		"PRAGMA journal_mode=DELETE",
	}
	for _, step := range steps {
		if _, err := database.ExecContext(ctx, step); err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
	}

	return nil
}
