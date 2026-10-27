package paths_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/paths"
)

func TestXDGOverrides(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("XDG variables are ignored on macOS")
	}

	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")

	if got := paths.DefaultDB(); got != filepath.Join("/tmp/xdg-data", "fdc", "fdc.sqlite") {
		t.Fatalf("DefaultDB = %q", got)
	}

	if got := paths.DefaultLog(); got != filepath.Join("/tmp/xdg-state", "fdc", "fdc.log") {
		t.Fatalf("DefaultLog = %q", got)
	}
}

func TestDefaultsLiveUnderHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	defaults := map[string]string{"db": paths.DefaultDB(), "log": paths.DefaultLog(), "bin": paths.DefaultBin()}

	for name, got := range defaults {
		if !filepath.IsAbs(got) || !strings.Contains(got, "fdc") {
			t.Errorf("%s = %q", name, got)
		}
	}

	if runtime.GOOS == "darwin" {
		inLibrary := strings.Contains(paths.DefaultDB(), "Library/Application Support") &&
			strings.Contains(paths.DefaultLog(), "Library/Logs")
		if !inLibrary {
			t.Fatalf("darwin paths: %s %s", paths.DefaultDB(), paths.DefaultLog())
		}

		return
	}

	underXDG := strings.HasSuffix(paths.DefaultDB(), ".local/share/fdc/fdc.sqlite") &&
		strings.HasSuffix(paths.DefaultLog(), ".local/state/fdc/fdc.log")
	if !underXDG {
		t.Fatalf("linux paths: %s %s", paths.DefaultDB(), paths.DefaultLog())
	}

	if !strings.HasSuffix(paths.DefaultBin(), ".local/bin/fdc") {
		t.Fatalf("bin: %s", paths.DefaultBin())
	}
}

func TestHomeError(t *testing.T) {
	if err := paths.HomeError(); err != nil {
		t.Fatalf("home is set in the test environment: %v", err)
	}

	t.Setenv("HOME", "")

	if err := paths.HomeError(); err == nil {
		t.Fatal("an empty HOME must be reported")
	}
}

func TestConfigDirFollowsXDGThenHome(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")

	if got := paths.ConfigDir(); got != "/xdg/config" {
		t.Fatalf("ConfigDir with XDG_CONFIG_HOME = %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")

	if got := paths.ConfigDir(); got != filepath.Join("/home/test", ".config") {
		t.Fatalf("ConfigDir without XDG_CONFIG_HOME = %q", got)
	}

	if got := paths.Home(); got != "/home/test" {
		t.Fatalf("Home = %q", got)
	}
}
