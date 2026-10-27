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
	// ErrInterrupted means the wait for the daemon was cut short by the
	// caller, an interrupt in practice. The service file is in place and the
	// service manager was told to start it, so the daemon may come up anyway.
	ErrInterrupted = errors.New("interrupted")
)
