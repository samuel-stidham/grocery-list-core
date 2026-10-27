package logging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	errNoFiles = errors.New("too many open files")
	errRename  = errors.New("rename refused")
)

// writeOK writes one record and fails the test on any error.
func writeOK(t *testing.T, rotating *RotatingFile, record string) {
	t.Helper()

	if _, err := rotating.Write([]byte(record)); err != nil {
		t.Fatalf("write %q: %v", record, err)
	}
}

// writeFails writes one record, expects the full count back together with
// want in the error chain.
func writeFails(t *testing.T, rotating *RotatingFile, record string, want error) {
	t.Helper()

	written, err := rotating.Write([]byte(record))
	if !errors.Is(err, want) || written != len(record) {
		t.Fatalf("write %q: want %v with a full write, got %d, %v", record, want, written, err)
	}
}

// dirHolds checks the exact set of file names in dir.
func dirHolds(t *testing.T, dir string, want ...string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dir holds %v, want %v", got, want)
	}
}

// fileIs checks the exact content of one file.
func fileIs(t *testing.T, path, want string) {
	t.Helper()

	got, _ := os.ReadFile(path)
	if string(got) != want {
		t.Fatalf("%s holds %q, want %q", filepath.Base(path), got, want)
	}
}

// chain opens a rotating file with keep 3 and fills the archives, so a test
// can prove they come through a failure untouched. It returns the file and
// the live record, which fills the file to 98 of the 100 byte limit, so any
// record of three bytes or more triggers a rotation.
func chain(t *testing.T, dir string) (*RotatingFile, string) {
	t.Helper()

	path := filepath.Join(dir, "fdc.log")

	rotating, err := OpenRotating(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}

	for n, content := range map[string]string{".1": "newest\n", ".2": "middle\n", ".3": "oldest\n"} {
		if err := os.WriteFile(path+n, []byte(content), logFileMode); err != nil {
			t.Fatal(err)
		}
	}

	live := strings.Repeat("l", 97) + "\n"
	writeOK(t, rotating, live)

	return rotating, live
}

// assertChainUntouched checks that the live file and every archive are what
// chain() made them, plus any records written since to the live file.
func assertChainUntouched(t *testing.T, dir, path, live string) {
	t.Helper()

	dirHolds(t, dir, "fdc.log", "fdc.log.1", "fdc.log.2", "fdc.log.3")
	fileIs(t, path, live)
	fileIs(t, path+".1", "newest\n")
	fileIs(t, path+".2", "middle\n")
	fileIs(t, path+".3", "oldest\n")
}

// TestFreshFileFailureMovesNothing covers EMFILE or ENOSPC while renames
// still work: the fresh file cannot be created, so rotation must stop before
// touching the chain, and the live file must stay where it is.
func TestFreshFileFailureMovesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")
	rotating, live := chain(t, dir)

	var alerts bytes.Buffer

	rotating.AlertTo(&alerts)

	realOpen := rotating.openFile
	rotating.openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errNoFiles }

	writeFails(t, rotating, "second\n", errNoFiles)
	writeFails(t, rotating, "third\n", errNoFiles)

	assertChainUntouched(t, dir, path, live+"second\nthird\n")

	if strings.Count(alerts.String(), "log rotation failed") != 1 {
		t.Fatalf("one alert per failure streak:\n%s", alerts.String())
	}

	rotating.openFile = realOpen

	writeOK(t, rotating, "fourth\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	dirHolds(t, dir, "fdc.log", "fdc.log.1", "fdc.log.2", "fdc.log.3")
	fileIs(t, path, "fourth\n")
	fileIs(t, path+".1", live+"second\nthird\n")
	fileIs(t, path+".2", "newest\n")
	fileIs(t, path+".3", "middle\n")

	if !strings.Contains(alerts.String(), "log rotation recovered") {
		t.Fatalf("recovery is reported:\n%s", alerts.String())
	}
}

// TestInstallFailureUndoesTheShift covers the last rename failing after the
// whole chain moved. Everything must come back, archives included, and the
// retry must start from the same state instead of eating one archive per
// attempt.
func TestInstallFailureUndoesTheShift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")
	rotating, live := chain(t, dir)

	realRename := rotating.rename
	rotating.rename = func(oldpath, newpath string) error {
		if oldpath == path+".tmp" {
			return errRename
		}

		return realRename(oldpath, newpath)
	}

	for _, record := range []string{"one\n", "two\n", "three\n", "four\n"} {
		writeFails(t, rotating, record, errRename)
	}

	assertChainUntouched(t, dir, path, live+"one\ntwo\nthree\nfour\n")

	rotating.rename = realRename

	writeOK(t, rotating, "fresh\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	dirHolds(t, dir, "fdc.log", "fdc.log.1", "fdc.log.2", "fdc.log.3")
	fileIs(t, path, "fresh\n")
	fileIs(t, path+".1", live+"one\ntwo\nthree\nfour\n")
	fileIs(t, path+".2", "newest\n")
	fileIs(t, path+".3", "middle\n")
}

// TestShiftFailureMidChainUndoesThePartialShift fails the rename of one
// archive after older ones already moved. The moved ones must move back.
func TestShiftFailureMidChainUndoesThePartialShift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")
	rotating, live := chain(t, dir)

	realRename := rotating.rename
	rotating.rename = func(oldpath, newpath string) error {
		if oldpath == path+".1" {
			return errRename
		}

		return realRename(oldpath, newpath)
	}

	writeFails(t, rotating, "blocked\n", errRename)
	assertChainUntouched(t, dir, path, live+"blocked\n")

	rotating.rename = realRename

	writeOK(t, rotating, "fresh\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	dirHolds(t, dir, "fdc.log", "fdc.log.1", "fdc.log.2", "fdc.log.3")
	fileIs(t, path, "fresh\n")
	fileIs(t, path+".1", live+"blocked\n")
	fileIs(t, path+".2", "newest\n")
	fileIs(t, path+".3", "middle\n")
}

// TestLiveRenameFailureUndoesTheShift fails the move of the current file to
// path.1 after every archive moved down.
func TestLiveRenameFailureUndoesTheShift(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")
	rotating, live := chain(t, dir)

	realRename := rotating.rename
	rotating.rename = func(oldpath, newpath string) error {
		if oldpath == path {
			return errRename
		}

		return realRename(oldpath, newpath)
	}

	writeFails(t, rotating, "blocked\n", errRename)

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	assertChainUntouched(t, dir, path, live+"blocked\n")
}

// TestLeftoversFromACrashAreCleanedUp plants the two files a crash mid
// rotation can leave, the temp file and the overflow archive, and checks the
// next rotation removes both.
func TestLeftoversFromACrashAreCleanedUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")
	rotating, _ := chain(t, dir)

	for _, leftover := range []string{path + ".tmp", path + ".4"} {
		if err := os.WriteFile(leftover, []byte("crash debris\n"), logFileMode); err != nil {
			t.Fatal(err)
		}
	}

	writeOK(t, rotating, "after crash\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	dirHolds(t, dir, "fdc.log", "fdc.log.1", "fdc.log.2", "fdc.log.3")
	fileIs(t, path, "after crash\n")
}

// TestFreshFileAppends covers an operator truncating the live log after a
// rotation. Without O_APPEND the next write would land at the old offset and
// leave a hole of NUL bytes.
func TestFreshFileAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := OpenRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}

	// 96 bytes, then an 8 byte record crosses the 100 byte limit and rotates.
	writeOK(t, rotating, strings.Repeat("a", 95)+"\n")
	writeOK(t, rotating, "rotated\n")
	writeOK(t, rotating, "more\n")
	dirHolds(t, dir, "fdc.log", "fdc.log.1")
	fileIs(t, path, "rotated\nmore\n")

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}

	writeOK(t, rotating, "after truncate\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	fileIs(t, path, "after truncate\n")
}

func TestAlertIsQuietWithoutAWriterAndStatefulWithOne(t *testing.T) {
	dir := t.TempDir()
	rotating, _ := chain(t, dir)

	rotating.openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errNoFiles }

	// No alert writer: failures are still returned, nothing else happens.
	writeFails(t, rotating, "quiet\n", errNoFiles)

	var alerts bytes.Buffer

	rotating.AlertTo(&alerts)
	writeFails(t, rotating, "loud\n", errNoFiles)
	writeFails(t, rotating, "still loud\n", errNoFiles)

	rotating.openFile = os.OpenFile
	writeOK(t, rotating, "fixed\n")
	writeOK(t, rotating, "fine\n")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(alerts.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "failed") || !strings.Contains(lines[1], "recovered") {
		t.Fatalf("want one failed line then one recovered line, got %q", lines)
	}

	if !strings.Contains(lines[0], filepath.Join(dir, "fdc.log")) || !strings.Contains(lines[0], errNoFiles.Error()) {
		t.Fatalf("the alert names the file and the cause: %q", lines[0])
	}
}

func TestKeepOneRotatesIntoASingleArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := OpenRotating(path, 20, 1)
	if err != nil {
		t.Fatal(err)
	}

	for _, record := range []string{"first record\n", "second record\n", "third record\n"} {
		writeOK(t, rotating, record)
	}

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	dirHolds(t, dir, "fdc.log", "fdc.log.1")
	fileIs(t, path, "third record\n")
	fileIs(t, path+".1", "second record\n")
}

func TestLimitBoundaryIsInclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := OpenRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}

	// 60 + 40 = 100 fits exactly. One more byte rotates.
	writeOK(t, rotating, strings.Repeat("a", 60))
	writeOK(t, rotating, strings.Repeat("b", 40))
	dirHolds(t, dir, "fdc.log")

	writeOK(t, rotating, "c")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	dirHolds(t, dir, "fdc.log", "fdc.log.1")
	fileIs(t, path, "c")
}

func TestEmptyRecordAndOversizedFirstRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdc.log")

	rotating, err := OpenRotating(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}

	written, err := rotating.Write(nil)
	if written != 0 || err != nil {
		t.Fatalf("empty write: %d, %v", written, err)
	}

	// The first record into an empty file is never split and never rotates,
	// whatever its size.
	writeOK(t, rotating, strings.Repeat("x", 50))
	dirHolds(t, dir, "fdc.log")

	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}

	fileIs(t, path, strings.Repeat("x", 50))
}
