package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/daemon"
	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/importer"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

func TestUserHintNamesTheNextStep(t *testing.T) {
	cases := map[string]struct {
		err  error
		hint string
	}{
		"missing file": {fmt.Errorf("%w: food.csv", importer.ErrMissingFile), "make fetch"},
		"header":       {fmt.Errorf("%w: food.csv", importer.ErrHeaderMismatch), "different release"},
		"bad value":    {fmt.Errorf("%w: row 3", importer.ErrBadValue), "inspect the row"},
		"smoke":        {importer.ErrSmokeTestFailed, "output was discarded"},
		"unknown type": {fmt.Errorf("%w: %q", datatypes.ErrUnknownType, "x"), "-types takes"},
		"no types":     {datatypes.ErrNoTypes, "-types takes"},
		"open":         {fmt.Errorf("%w: x.sqlite", store.ErrOpen), "make import"},
		"address in use": {
			&net.OpError{Op: "listen", Err: &os.SyscallError{Syscall: "bind", Err: syscall.EADDRINUSE}},
			"another fdc is probably running",
		},
		"platform":   {fmt.Errorf("%w: %q", daemon.ErrUnsupportedPlatform, "plan9"), "systemd user unit"},
		"binary":     {fmt.Errorf("%w: x", daemon.ErrBinMissing), "make install"},
		"manager":    {fmt.Errorf("%w: exit status 1", daemon.ErrManager), "systemctl --user status"},
		"not active": {fmt.Errorf("%w: fdc.service is failed", daemon.ErrNotActive), "launchctl print gui/$(id -u)/"},
	}

	for name, tc := range cases {
		hinted := userHint(tc.err)
		if !errors.Is(hinted, tc.err) && !strings.Contains(hinted.Error(), tc.err.Error()) {
			t.Errorf("%s: hint must wrap the original: %v", name, hinted)
		}

		if !strings.Contains(hinted.Error(), tc.hint) {
			t.Errorf("%s: want hint %q in %q", name, tc.hint, hinted.Error())
		}
	}
}

func TestUserHintPassesUnknownErrorsThrough(t *testing.T) {
	plain := errors.New("something else")
	if got := userHint(plain); got != plain {
		t.Fatalf("unknown errors must come back unchanged, got %v", got)
	}

	if userHint(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
