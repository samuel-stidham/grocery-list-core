// Command fdc imports the USDA FoodData Central CSV download into SQLite and
// serves nutrient lookups over a local HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/samuel-stidham/grocery-list-core/internal/api"
	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/importer"
	"github.com/samuel-stidham/grocery-list-core/internal/logging"
	"github.com/samuel-stidham/grocery-list-core/internal/meta"
	"github.com/samuel-stidham/grocery-list-core/internal/paths"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// version is set by the Makefile through -ldflags.
var version = "dev"

const defaultListen = "127.0.0.1:8471"

// HTTP server timeouts. ReadHeaderTimeout is what defeats slowloris. The
// others bound a stuck client without cutting off a large score request.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// megabyte converts the -log-max-mb flag into bytes.
const megabyte = 1 << 20

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// sayf writes a line of output. Write failures on stdout or stderr have no
// useful handling in a command line tool, so they are ignored on purpose.
func sayf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format+"\n", args...)
}

func usage(out io.Writer) {
	sayf(out, `usage: fdc <command> [flags]

commands:
  import     build the SQLite database from the extracted USDA CSV files
  serve      run the HTTP API over the database
  install    set up the user daemon (systemd on Linux, launchd on macOS)
  uninstall  remove the user daemon
  version    print version information

run "fdc <command> -h" for the flags of a command.`)
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)

		return exitUsage
	}

	switch args[0] {
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stderr)
	case "version":
		printVersion(stdout)

		return exitOK
	case "install":
		return runInstall(args[1:], stdout, stderr, realInstaller())
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr, realInstaller())
	case "-h", "--help", "help":
		usage(stdout)

		return exitOK
	}

	sayf(stderr, "fdc: unknown command %q", args[0])
	usage(stderr)

	return exitUsage
}

func printVersion(out io.Writer) {
	sqlite := "unknown"

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "modernc.org/sqlite" {
				sqlite = dep.Version
			}
		}
	}

	sayf(out, "fdc %s\n%s %s/%s\nsqlite %s (modernc.org/sqlite %s)\ndata release %s",
		version, runtime.Version(), runtime.GOOS, runtime.GOARCH, store.LibraryVersion(), sqlite, meta.DataVersion)
}

// importFlags holds the parsed flags of fdc import.
type importFlags struct {
	src        string
	out        string
	types      string
	batch      int
	verifyOnly bool
}

func parseImportFlags(args []string, stderr io.Writer) (importFlags, *flag.FlagSet, error) {
	var flags importFlags

	set := flag.NewFlagSet("fdc import", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&flags.src, "src", "", "directory holding the extracted USDA CSV files (required)")
	set.StringVar(&flags.out, "out", "data/fdc.sqlite", "database to write, replaced atomically on success")
	set.StringVar(&flags.types, "types", strings.Join(datatypes.All(), ","), "comma separated food types to import")
	set.IntVar(&flags.batch, "batch", importer.DefaultBatchSize, "rows per transaction")
	set.BoolVar(&flags.verifyOnly, "verify-only", false, "only verify the database at -out and print the report")

	if err := set.Parse(args); err != nil {
		return flags, set, fmt.Errorf("parse flags: %w", err)
	}

	return flags, set, nil
}

func runImport(args []string, stdout, stderr io.Writer) int {
	flags, set, err := parseImportFlags(args, stderr)
	if err != nil {
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if flags.verifyOnly {
		return runVerify(ctx, flags.out, stdout, stderr)
	}

	if flags.src == "" {
		sayf(stderr, "fdc import: -src is required")
		set.Usage()

		return exitUsage
	}

	selected, err := datatypes.ParseList(flags.types)
	if err != nil {
		sayf(stderr, "fdc import: %v", userHint(err))

		return exitUsage
	}

	report, err := importer.Run(ctx, importer.Options{
		Src:       flags.src,
		Out:       flags.out,
		Types:     selected,
		BatchSize: flags.batch,
		Version:   version,
		Logf: func(format string, logArgs ...any) {
			sayf(stderr, format, logArgs...)
		},
	})
	if report != nil {
		report.Print(stdout)
	}

	if err != nil {
		sayf(stderr, "fdc import: %v", userHint(err))

		return exitFailure
	}

	sayf(stdout, "wrote %s", flags.out)

	return exitOK
}

func runVerify(ctx context.Context, path string, stdout, stderr io.Writer) int {
	report, err := importer.Verify(ctx, path)
	if err != nil {
		sayf(stderr, "fdc import: %v", userHint(err))

		return exitFailure
	}

	report.Print(stdout)

	if report.Smoke.Applicable && !report.Smoke.OK {
		return exitFailure
	}

	return exitOK
}

// serveFlags holds the parsed flags of fdc serve.
type serveFlags struct {
	db string
	// dbExplicit is true when -db was given, so the default path and its
	// home directory lookup are not in play.
	dbExplicit bool
	listen     string
	logFile    string
	logMaxMB   int
	logKeep    int
	logJSON    bool
	verbose    bool
}

func parseServeFlags(args []string, stderr io.Writer) (serveFlags, error) {
	var flags serveFlags

	set := flag.NewFlagSet("fdc serve", flag.ContinueOnError)
	set.SetOutput(stderr)
	bindServeFlags(set, &flags, "")

	if err := set.Parse(args); err != nil {
		return flags, fmt.Errorf("parse flags: %w", err)
	}

	set.Visit(func(given *flag.Flag) {
		if given.Name == "db" {
			flags.dbExplicit = true
		}
	})

	return flags, nil
}

// bindServeFlags registers the flags serve and install share. install writes
// them into the service file, so the two commands cannot drift apart. Only
// the log file default differs: serve logs to stderr unless told otherwise,
// a daemon always logs to a file.
func bindServeFlags(set *flag.FlagSet, flags *serveFlags, logFileDefault string) {
	set.StringVar(&flags.db, "db", paths.DefaultDB(), "database to serve")
	set.StringVar(&flags.listen, "listen", defaultListen, "address to listen on")
	set.StringVar(&flags.logFile, "log-file", logFileDefault,
		"append the log to this file instead of stderr, rotating by size")
	set.IntVar(&flags.logMaxMB, "log-max-mb", int(logging.DefaultMaxBytes/megabyte),
		"rotate the log file past this many megabytes")
	set.IntVar(&flags.logKeep, "log-keep", logging.DefaultKeep, "rotated log files to keep")
	set.BoolVar(&flags.logJSON, "log-json", false, "write log records as JSON objects")
	set.BoolVar(&flags.verbose, "verbose", false, "log at debug level")
}

// serveRun carries what the serve command needs across its steps.
type serveRun struct {
	flags  serveFlags
	logger *slog.Logger
	// toFile is true when the logger writes to -log-file. Fatal errors are
	// then recorded there too, so the file explains why the daemon stopped.
	toFile bool
	stderr io.Writer
}

// fail reports a fatal serve error on stderr, where whoever started the
// process is looking, with the hint for well-known causes.
func (serve *serveRun) fail(err error) int {
	err = userHint(err)
	sayf(serve.stderr, "fdc serve: %v", err)

	if serve.toFile {
		serve.logger.Error("stopped", "error", err.Error())
	}

	return exitFailure
}

// nopCloser stands in for the log file when the log goes to stderr, which
// the command does not own and must not close.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// openLogger builds the logger from the flags. Without -log-file it writes
// to stderr, which is the journal under systemd and the terminal under make.
func openLogger(flags serveFlags, stderr io.Writer) (*slog.Logger, io.Closer, error) {
	opts := logging.Options{Verbose: flags.verbose, JSON: flags.logJSON}

	if flags.logFile == "" {
		return logging.Setup(stderr, opts), nopCloser{}, nil
	}

	file, err := logging.OpenRotating(flags.logFile, int64(flags.logMaxMB)*megabyte, flags.logKeep)
	if err != nil {
		return nil, nil, fmt.Errorf("log file %s: %w", flags.logFile, err)
	}

	// slog drops write errors, so a rotation failure is announced once on
	// stderr, which the service manager keeps.
	file.AlertTo(stderr)

	return logging.Setup(file, opts), file, nil
}

func runServe(args []string, stderr io.Writer) int {
	flags, err := parseServeFlags(args, stderr)
	if err != nil {
		return exitUsage
	}

	logger, closeLog, err := openLogger(flags, stderr)
	if err != nil {
		sayf(stderr, "fdc serve: %v", err)

		return exitFailure
	}
	defer func() { _ = closeLog.Close() }()

	serve := &serveRun{flags: flags, logger: logger, toFile: flags.logFile != "", stderr: stderr}

	if err := paths.HomeError(); err != nil && !flags.dbExplicit {
		logger.Warn("home directory unavailable, the default -db path falls back to the working directory",
			"error", err.Error())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := store.OpenReadOnly(ctx, flags.db)
	if err != nil {
		return serve.fail(err)
	}
	defer func() { _ = database.Close() }()

	server, err := api.New(ctx, database, api.Options{
		Version: version,
		Logger:  logger,
		Hosts:   []string{listenHost(flags.listen)},
	})
	if err != nil {
		return serve.fail(err)
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", flags.listen)
	if err != nil {
		return serve.fail(err)
	}

	dataVersion, importedAt := server.Imported()

	logger.Info("serving",
		"version", version,
		"db", flags.db,
		"data_version", dataVersion,
		"imported_at", importedAt,
		"listen", listener.Addr().String(),
	)

	return serve.untilSignal(ctx, newHTTPServer(server.Handler(), logger), listener)
}

// newHTTPServer wraps the handler with the timeouts every daemon here uses.
// ErrorLog routes net/http's own complaints, recovered handler panics among
// them, through the same logger, so nothing bypasses -log-file.
func newHTTPServer(handler http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

// listenHost is the host part of a -listen address, so a server bound to a
// LAN address also answers requests that name that address.
func listenHost(listen string) string {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}

	return host
}

// untilSignal runs the server until it fails or the context is cancelled by
// a signal, then shuts it down gracefully. Serve owns the listener and closes
// it on both paths.
func (serve *serveRun) untilSignal(ctx context.Context, httpServer *http.Server, listener net.Listener) int {
	errc := make(chan error, 1)

	go func() { errc <- httpServer.Serve(listener) }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return serve.fail(err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return serve.fail(fmt.Errorf("shutdown: %w", err))
		}

		serve.logger.Info("stopped")
	}

	return exitOK
}
