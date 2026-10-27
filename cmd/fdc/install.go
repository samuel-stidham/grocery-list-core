package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

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

// Probe timing. systemctl restart returns once the process exists, before
// it has opened the database or bound the port, so install asks the daemon
// itself. Opening the database takes well under a second on a cold cache.
const (
	probeTimeout  = 5 * time.Second
	probeInterval = 200 * time.Millisecond
	probeRequest  = time.Second
)

// installer holds what install and uninstall talk to: the service manager,
// the platform, and the daemon itself. Tests swap all three.
type installer struct {
	runner   daemon.Runner
	platform daemon.Platform
	// probe reports whether a daemon answers on the listen address.
	probe func(ctx context.Context, listen string) error
}

// realInstaller talks to this machine.
func realInstaller() installer {
	return installer{runner: daemon.ExecRunner{}, platform: daemon.Platform(runtime.GOOS), probe: waitForAbout}
}

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

// runInstall writes and starts the user daemon, then waits for it to answer
// before calling the install a success.
func runInstall(args []string, stdout, stderr io.Writer, deps installer) int {
	flags, err := parseInstallFlags(args, stderr)
	if err != nil {
		return exitUsage
	}

	manager, err := daemon.New(deps.platform, deps.runner)
	if err != nil {
		sayf(stderr, "fdc install: %v", userHint(err))

		return exitFailure
	}

	if flags.print {
		_, _ = stdout.Write(daemon.Render(deps.platform, flags.service()))

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

	if err := deps.probe(ctx, flags.listen); err != nil {
		sayf(stderr, "fdc install: %v", userHint(err))

		return exitFailure
	}

	sayf(stdout, "%s is running on %s, logging to %s", serviceName, flags.listen, logTarget(flags.logFile))

	return exitOK
}

// logTarget names where the daemon logs, for the install report.
func logTarget(logFile string) string {
	if logFile == "" {
		return "stderr, which the service manager keeps"
	}

	return logFile
}

// runUninstall stops the daemon and removes its service file. The binary,
// the database and the log stay.
func runUninstall(args []string, stdout, stderr io.Writer, deps installer) int {
	set := flag.NewFlagSet("fdc uninstall", flag.ContinueOnError)
	set.SetOutput(stderr)

	if err := set.Parse(args); err != nil {
		return exitUsage
	}

	manager, err := daemon.New(deps.platform, deps.runner)
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

// waitForAbout polls the daemon's about endpoint until it answers or the
// probe timeout passes.
func waitForAbout(ctx context.Context, listen string) error {
	return pollAbout(ctx, aboutURL(listen), probeTimeout)
}

// pollAbout asks url every probe interval. Anything but a 200 within timeout
// is ErrNotActive, carrying the last answer or connection error.
func pollAbout(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{Timeout: probeRequest}
	ticker := time.NewTicker(probeInterval)

	defer ticker.Stop()

	for {
		last := probeOnce(ctx, client, url)
		if last == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: no answer from %s within %s: %w", daemon.ErrNotActive, url, timeout, last)
		case <-ticker.C:
		}
	}
}

// probeOnce makes one probe request and reads the body out so the connection
// is reused.
func probeOnce(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("probe request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe: %s answered %d", url, resp.StatusCode)
	}

	return nil
}

// aboutURL is the daemon's about endpoint for a -listen address. An empty
// host means every interface, and loopback is the one to ask.
func aboutURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/v1/about"
	}

	if host == "" {
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port) + "/v1/about"
}
