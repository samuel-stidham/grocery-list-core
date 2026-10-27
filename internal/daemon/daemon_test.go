package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/daemon"
)

const (
	testDirMode  = 0o750
	testFileMode = 0o600
)

// goldenService carries every character the writers must handle: spaces, a
// percent sign, a dollar sign, and an ampersand in paths.
func goldenService() daemon.Service {
	return daemon.Service{
		Name:        "fdc",
		Label:       "me.samuelstidham.fdc",
		Description: "FoodData Central nutrient API (fdc), 100% local",
		Bin:         "/home/test user/.local/bin/fdc",
		Args: []string{
			"serve",
			"--db", "/home/test user/Data & $Files/fdc.sqlite",
			"--listen", "127.0.0.1:8471",
			"--log-file", "/home/test user/logs/100% rotated/fdc.log",
			"--log-max-mb", "10",
			"--log-keep", "5",
		},
		StderrLog: "/home/test user/Library/Logs/fdc/fdc.stderr.log",
	}
}

// recorder stands in for systemctl and launchctl. It records every command
// as one line and answers from canned outputs and failures keyed the same
// way, so a test states exactly which call misbehaves.
type recorder struct {
	calls   []string
	outputs map[string]string
	fails   map[string]error
}

func (rec *recorder) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	rec.calls = append(rec.calls, line)

	return []byte(rec.outputs[line]), rec.fails[line]
}

func newManager(t *testing.T, platform daemon.Platform, rec *recorder) *daemon.Manager {
	t.Helper()

	manager, err := daemon.New(platform, rec)
	if err != nil {
		t.Fatal(err)
	}

	return manager
}

// setup points HOME at a fresh directory, creates a binary file there, and
// returns a service that runs it.
func setup(t *testing.T) (string, daemon.Service) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	bin := filepath.Join(home, "bin", "fdc")
	if err := os.MkdirAll(filepath.Dir(bin), testDirMode); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), testFileMode); err != nil {
		t.Fatal(err)
	}

	service := goldenService()
	service.Bin = bin

	return home, service
}

func target(service daemon.Service) string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), service.Label)
}

func assertCalls(t *testing.T, rec *recorder, want ...string) {
	t.Helper()

	if !slices.Equal(rec.calls, want) {
		t.Fatalf("commands run:\n%s\nwant:\n%s", strings.Join(rec.calls, "\n"), strings.Join(want, "\n"))
	}
}

func assertGone(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s should not exist: %v", path, err)
	}
}

func TestRenderMatchesGolden(t *testing.T) {
	files := map[daemon.Platform]string{daemon.Linux: "fdc.service", daemon.Darwin: "fdc.plist"}

	for platform, file := range files {
		want, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}

		if got := daemon.Render(platform, goldenService()); !bytes.Equal(got, want) {
			t.Errorf("%s output differs from testdata/%s:\n%s", platform, file, got)
		}
	}
}

func TestPathFollowsThePlatform(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	t.Setenv("XDG_CONFIG_HOME", "")

	linux := newManager(t, daemon.Linux, &recorder{})
	darwin := newManager(t, daemon.Darwin, &recorder{})
	service := goldenService()

	if got := linux.Path(service); got != "/home/test/.config/systemd/user/fdc.service" {
		t.Fatalf("linux path = %q", got)
	}

	if got := darwin.Path(service); got != "/home/test/Library/LaunchAgents/me.samuelstidham.fdc.plist" {
		t.Fatalf("darwin path = %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	if got := linux.Path(service); got != "/xdg/systemd/user/fdc.service" {
		t.Fatalf("linux path under XDG_CONFIG_HOME = %q", got)
	}
}

func TestInstallLinuxWritesTheUnitAndStartsIt(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{outputs: map[string]string{"systemctl --user is-active fdc.service": "active\n"}}
	manager := newManager(t, daemon.Linux, rec)

	// A hand-placed unit with other content and a wider mode is adopted.
	path := manager.Path(service)
	if err := os.MkdirAll(filepath.Dir(path), testDirMode); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("[Unit]\nDescription=old\n"), testFileMode); err != nil {
		t.Fatal(err)
	}

	got, err := manager.Install(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}

	if got != path {
		t.Fatalf("Install returned %q, want %q", got, path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, daemon.Render(daemon.Linux, service)) {
		t.Fatalf("unit on disk:\n%s", content)
	}

	assertCalls(t, rec,
		"systemctl --user daemon-reload",
		"systemctl --user enable fdc.service",
		"systemctl --user restart fdc.service",
		"systemctl --user is-active fdc.service",
	)
}

func TestInstallReplacesAUnitItCannotOpenForWriting(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{outputs: map[string]string{"systemctl --user is-active fdc.service": "active"}}
	manager := newManager(t, daemon.Linux, rec)

	// A read-only file stands in for one placed by hand under another owner.
	// The directory is the user's, so a rename over it still works.
	path := manager.Path(service)
	if err := os.MkdirAll(filepath.Dir(path), testDirMode); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("[Unit]\nDescription=old\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.Install(context.Background(), service); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, daemon.Render(daemon.Linux, service)) {
		t.Fatalf("unit on disk:\n%s", content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != testFileMode {
		t.Fatalf("replaced unit mode %o", info.Mode().Perm())
	}

	assertGone(t, path+".tmp")
}

func TestInstallCreatesTheUnitDirectoryWithPrivateModes(t *testing.T) {
	home, service := setup(t)
	rec := &recorder{outputs: map[string]string{"systemctl --user is-active fdc.service": "active"}}

	path, err := newManager(t, daemon.Linux, rec).Install(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(path, filepath.Join(home, ".config", "systemd", "user")) {
		t.Fatalf("unit written outside the home: %s", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != testFileMode {
		t.Fatalf("unit mode %o", info.Mode().Perm())
	}

	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if dir.Mode().Perm() != testDirMode {
		t.Fatalf("unit directory mode %o", dir.Mode().Perm())
	}
}

func TestInstallReportsAServiceThatDidNotComeUp(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{
		outputs: map[string]string{"systemctl --user is-active fdc.service": "failed\n"},
		fails:   map[string]error{"systemctl --user is-active fdc.service": errors.New("exit status 3")},
	}

	_, err := newManager(t, daemon.Linux, rec).Install(context.Background(), service)
	if !errors.Is(err, daemon.ErrNotActive) || !strings.Contains(err.Error(), "fdc.service is failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallStopsAtTheFirstManagerFailure(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{
		outputs: map[string]string{"systemctl --user enable fdc.service": "Failed to connect to bus\n"},
		fails:   map[string]error{"systemctl --user enable fdc.service": errors.New("exit status 1")},
	}
	manager := newManager(t, daemon.Linux, rec)

	_, err := manager.Install(context.Background(), service)
	if !errors.Is(err, daemon.ErrManager) || !strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Fatalf("err = %v", err)
	}

	assertCalls(t, rec, "systemctl --user daemon-reload", "systemctl --user enable fdc.service")

	// The file stays so a retry after fixing the session needs no rewrite.
	if _, err := os.Stat(manager.Path(service)); err != nil {
		t.Fatalf("unit file: %v", err)
	}
}

func TestInstallRefusesABinaryTheManagerCouldNotStart(t *testing.T) {
	home, service := setup(t)
	rec := &recorder{}
	manager := newManager(t, daemon.Linux, rec)

	cases := map[string]string{
		"missing":   filepath.Join(home, "bin", "missing"),
		"relative":  "dist/fdc",
		"directory": filepath.Join(home, "bin"),
	}

	for name, bin := range cases {
		service.Bin = bin

		_, err := manager.Install(context.Background(), service)
		if !errors.Is(err, daemon.ErrBinMissing) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	assertCalls(t, rec)
	assertGone(t, manager.Path(service))
}

func TestInstallDarwinReplacesTheAgent(t *testing.T) {
	home, service := setup(t)
	rec := &recorder{fails: map[string]error{"launchctl bootout " + target(service): errors.New("exit status 3")}}
	manager := newManager(t, daemon.Darwin, rec)

	path, err := manager.Install(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}

	if path != filepath.Join(home, "Library", "LaunchAgents", "me.samuelstidham.fdc.plist") {
		t.Fatalf("plist path %q", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, daemon.Render(daemon.Darwin, service)) {
		t.Fatalf("plist on disk:\n%s", content)
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	assertCalls(t, rec,
		"launchctl bootout "+target(service),
		"launchctl bootstrap "+domain+" "+path,
		"launchctl print "+target(service),
	)
}

func TestInstallDarwinReportsAnAgentLaunchdDoesNotKnow(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{fails: map[string]error{"launchctl print " + target(service): errors.New("exit status 113")}}

	_, err := newManager(t, daemon.Darwin, rec).Install(context.Background(), service)
	if !errors.Is(err, daemon.ErrNotActive) {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallLinuxStopsRemovesAndReloads(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{outputs: map[string]string{"systemctl --user is-active fdc.service": "active"}}
	manager := newManager(t, daemon.Linux, rec)

	path, err := manager.Install(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}

	rec.calls = nil

	got, err := manager.Uninstall(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}

	if got != path {
		t.Fatalf("Uninstall returned %q, want %q", got, path)
	}

	assertGone(t, path)
	assertCalls(t, rec, "systemctl --user disable --now fdc.service", "systemctl --user daemon-reload")
}

func TestUninstallWithoutAUnitIsNotInstalled(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{}
	manager := newManager(t, daemon.Linux, rec)

	path, err := manager.Uninstall(context.Background(), service)
	if !errors.Is(err, daemon.ErrNotInstalled) {
		t.Fatalf("err = %v", err)
	}

	if path != manager.Path(service) {
		t.Fatalf("path %q", path)
	}

	assertCalls(t, rec)
}

func TestUninstallKeepsTheUnitWhenTheManagerRefuses(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{
		outputs: map[string]string{"systemctl --user is-active fdc.service": "active"},
		fails:   map[string]error{"systemctl --user disable --now fdc.service": errors.New("exit status 1")},
	}
	manager := newManager(t, daemon.Linux, rec)

	path, err := manager.Install(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}

	rec.calls = nil

	if _, err := manager.Uninstall(context.Background(), service); !errors.Is(err, daemon.ErrManager) {
		t.Fatalf("err = %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unit must survive a failed stop: %v", err)
	}

	assertCalls(t, rec, "systemctl --user disable --now fdc.service")
}

func TestUninstallDarwinIgnoresAnAgentThatIsNotLoaded(t *testing.T) {
	_, service := setup(t)
	rec := &recorder{fails: map[string]error{"launchctl bootout " + target(service): errors.New("exit status 3")}}
	manager := newManager(t, daemon.Darwin, rec)

	path, err := manager.Install(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}

	rec.calls = nil

	if _, err := manager.Uninstall(context.Background(), service); err != nil {
		t.Fatal(err)
	}

	assertGone(t, path)
	assertCalls(t, rec, "launchctl bootout "+target(service))
}

func TestNewRejectsAnUnknownPlatform(t *testing.T) {
	if _, err := daemon.New("plan9", &recorder{}); !errors.Is(err, daemon.ErrUnsupportedPlatform) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRunnerReturnsOutputAndNamesAFailedCommand(t *testing.T) {
	ctx := context.Background()

	output, err := daemon.ExecRunner{}.Run(ctx, "echo", "hi")
	if err != nil || string(output) != "hi\n" {
		t.Fatalf("echo: %q %v", output, err)
	}

	output, err = daemon.ExecRunner{}.Run(ctx, "sh", "-c", "echo oops >&2; exit 3")
	if err == nil || !strings.Contains(string(output), "oops") || !strings.Contains(err.Error(), "sh -c") {
		t.Fatalf("failing command: %q %v", output, err)
	}
}
