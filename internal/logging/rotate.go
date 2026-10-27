package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Rotation defaults. Ten megabytes and five kept files cap a runaway log at
// sixty megabytes. Both are flags on the serve command.
const (
	DefaultMaxBytes int64 = 10 << 20
	DefaultKeep           = 5
)

// Permissions for the log directory and files. The log holds request paths
// and error text, which nobody else on the machine needs to read.
const (
	logDirMode  = 0o750
	logFileMode = 0o600
)

// ErrRotationSettings means maxBytes or keep was zero or negative.
var ErrRotationSettings = errors.New("log rotation needs a positive size and keep count")

// RotatingFile is an io.Writer that rotates a log file by size. The current
// file is path, the newest rotated file is path.1, and the oldest is
// path.<keep>. A write that would cross maxBytes rotates first, so no file
// grows past the limit by more than one record.
type RotatingFile struct {
	path     string
	maxBytes int64
	keep     int
	// openFile and rename are os.OpenFile and os.Rename, swapped by tests to
	// fail one step of a rotation and prove the others undo themselves.
	openFile func(name string, flag int, perm os.FileMode) (*os.File, error)
	rename   func(oldpath, newpath string) error
	// alert receives one line when a rotation failure streak starts and one
	// when it ends, because slog drops the errors Write returns. Nil is quiet.
	alert io.Writer

	mu      sync.Mutex
	file    *os.File
	size    int64
	failing bool
}

// OpenRotating opens or creates path for appending, creating its directory.
func OpenRotating(path string, maxBytes int64, keep int) (*RotatingFile, error) {
	if maxBytes <= 0 || keep <= 0 {
		return nil, fmt.Errorf("%w: got %d bytes, keep %d", ErrRotationSettings, maxBytes, keep)
	}

	if err := os.MkdirAll(filepath.Dir(path), logDirMode); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	rotating := &RotatingFile{path: path, maxBytes: maxBytes, keep: keep, openFile: os.OpenFile, rename: os.Rename}
	if err := rotating.open(); err != nil {
		return nil, err
	}

	return rotating, nil
}

// AlertTo names where rotation failures are reported in one line. The daemon
// passes stderr, which the service manager keeps, so an operator can see why
// the log stopped rotating.
func (r *RotatingFile) AlertTo(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.alert = w
}

func (r *RotatingFile) open() error {
	file, err := r.openFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, logFileMode)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()

		return fmt.Errorf("stat log file: %w", err)
	}

	r.file, r.size = file, info.Size()

	return nil
}

// Write appends one record, rotating first when it would cross the limit. A
// single record larger than the limit still gets written whole, into a fresh
// file, because truncating a log line is worse than one oversized file. When
// rotation fails the record still goes to the current file, so a bad
// rotation never costs a log line, and the rotation error is returned
// alongside the full count.
func (r *RotatingFile) Write(record []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return 0, fmt.Errorf("write log: %w", os.ErrClosed)
	}

	var rotateErr error
	if r.size > 0 && r.size+int64(len(record)) > r.maxBytes {
		rotateErr = r.rotate()
		r.report(rotateErr)
	}

	written, err := r.file.Write(record)
	r.size += int64(written)

	if err != nil {
		return written, errors.Join(rotateErr, fmt.Errorf("write log: %w", err))
	}

	return written, rotateErr
}

// report writes one alert line when a failure streak starts and one when it
// ends. Every record in between still reaches the current file.
func (r *RotatingFile) report(rotateErr error) {
	if r.alert == nil || (rotateErr != nil) == r.failing {
		return
	}

	r.failing = rotateErr != nil

	if r.failing {
		_, _ = fmt.Fprintf(r.alert, "fdc: log rotation failed, records stay in %s until it recovers: %v\n", r.path, rotateErr)

		return
	}

	_, _ = fmt.Fprintf(r.alert, "fdc: log rotation recovered\n")
}

// rotate creates the fresh file first under a temp name, so a failure to
// create it moves nothing. It then shifts the kept files, moves the current
// file to path.1, installs the fresh file, and only then closes the old
// handle and drops the oldest archive. On any failure the shift is undone,
// so the live file and every archive are back where they were, writes keep
// going to the old handle, and a retry starts from the same state.
func (r *RotatingFile) rotate() error {
	tmp := r.path + ".tmp"

	fresh, err := r.openFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_APPEND|os.O_WRONLY, logFileMode)
	if err != nil {
		return fmt.Errorf("create fresh log: %w", err)
	}

	if err := r.shift(); err != nil {
		discard(fresh, tmp)

		return err
	}

	if err := r.rename(tmp, r.path); err != nil {
		discard(fresh, tmp)

		_ = r.rename(r.rotated(1), r.path)
		r.unshift(1)

		return fmt.Errorf("install fresh log: %w", err)
	}

	previous := r.file

	r.file, r.size = fresh, 0

	_ = previous.Close()
	_ = os.Remove(r.overflow())

	return nil
}

// shift moves every kept file one slot down and the current file to path.1.
// The oldest archive goes to the overflow slot path.<keep+1> rather than
// being deleted, so a failed install can put everything back. The current
// file moves last, so a failure earlier in the chain leaves it where it is.
// A current file that someone deleted is not an error: there is nothing to
// keep. A leftover overflow file from a crash mid rotation is already due
// for deletion and goes first.
func (r *RotatingFile) shift() error {
	_ = os.Remove(r.overflow())

	for n := r.keep; n >= 1; n-- {
		if err := r.rename(r.rotated(n), r.rotated(n+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			r.unshift(n + 1)

			return fmt.Errorf("shift rotated log %d: %w", n, err)
		}
	}

	if err := r.rename(r.path, r.rotated(1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		r.unshift(1)

		return fmt.Errorf("rotate log: %w", err)
	}

	return nil
}

// unshift moves path.<m+1> back to path.<m> for every m between from and keep.
// Each target slot was emptied by the shift, so the moves cannot collide.
// Errors are ignored, because this already runs on a failure path and the
// next rotation retries from whatever state is left.
func (r *RotatingFile) unshift(from int) {
	for m := from; m <= r.keep; m++ {
		_ = r.rename(r.rotated(m+1), r.rotated(m))
	}
}

// overflow is the slot the oldest archive waits in during a rotation.
func (r *RotatingFile) overflow() string {
	return r.rotated(r.keep + 1)
}

// discard closes and removes a fresh file that will not be installed.
func discard(fresh *os.File, name string) {
	_ = fresh.Close()
	_ = os.Remove(name)
}

func (r *RotatingFile) rotated(n int) string {
	return r.path + "." + strconv.Itoa(n)
}

// Close closes the current file. Later writes fail.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return nil
	}

	err := r.file.Close()
	r.file = nil

	if err != nil {
		return fmt.Errorf("close log: %w", err)
	}

	return nil
}

var _ io.WriteCloser = (*RotatingFile)(nil)
