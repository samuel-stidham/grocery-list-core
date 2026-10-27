package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/samuel-stidham/grocery-list-core/internal/daemon"
	"github.com/samuel-stidham/grocery-list-core/internal/paths"
)

// The daemon identity. The frontend repo runs a copy of the daemon package
// under its own name and label.
const (
	serviceName        = "fdc"
	serviceLabel       = "me.samuelstidham.fdc"
	serviceDescription = "FoodData Central nutrient API (fdc)"
)

// installFlags are the serve flags the service file will carry, plus where
// the installed binary is and whether to only print the file.
type installFlags struct {
	serveFlags

	bin   string
	print bool
}

func parseInstallFlags(args []string, stderr io.Writer) (installFlags, error) {
	var flags installFlags

	set := flag.NewFlagSet("fdc install", flag.ContinueOnError)
	set.SetOutput(stderr)
	bindServeFlags(set, &flags.serveFlags, paths.DefaultLog())
	set.StringVar(&flags.bin, "bin", paths.DefaultBin(), "installed binary the service runs")
	set.BoolVar(&flags.print, "print", false, "print the service file and change nothing")

	if err := set.Parse(args); err != nil {
		return flags, fmt.Errorf("parse flags: %w", err)
	}

	return flags, nil
}

// service turns the flags into the daemon definition. Every log setting is
// written out, so the file states how the daemon runs without reference to
// defaults that may change between releases.
func (flags installFlags) service() daemon.Service {
	args := []string{
		"serve",
		"--db", flags.db,
		"--listen", flags.listen,
		"--log-file", flags.logFile,
		"--log-max-mb", strconv.Itoa(flags.logMaxMB),
		"--log-keep", strconv.Itoa(flags.logKeep),
	}

	if flags.logJSON {
		args = append(args, "--log-json")
	}

	if flags.verbose {
		args = append(args, "--verbose")
	}

	return daemon.Service{
		Name:        serviceName,
		Label:       serviceLabel,
		Description: serviceDescription,
		Bin:         flags.bin,
		Args:        args,
		StderrLog:   filepath.Join(paths.LogDir(), serviceName+".stderr.log"),
	}
}

// runInstall writes and starts the user daemon. The runner and the platform
// come from the caller, so tests drive it without a service manager.
func runInstall(args []string, stdout, stderr io.Writer, runner daemon.Runner, platform daemon.Platform) int {
	flags, err := parseInstallFlags(args, stderr)
	if err != nil {
		return exitUsage
	}

	manager, err := daemon.New(platform, runner)
	if err != nil {
		sayf(stderr, "fdc install: %v", userHint(err))

		return exitFailure
	}

	if flags.print {
		_, _ = stdout.Write(daemon.Render(platform, flags.service()))

		return exitOK
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	path, err := manager.Install(ctx, flags.service())
	if err != nil {
		sayf(stderr, "fdc install: %v", userHint(err))

		return exitFailure
	}

	sayf(stdout, "wrote %s", path)
	sayf(stdout, "%s is running on %s, logging to %s", serviceName, flags.listen, flags.logFile)

	return exitOK
}

// runUninstall stops the daemon and removes its service file. The binary,
// the database and the log stay.
func runUninstall(args []string, stdout, stderr io.Writer, runner daemon.Runner, platform daemon.Platform) int {
	set := flag.NewFlagSet("fdc uninstall", flag.ContinueOnError)
	set.SetOutput(stderr)

	if err := set.Parse(args); err != nil {
		return exitUsage
	}

	manager, err := daemon.New(platform, runner)
	if err != nil {
		sayf(stderr, "fdc uninstall: %v", userHint(err))

		return exitFailure
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	path, err := manager.Uninstall(ctx, daemon.Service{Name: serviceName, Label: serviceLabel})

	switch {
	case errors.Is(err, daemon.ErrNotInstalled):
		sayf(stdout, "nothing installed at %s", path)

		return exitOK
	case err != nil:
		sayf(stderr, "fdc uninstall: %v", userHint(err))

		return exitFailure
	}

	sayf(stdout, "removed %s", path)
	sayf(stdout, "the database and the log were left in place")

	return exitOK
}
