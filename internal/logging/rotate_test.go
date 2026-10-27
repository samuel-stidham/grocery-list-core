package logging_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/logging"
)

const (
	limit    = 100
	keepTwo  = 2
	line40   = "0123456789012345678901234567890123456789"
	lineMode = 0o600
)

func fileNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

func TestOpenRotatingCreatesDirectoryAndFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "deeper", "fdc.log")

	rotating, err := logging.OpenRotating(path, limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rotating.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != lineMode {
		t.Fatalf("log file mode %o, want %o", info.Mode().Perm(), lineMode)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if dirInfo.Mode().Perm() != 0o750 {
		t.Fatalf("log dir mode %o, want 750", dirInfo.Mode().Perm())
	}
}

func TestRotatesAtLimitAndKeepsCount(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := logging.OpenRotating(path, limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	// Each write is 41 bytes. Two fit under 100, the third rotates, and so on.
	// Eight writes produce three rotations, so the oldest file must be gone.
	for i := range 8 {
		record := line40 + string(rune('a'+i)) + "\n"
		if _, err := rotating.Write([]byte(record[:41])); err != nil {
			t.Fatal(err)
		}
	}

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	assertRotatedSet(t, dir)

	// The newest rotated file holds the most recent full batch, so its
	// records come after those in the oldest one.
	newest, _ := os.ReadFile(filepath.Join(dir, "fdc.log.1"))
	oldest, _ := os.ReadFile(filepath.Join(dir, "fdc.log.2"))

	if !strings.Contains(string(newest), "e") || !strings.Contains(string(oldest), "c") {
		t.Fatalf("rotation order wrong:\n.1=%q\n.2=%q", newest, oldest)
	}
}

// assertRotatedSet checks that exactly the current file and two rotated
// files exist, each under the size limit.
func assertRotatedSet(t *testing.T, dir string) {
	t.Helper()

	names := fileNames(t, dir)
	if len(names) != 3 {
		t.Fatalf("want fdc.log, fdc.log.1, fdc.log.2 only, got %v", names)
	}

	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if info.Size() > limit {
			t.Fatalf("%s is %d bytes, over the %d limit", name, info.Size(), limit)
		}
	}
}

func TestOversizedRecordIsWrittenWhole(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := logging.OpenRotating(path, limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("x", 250) + "\n"

	if _, err := rotating.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}

	if _, err := rotating.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	current, _ := os.ReadFile(path)
	if string(current) != big {
		t.Fatalf("oversized record must land whole in a fresh file, got %d bytes", len(current))
	}

	previous, _ := os.ReadFile(path + ".1")
	if string(previous) != "first\n" {
		t.Fatalf("previous file wrong: %q", previous)
	}
}

func TestReopenContinuesExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fdc.log")

	first, err := logging.OpenRotating(path, limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := first.Write([]byte(line40 + "\n")); err != nil {
		t.Fatal(err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := logging.OpenRotating(path, limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	// 41 + 41 = 82 fits. The third write (123) rotates, so the size counter
	// must have been picked up from the existing file.
	for range 2 {
		if _, err := second.Write([]byte(line40 + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("size from the existing file was not counted: %v", err)
	}
}

func TestConcurrentWritesAreSafe(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fdc.log")

	rotating, err := logging.OpenRotating(path, 1000, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			for range 20 {
				if _, err := rotating.Write([]byte(line40 + "\n")); err != nil {
					t.Error(err)
				}
			}
		})
	}

	group.Wait()

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	t.Parallel()

	rotating, err := logging.OpenRotating(filepath.Join(t.TempDir(), "fdc.log"), limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := rotating.Write([]byte("late\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}

	if err := rotating.Close(); err != nil {
		t.Fatalf("second close must be a no-op: %v", err)
	}
}

func TestRejectsBadSettings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fdc.log")

	for _, settings := range []struct {
		max  int64
		keep int
	}{{0, 1}, {10, 0}, {-1, 1}} {
		_, err := logging.OpenRotating(path, settings.max, settings.keep)
		if !errors.Is(err, logging.ErrRotationSettings) {
			t.Fatalf("%+v: %v", settings, err)
		}
	}
}

// mustWrite writes one record and fails the test on any error.
func mustWrite(t *testing.T, rotating *logging.RotatingFile, record string) {
	t.Helper()

	if _, err := rotating.Write([]byte(record)); err != nil {
		t.Fatalf("write %q: %v", record, err)
	}
}

func TestDeletedFileIsRecreatedOnRotation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := logging.OpenRotating(path, limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, rotating, line40+"\n")

	// An operator frees space by deleting the live log. The next write lands
	// in the unlinked inode. The one after crosses the limit (82 + 41 bytes)
	// and rotation must recover.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, rotating, line40+"\n")

	after := "after delete " + strings.Repeat("-", 27) + "\n"
	mustWrite(t, rotating, after)
	mustWrite(t, rotating, "still logging\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("a fresh log file must exist: %v", err)
	}

	if string(content) != after+"still logging\n" {
		t.Fatalf("fresh file content %q", content)
	}

	if names := fileNames(t, dir); len(names) != 1 {
		t.Fatalf("nothing to rotate when the file was deleted, got %v", names)
	}
}

// blockRotation makes the shift of fdc.log.2 fail by putting a non-empty
// directory in the overflow slot fdc.log.3. Remove cannot delete it and
// Rename cannot replace it, so rotation fails without touching permissions.
func blockRotation(t *testing.T, path string) {
	t.Helper()

	for _, name := range []string{path + ".1", path + ".2"} {
		if err := os.WriteFile(name, []byte("older\n"), lineMode); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(filepath.Join(path+".3", "inner"), 0o750); err != nil {
		t.Fatal(err)
	}
}

func TestFailedRotationKeepsWritingToTheOldFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := logging.OpenRotating(path, limit, keepTwo)
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, rotating, line40+"\n")
	mustWrite(t, rotating, line40+"\n")
	blockRotation(t, path)

	// 82 + 41 bytes crosses the limit, so this write rotates, and fails to.
	kept := "kept anyway " + strings.Repeat("-", 28) + "\n"

	written, err := rotating.Write([]byte(kept))
	if err == nil || !strings.Contains(err.Error(), "shift rotated log") {
		t.Fatalf("the rotation failure must be reported: %v", err)
	}

	if written != len(kept) {
		t.Fatalf("the record must still be written in full, got %d bytes", written)
	}

	if _, err := rotating.Write([]byte("and this\n")); err == nil {
		t.Fatal("rotation is retried and still fails, so the error must repeat")
	}

	// Clear the blocker and the next write rotates for real.
	if err := os.RemoveAll(path + ".3"); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, rotating, "recovered\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	assertRotatedHolds(t, path, []string{"kept anyway", "and this"}, "recovered\n")
}

// assertRotatedHolds checks that fdc.log.1 holds every wanted record and
// that the current file holds exactly current.
func assertRotatedHolds(t *testing.T, path string, wanted []string, current string) {
	t.Helper()

	rotated, _ := os.ReadFile(path + ".1")
	for _, want := range wanted {
		if !strings.Contains(string(rotated), want) {
			t.Errorf("rotated file lacks %q:\n%s", want, rotated)
		}
	}

	got, _ := os.ReadFile(path)
	if string(got) != current {
		t.Errorf("fresh file content %q, want %q", got, current)
	}
}
