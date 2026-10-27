package main

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/samuel-stidham/grocery-list-core/internal/daemon"
	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/importer"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// Exit codes. Two is what the flag package uses for usage errors.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// userHint decorates well-known sentinels with what to do next. This is the
// command boundary where an internal error becomes user guidance. Errors it
// does not know pass through unchanged.
func userHint(err error) error {
	if err == nil {
		return nil
	}

	if hinted := importHint(err); hinted != nil {
		return hinted
	}

	if hinted := serveHint(err); hinted != nil {
		return hinted
	}

	if hinted := daemonHint(err); hinted != nil {
		return hinted
	}

	return err
}

// importHint covers the importer and type sentinels, or returns nil when err
// is none of them.
func importHint(err error) error {
	switch {
	case errors.Is(err, importer.ErrMissingFile):
		return fmt.Errorf("%w: run `make fetch` to download and extract the pinned release", err)
	case errors.Is(err, importer.ErrHeaderMismatch):
		return fmt.Errorf(
			"%w: this archive is a different release, update data/fetch.sh and "+
				"internal/importer/headers.go together after checking the field descriptions",
			err,
		)
	case errors.Is(err, importer.ErrBadValue):
		return fmt.Errorf("%w: USDA changed how a column is written, inspect the row and decide before papering over it", err)
	case errors.Is(err, importer.ErrSmokeTestFailed):
		return fmt.Errorf(
			"%w: the output was discarded, the source files are not the release this importer was built for", err)
	case errors.Is(err, datatypes.ErrUnknownType), errors.Is(err, datatypes.ErrNoTypes):
		return fmt.Errorf("%w: -types takes a comma separated list of sr_legacy, foundation, branded, survey", err)
	default:
		return nil
	}
}

// serveHint covers the serving sentinels and the one syscall error an
// operator hits on a second start.
func serveHint(err error) error {
	switch {
	case errors.Is(err, store.ErrOpen):
		return fmt.Errorf("%w: run `make import` first, or pass -db with the path of a built database", err)
	case errors.Is(err, syscall.EADDRINUSE):
		return fmt.Errorf("%w: another fdc is probably running, check `systemctl --user status fdc` or pass -listen", err)
	default:
		return nil
	}
}

// daemonHint covers the install and uninstall sentinels.
func daemonHint(err error) error {
	switch {
	case errors.Is(err, daemon.ErrUnsupportedPlatform):
		return fmt.Errorf("%w: install writes a systemd user unit on Linux or a launchd agent on macOS, nothing else", err)
	case errors.Is(err, daemon.ErrBinMissing):
		return fmt.Errorf("%w: run `make install`, which copies the binary to ~/.local/bin first, or pass -bin", err)
	case errors.Is(err, daemon.ErrManager):
		return fmt.Errorf("%w: check the user service manager with `systemctl --user status` on Linux "+
			"or `launchctl print gui/$(id -u)` on macOS", err)
	case errors.Is(err, daemon.ErrNotActive):
		return fmt.Errorf("%w: see `systemctl --user status %s` on Linux or `launchctl print gui/$(id -u)/%s` "+
			"on macOS, and the log file, for the reason", err, serviceName, serviceLabel)
	default:
		return nil
	}
}
