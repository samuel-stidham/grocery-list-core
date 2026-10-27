// Package paths knows where the installed binary, the served database, and
// the log file live on each platform. The daemon units point at these paths.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const appName = "fdc"

// Home returns the user's home directory. When the environment has none,
// paths fall back to the working directory, which is documented behaviour
// for a process started without HOME. The daemon writers build the service
// file path from it.
func Home() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	return dir
}

// ConfigDir is the user configuration root on Linux, where the systemd user
// unit directory lives. XDG_CONFIG_HOME overrides the ~/.config default.
func ConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}

	return filepath.Join(Home(), ".config")
}

// HomeError reports why the home directory could not be resolved, or nil.
// Callers log it once at startup so a fallback to the working directory is
// never silent.
func HomeError() error {
	if _, err := os.UserHomeDir(); err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	return nil
}

// DataDir is the directory of the served database.
func DataDir() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(Home(), "Library", "Application Support", appName)
	}

	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, appName)
	}

	return filepath.Join(Home(), ".local", "share", appName)
}

// DefaultDB is the database path the serve and install commands default to.
func DefaultDB() string {
	return filepath.Join(DataDir(), appName+".sqlite")
}

// LogDir is the platform user log directory.
func LogDir() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(Home(), "Library", "Logs", appName)
	}

	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, appName)
	}

	return filepath.Join(Home(), ".local", "state", appName)
}

// DefaultLog is the daemon log file.
func DefaultLog() string {
	return filepath.Join(LogDir(), appName+".log")
}

// DefaultBin is where make install puts the binary.
func DefaultBin() string {
	return filepath.Join(Home(), ".local", "bin", appName)
}
