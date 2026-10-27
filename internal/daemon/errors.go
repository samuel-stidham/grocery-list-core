package daemon

import "errors"

// Sentinel errors. The command boundary turns them into advice.
var (
	// ErrUnsupportedPlatform means the platform has no service manager this
	// package knows how to drive.
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	// ErrBinMissing means the binary the service would run does not exist
	// where the service file would point, or could not be executed.
	ErrBinMissing = errors.New("binary not found")
	// ErrManager means systemctl or launchctl refused a command. The error
	// carries the command's own output.
	ErrManager = errors.New("service manager command failed")
	// ErrNotActive means the service file was installed but the service did
	// not come up.
	ErrNotActive = errors.New("service is not running")
	// ErrNotInstalled means there is no service file to remove.
	ErrNotInstalled = errors.New("service is not installed")
	// ErrInterrupted means the caller gave up before the service manager
	// finished or confirmed the daemon, by Ctrl-C in practice. The daemon is
	// in whatever state the manager reached. The error carries the command
	// that was cut short, or the last refusal when the interrupt landed
	// between bootstrap attempts.
	ErrInterrupted = errors.New("interrupted")
)
