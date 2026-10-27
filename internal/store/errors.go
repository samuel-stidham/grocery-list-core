package store

import "errors"

var (
	// ErrNotFound means an fdc_id is not in the database.
	ErrNotFound = errors.New("not found")
	// ErrOpen means the database could not be opened for serving. The usual
	// cause is that no import has run yet.
	ErrOpen = errors.New("database could not be opened")
)
