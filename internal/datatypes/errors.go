package datatypes

import "errors"

var (
	// ErrUnknownType means a type name is not one of the four short names.
	ErrUnknownType = errors.New("unknown food type")
	// ErrNoTypes means a type list was empty after trimming.
	ErrNoTypes = errors.New("no food types given")
)
