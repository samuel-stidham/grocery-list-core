package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/daemon"
	"github.com/samuel-stidham/grocery-list-core/internal/paths"
)

// fakeRunner records service manager commands and answers from canned
// outputs and failures keyed by the full command line.
type fakeRunner struct {
	calls   []string
	outputs map[string]string
	fails   map[string]error
}

func (fake *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	fake.calls = append(fake.calls, line)

	return []byte(fake.outputs[line]), fake.fails[line]
}

// activeRunner answers is-active the way a healthy unit does.
func activeRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{"systemctl --user is-active fdc.service": "active\n"}}
}

// installHome points every default path at a fresh directory and returns it
// together with a binary file the unit can point at.
func installHome(t *testing.T) (string, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, "")
	}

	bin := filepath.Join(home, ".local", "bin", "fdc")
	if err := os.MkdirAll(filepath.Dir(bin), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), testFileMode); err != nil {
		t.Fatal(err)
	}

	return home, bin
}

func unitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", "fdc.service")
}

func TestInstallPrintChangesNothing(t *testing.T) {
	home, bin := installHome(t)
	fake := &fakeRunner{}

	var stdout, stderr bytes.Buffer

	code := runInstall([]string{"-print", "-bin", bin}, &stdout, &stderr, fake, daemon.Linux)
	if code != exitOK {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}

	wants := []string{
		"ExecStart=" + bin + " serve",
		"--db " + paths.DefaultDB(),
		"--listen 127.0.0.1:8471",
		"--log-file " + paths.DefaultLog(),
		"--log-max-mb 10 --log-keep 5",
	}

	for _, want := range wants {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("printed unit lacks %q:\n%s", want, stdout.String())
		}
	}

	if len(fake.calls) != 0 {
		t.Fatalf("print ran commands: %v", fake.calls)
	}

	if _, err := os.Stat(filepath.Join(home, ".config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("print wrote under the home: %v", err)
	}
}

func TestInstallWritesTheUnitAndReportsIt(t *testing.T) {
	home, bin := installHome(t)
	fake := activeRunner()

	var stdout, stderr bytes.Buffer

	code := runInstall([]string{"-bin", bin, "-log-json", "-verbose"}, &stdout, &stderr, fake, daemon.Linux)
	if code != exitOK {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}

	content, err := os.ReadFile(unitPath(home))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "--log-json --verbose") {
		t.Fatalf("unit lacks the log flags:\n%s", content)
	}

	running := "fdc is running on 127.0.0.1:8471, logging to " + paths.DefaultLog()

	for _, want := range []string{"wrote " + unitPath(home), running} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}

	if len(fake.calls) != 4 {
		t.Fatalf("commands run: %v", fake.calls)
	}
}

func TestInstallFlagErrors(t *testing.T) {
	code := runInstall([]string{"-bogus"}, io.Discard, io.Discard, &fakeRunner{}, daemon.Linux)
	if code != exitUsage {
		t.Fatalf("code %d", code)
	}
}

func TestInstallMissingBinaryHintsAtMakeInstall(t *testing.T) {
	_, bin := installHome(t)
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}

	fake := &fakeRunner{}

	var stdout, stderr bytes.Buffer

	code := runInstall([]string{"-bin", bin}, &stdout, &stderr, fake, daemon.Linux)
	if code != exitFailure || !strings.Contains(stderr.String(), "make install") {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}

	if len(fake.calls) != 0 {
		t.Fatalf("commands run without a binary: %v", fake.calls)
	}
}

func TestInstallManagerFailureHintsAtTheUserSession(t *testing.T) {
	_, bin := installHome(t)
	fake := &fakeRunner{
		outputs: map[string]string{"systemctl --user daemon-reload": "Failed to connect to bus: No medium found\n"},
		fails:   map[string]error{"systemctl --user daemon-reload": errors.New("exit status 1")},
	}

	var stdout, stderr bytes.Buffer

	code := runInstall([]string{"-bin", bin}, &stdout, &stderr, fake, daemon.Linux)
	if code != exitFailure {
		t.Fatalf("code %d", code)
	}

	for _, want := range []string{"Failed to connect to bus", "systemctl --user status"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, stderr.String())
		}
	}
}

func TestInstallInactiveServiceHintsAtTheLog(t *testing.T) {
	_, bin := installHome(t)
	fake := &fakeRunner{
		outputs: map[string]string{"systemctl --user is-active fdc.service": "inactive\n"},
		fails:   map[string]error{"systemctl --user is-active fdc.service": errors.New("exit status 3")},
	}

	var stdout, stderr bytes.Buffer

	code := runInstall([]string{"-bin", bin}, &stdout, &stderr, fake, daemon.Linux)
	if code != exitFailure {
		t.Fatalf("code %d", code)
	}

	for _, want := range []string{"fdc.service is inactive", "log file"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, stderr.String())
		}
	}
}

func TestInstallAndUninstallRejectAnUnknownPlatform(t *testing.T) {
	_, bin := installHome(t)

	var stderr bytes.Buffer

	code := runInstall([]string{"-bin", bin}, io.Discard, &stderr, &fakeRunner{}, "plan9")
	if code != exitFailure || !strings.Contains(stderr.String(), "unsupported platform") {
		t.Fatalf("install: code %d stderr %q", code, stderr.String())
	}

	stderr.Reset()

	code = runUninstall(nil, io.Discard, &stderr, &fakeRunner{}, "plan9")
	if code != exitFailure || !strings.Contains(stderr.String(), "unsupported platform") {
		t.Fatalf("uninstall: code %d stderr %q", code, stderr.String())
	}
}

func TestUninstallRemovesTheUnit(t *testing.T) {
	home, bin := installHome(t)
	fake := activeRunner()

	if code := runInstall([]string{"-bin", bin}, io.Discard, io.Discard, fake, daemon.Linux); code != exitOK {
		t.Fatalf("install code %d", code)
	}

	fake.calls = nil

	var stdout, stderr bytes.Buffer

	code := runUninstall(nil, &stdout, &stderr, fake, daemon.Linux)
	if code != exitOK {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}

	for _, want := range []string{"removed " + unitPath(home), "database and the log were left in place"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}

	if _, err := os.Stat(unitPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unit still there: %v", err)
	}

	want := []string{"systemctl --user disable --now fdc.service", "systemctl --user daemon-reload"}
	if strings.Join(fake.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands run: %v", fake.calls)
	}
}

func TestUninstallWhenNothingIsInstalled(t *testing.T) {
	home, _ := installHome(t)
	fake := &fakeRunner{}

	var stdout, stderr bytes.Buffer

	code := runUninstall(nil, &stdout, &stderr, fake, daemon.Linux)
	if code != exitOK || !strings.Contains(stdout.String(), "nothing installed at "+unitPath(home)) {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}

	if len(fake.calls) != 0 {
		t.Fatalf("commands run: %v", fake.calls)
	}
}

func TestUninstallFlagErrors(t *testing.T) {
	code := runUninstall([]string{"-bogus"}, io.Discard, io.Discard, &fakeRunner{}, daemon.Linux)
	if code != exitUsage {
		t.Fatalf("code %d", code)
	}
}

func TestInstallCommandsAreWired(t *testing.T) {
	_, bin := installHome(t)

	code, stdout, stderr := runCapture("install", "-print", "-bin", bin)
	if code != exitOK || !strings.Contains(stdout, bin) {
		t.Fatalf("install -print: code %d stdout %q stderr %q", code, stdout, stderr)
	}

	code, _, stderr = runCapture("uninstall", "-h")
	if code != exitUsage || !strings.Contains(stderr, "Usage of fdc uninstall") {
		t.Fatalf("uninstall -h: code %d stderr %q", code, stderr)
	}
}
