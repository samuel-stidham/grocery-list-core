// Package logging configures the process-wide structured logger and owns the
// size-rotating log file the daemon writes to.
//
// The entry points mirror safetybox's logging package so every Go service here
// is set up the same way. The text handler is the standard library one, since
// a daemon's log file is read in an editor and not styled in a terminal.
package logging

import (
	"io"
	"log/slog"
)

// Options control how the logger is built.
type Options struct {
	// Verbose lowers the level from Info to Debug.
	Verbose bool
	// JSON switches the handler from text to JSON, one object per line.
	JSON bool
}

// New builds a logger writing to w.
func New(w io.Writer, opts Options) *slog.Logger {
	level := slog.LevelInfo
	if opts.Verbose {
		level = slog.LevelDebug
	}

	handlerOpts := &slog.HandlerOptions{Level: level}

	if opts.JSON {
		return slog.New(slog.NewJSONHandler(w, handlerOpts))
	}

	return slog.New(slog.NewTextHandler(w, handlerOpts))
}

// Setup builds a logger on w and installs it as the slog default.
func Setup(w io.Writer, opts Options) *slog.Logger {
	logger := New(w, opts)
	slog.SetDefault(logger)

	return logger
}
