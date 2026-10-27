package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/samuel-stidham/grocery-list-core/"

// goListDeps returns the transitive imports of one package in this module,
// as go list reports them.
func goListDeps(t *testing.T, pkg string) map[string]bool {
	t.Helper()

	// #nosec G204 -- pkg comes from the constant lists in this file, never from input
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "-f", "{{.ImportPath}}", pkg)
	cmd.Dir = "../.."

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}

	deps := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		deps[line] = true
	}

	return deps
}

// TestInnerPackagesNeverImportOutward pins the dependency direction. The
// data packages know nothing about HTTP or the import pipeline, and the two
// pipelines know nothing about each other or the command.
func TestInnerPackagesNeverImportOutward(t *testing.T) {
	inner := []string{"internal/datatypes", "internal/meta", "internal/store", "internal/paths", "internal/logging"}
	outer := []string{"internal/api", "internal/importer", "cmd/fdc"}

	for _, pkg := range inner {
		deps := goListDeps(t, module+pkg)

		for _, forbidden := range outer {
			if deps[module+forbidden] {
				t.Errorf("%s imports %s, which points outward", pkg, forbidden)
			}
		}
	}

	// The two pipelines are peers: neither imports the other or the command.
	peers := map[string]string{"internal/api": "internal/importer", "internal/importer": "internal/api"}

	for pkg, other := range peers {
		deps := goListDeps(t, module+pkg)

		if deps[module+"cmd/fdc"] {
			t.Errorf("%s imports the command", pkg)
		}

		if deps[module+other] {
			t.Errorf("%s imports %s; the pipelines stay independent", pkg, other)
		}
	}
}
