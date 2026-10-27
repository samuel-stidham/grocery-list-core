// Package daemon installs and removes the user service that keeps the server
// running: a systemd user unit on Linux, a launchd agent on macOS. It renders
// the service file, writes it under the user's home, and drives the service
// manager through a Runner, so tests never call systemctl or launchctl.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/samuel-stidham/grocery-list-core/internal/paths"
)

// File modes. The service file holds no secret, but nothing other than the
// user's own service manager reads it, so it gets the private mode.
const (
	dirMode  = 0o750
	fileMode = 0o600
)

// Service describes one daemon to install. The frontend repo copies this
// package and fills the same fields with its own name and label.
type Service struct {
	// Name is the systemd unit stem: fdc becomes fdc.service.
	Name string
	// Label is the launchd label and plist stem, reverse DNS by convention.
	Label string
	// Description is the line the service manager shows for the service.
	Description string
	// Bin is the absolute path of the binary to run. It must exist.
	Bin string
	// Args follow Bin on the command line.
	Args []string
	// StderrLog is where launchd sends standard error, which carries the
	// log rotation alerts. systemd sends it to the journal and ignores this.
	StderrLog string
}

// Platform selects the service manager.
type Platform string

// The platforms with a writer.
const (
	Linux  Platform = "linux"
	Darwin Platform = "darwin"
)

// Runner executes one service manager command and returns its combined
// output. Tests inject a recorder.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs the command for real.
type ExecRunner struct{}

// Run executes name with args and returns stdout and stderr together. The
// name is systemctl or launchctl and every argument comes from Service
// fields. Nothing passes through a shell.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	// #nosec G204 -- name is systemctl or launchctl, args come from Service fields, no shell
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}

	return output, nil
}

// Manager installs and removes services on one platform.
type Manager struct {
	platform Platform
	runner   Runner
	// uid names the launchd domain gui/<uid>. Unused on Linux.
	uid int
}

// New returns the manager for platform, or ErrUnsupportedPlatform.
func New(platform Platform, runner Runner) (*Manager, error) {
	switch platform {
	case Linux, Darwin:
		return &Manager{platform: platform, runner: runner, uid: os.Getuid()}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedPlatform, platform)
	}
}

// Path is where the service file lives: the systemd user unit directory
// under the configuration root on Linux, ~/Library/LaunchAgents on macOS.
func (manager *Manager) Path(service Service) string {
	if manager.platform == Darwin {
		return filepath.Join(paths.Home(), "Library", "LaunchAgents", service.Label+".plist")
	}

	return filepath.Join(paths.ConfigDir(), "systemd", "user", service.Name+".service")
}

// Install writes the service file, loads it, starts the service, and
// confirms it is running. Running it again adopts an existing file and
// restarts the service on the new definition. It returns the path written.
func (manager *Manager) Install(ctx context.Context, service Service) (string, error) {
	if err := checkBin(service.Bin); err != nil {
		return "", err
	}

	path := manager.Path(service)

	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	if err := replaceFile(path, Render(manager.platform, service)); err != nil {
		return "", err
	}

	if manager.platform == Darwin {
		return path, manager.loadAgent(ctx, service, path)
	}

	return path, manager.loadUnit(ctx, service)
}

// Uninstall stops and unloads the service and removes its file. The binary,
// the database and the log stay. A missing file is ErrNotInstalled. The path
// comes back on every return so the caller can name it.
func (manager *Manager) Uninstall(ctx context.Context, service Service) (string, error) {
	path := manager.Path(service)

	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, fmt.Errorf("%w: %s", ErrNotInstalled, path)
	}

	if err != nil {
		return path, fmt.Errorf("stat %s: %w", path, err)
	}

	if err := manager.unload(ctx, service); err != nil {
		return path, err
	}

	if err := os.Remove(path); err != nil {
		return path, fmt.Errorf("remove %s: %w", path, err)
	}

	if manager.platform == Linux {
		if _, err := manager.systemctl(ctx, "daemon-reload"); err != nil {
			return path, err
		}
	}

	return path, nil
}

// replaceFile writes the service file beside its target and renames it into
// place. The rename swaps a hand-placed file the user owns the directory of
// but cannot open for writing, and the service manager never sees a half
// written file.
func replaceFile(path string, content []byte) error {
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, content, fileMode); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("replace %s: %w", path, err)
	}

	return nil
}

// checkBin rejects a binary the service manager could not start: a relative
// path, a missing file, or a directory.
func checkBin(bin string) error {
	if !filepath.IsAbs(bin) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrBinMissing, bin)
	}

	info, err := os.Stat(bin)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBinMissing, err)
	}

	if info.IsDir() {
		return fmt.Errorf("%w: %s is a directory", ErrBinMissing, bin)
	}

	return nil
}

// loadUnit tells systemd about the file, enables it for every login, and
// restarts it so a running service picks up the new definition. restart
// starts a stopped service too. is-active then confirms the result.
func (manager *Manager) loadUnit(ctx context.Context, service Service) error {
	unit := service.Name + ".service"

	for _, step := range [][]string{{"daemon-reload"}, {"enable", unit}, {"restart", unit}} {
		if _, err := manager.systemctl(ctx, step...); err != nil {
			return err
		}
	}

	// is-active exits non-zero for anything but active and prints the state,
	// so the output is the message either way.
	state, err := manager.runner.Run(ctx, "systemctl", "--user", "is-active", unit)
	if err != nil || strings.TrimSpace(string(state)) != "active" {
		return fmt.Errorf("%w: %s is %s", ErrNotActive, unit, strings.TrimSpace(string(state)))
	}

	return nil
}

// loadAgent replaces the agent in the user's GUI domain. bootout fails when
// nothing is loaded, which is the normal first install, so its result is
// ignored. bootstrap loads the plist, RunAtLoad starts the service, and
// print confirms launchd knows it.
func (manager *Manager) loadAgent(ctx context.Context, service Service, path string) error {
	_, _ = manager.launchctl(ctx, "bootout", manager.target(service))

	if _, err := manager.launchctl(ctx, "bootstrap", manager.domain(), path); err != nil {
		return err
	}

	if _, err := manager.launchctl(ctx, "print", manager.target(service)); err != nil {
		return fmt.Errorf("%w: %w", ErrNotActive, err)
	}

	return nil
}

// unload stops the service before its file goes. On macOS a service that is
// not loaded is already what uninstall wants, so bootout's result is ignored.
func (manager *Manager) unload(ctx context.Context, service Service) error {
	if manager.platform == Darwin {
		_, _ = manager.launchctl(ctx, "bootout", manager.target(service))

		return nil
	}

	_, err := manager.systemctl(ctx, "disable", "--now", service.Name+".service")

	return err
}

func (manager *Manager) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	return manager.run(ctx, "systemctl", append([]string{"--user"}, args...)...)
}

func (manager *Manager) launchctl(ctx context.Context, args ...string) ([]byte, error) {
	return manager.run(ctx, "launchctl", args...)
}

// domain is the launchd domain of the logged in user.
func (manager *Manager) domain() string {
	return fmt.Sprintf("gui/%d", manager.uid)
}

// target names the agent inside its domain.
func (manager *Manager) target(service Service) string {
	return manager.domain() + "/" + service.Label
}

// run wraps a failed manager command in ErrManager together with its output,
// which is where systemctl and launchctl explain themselves.
func (manager *Manager) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := manager.runner.Run(ctx, name, args...)
	if err == nil {
		return output, nil
	}

	if text := strings.TrimSpace(string(output)); text != "" {
		return output, fmt.Errorf("%w: %w: %s", ErrManager, err, text)
	}

	return output, fmt.Errorf("%w: %w", ErrManager, err)
}
