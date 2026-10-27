package importer

import "errors"

var (
	// ErrMissingFile means a CSV file the import needs is not under --src.
	// Running data/fetch.sh is the usual fix.
	ErrMissingFile = errors.New("csv file missing")
	// ErrUnknownFile means a file name has no registered header. That is a
	// programming error in this package, never a data problem.
	ErrUnknownFile = errors.New("no expected header registered")
	// ErrHeaderMismatch means a CSV header differs from the pinned release.
	// The archive is a different release, and the importer refuses to guess
	// at columns.
	ErrHeaderMismatch = errors.New("csv header does not match the pinned release")
	// ErrBadValue means a cell could not be parsed as the column's type. The
	// wrapped message names the file, the row, the column, and the value.
	ErrBadValue = errors.New("csv value could not be parsed")
	// ErrSmokeTestFailed means the finished database did not report iron for
	// Spinach, raw as USDA publishes it, so the import is not trusted.
	ErrSmokeTestFailed = errors.New("smoke test failed")
)
