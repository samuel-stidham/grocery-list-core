package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samuel-stidham/grocery-list-core/internal/logging"
)

const testFileMode = 0o600

func runCapture(args ...string) (int, string, string) {
	var out, errb bytes.Buffer

	code := run(args, &out, &errb)

	return code, out.String(), errb.String()
}

func TestNoArgsPrintsUsage(t *testing.T) {
	code, _, stderr := runCapture()
	if code != exitUsage || !strings.Contains(stderr, "usage: fdc") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := runCapture("frobnicate")
	if code != exitUsage || !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestHelp(t *testing.T) {
	code, stdout, _ := runCapture("help")
	if code != exitOK || !strings.Contains(stdout, "import") || !strings.Contains(stdout, "serve") {
		t.Fatalf("code %d stdout %q", code, stdout)
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := runCapture("version")
	if code != exitOK {
		t.Fatalf("code %d", code)
	}

	for _, want := range []string{"fdc dev", "sqlite 3.", "modernc.org/sqlite v", "data release 2026-04-30"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("version output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestImportFlagErrors(t *testing.T) {
	code, _, stderr := runCapture("import")
	if code != exitUsage || !strings.Contains(stderr, "-src is required") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}

	code, _, stderr = runCapture("import", "-src", "x", "-types", "sr_legacy,bogus")
	if code != exitUsage || !strings.Contains(stderr, `unknown food type: "bogus"`) {
		t.Fatalf("code %d stderr %q", code, stderr)
	}

	code, _, _ = runCapture("import", "-bogus-flag")
	if code != exitUsage {
		t.Fatalf("bad flag: code %d", code)
	}
}

func TestImportAndVerifyOnly(t *testing.T) {
	out := filepath.Join(t.TempDir(), "fdc.sqlite")

	code, stdout, stderr := runCapture("import", "-src", "../../internal/importer/testdata/fixture",
		"-out", out, "-types", "sr_legacy,foundation")
	if code != exitOK {
		t.Fatalf("import failed: %d\n%s\n%s", code, stdout, stderr)
	}

	if !strings.Contains(stdout, "smoke test (iron, Spinach raw): ok") || !strings.Contains(stdout, "wrote "+out) {
		t.Fatalf("stdout:\n%s", stdout)
	}

	if !strings.Contains(stdout, "rows per file (read, kept):") || !strings.Contains(stdout, "food.csv") {
		t.Fatalf("the report must show rows read and kept per file:\n%s", stdout)
	}

	if !strings.Contains(stderr, "food.csv") {
		t.Fatalf("progress lines go to stderr:\n%s", stderr)
	}
}

func TestVerifyOnly(t *testing.T) {
	out := buildFixtureDB(t)

	code, stdout, _ := runCapture("import", "-verify-only", "-out", out)
	if code != exitOK || !strings.Contains(stdout, "rows per table") || strings.Contains(stdout, "rows per file") {
		t.Fatalf("verify-only reports tables, and has no files to report: %d\n%s", code, stdout)
	}

	code, _, stderr := runCapture("import", "-verify-only", "-out", filepath.Join(t.TempDir(), "missing.sqlite"))
	if code != exitFailure {
		t.Fatalf("verify-only on a missing file: code %d stderr %q", code, stderr)
	}
}

func TestImportFailureExitCode(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "food.csv"), []byte("\"wrong\"\n"), testFileMode); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCapture("import", "-src", src, "-out", filepath.Join(t.TempDir(), "x.sqlite"))
	if code != exitFailure || !strings.Contains(stderr, "fdc import:") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestServeFlagErrors(t *testing.T) {
	code, _, stderr := runCapture("serve", "-bogus")
	if code != exitUsage || !strings.Contains(stderr, "flag provided but not defined") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}

	code, _, stderr = runCapture("serve", "-log-file", filepath.Join(t.TempDir(), "fdc.log"), "-log-max-mb", "0")
	if code != exitFailure || !strings.Contains(stderr, "fdc serve: log file") ||
		!strings.Contains(stderr, "rotation needs") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestServeMissingDatabaseHintsAtImport(t *testing.T) {
	code, _, stderr := runCapture("serve", "-db", filepath.Join(t.TempDir(), "none.sqlite"), "-listen", "127.0.0.1:0")
	if code != exitFailure || !strings.Contains(stderr, "fdc serve: database could not be opened") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}

	if !strings.Contains(stderr, "run `make import` first") {
		t.Fatalf("hint missing: %q", stderr)
	}
}

func TestServeLogFileUnwritable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), testFileMode); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCapture("serve", "-log-file", filepath.Join(blocker, "fdc.log"))
	if code != exitFailure || !strings.Contains(stderr, "fdc serve: log file") ||
		!strings.Contains(stderr, "create log directory") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

// buildFixtureDB imports the fixture into a temp directory for the serve tests.
func buildFixtureDB(t *testing.T) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "fdc.sqlite")

	code, stdout, stderr := runCapture("import", "-src", "../../internal/importer/testdata/fixture", "-out", out)
	if code != exitOK {
		t.Fatalf("import failed: %d\n%s\n%s", code, stdout, stderr)
	}

	return out
}

func TestServeAddressInUseHint(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	code, _, stderr := runCapture("serve", "-db", buildFixtureDB(t), "-listen", listener.Addr().String())
	if code != exitFailure || !strings.Contains(stderr, "another fdc is probably running") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}

	if !strings.Contains(stderr, "fdc serve: ") {
		t.Fatalf("fatal errors go to stderr with the command prefix: %q", stderr)
	}
}

// waitForLog polls the log file until it holds want or the deadline passes.
func waitForLog(t *testing.T, path, want string) string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(path); err == nil && strings.Contains(string(content), want) {
			return string(content)
		}

		time.Sleep(20 * time.Millisecond)
	}

	content, _ := os.ReadFile(path)
	t.Fatalf("log never contained %q:\n%s", want, content)

	return ""
}

// listenAddress digs the address out of the JSON serving record.
func listenAddress(t *testing.T, logContent string) string {
	t.Helper()

	const marker = `"listen":"`

	start := strings.Index(logContent, marker)
	if start < 0 {
		t.Fatalf("no listen address in log:\n%s", logContent)
	}

	rest := logContent[start+len(marker):]

	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("unterminated listen address in log:\n%s", logContent)
	}

	return rest[:end]
}

// getAbout fetches /v1/about from a running serve and returns the status and
// the headers.
func getAbout(t *testing.T, address string) (int, http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+address+"/v1/about", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	return resp.StatusCode, resp.Header
}

// stopServe sends the process SIGINT and waits for the serve goroutine to
// return its exit code.
func stopServe(t *testing.T, done <-chan int, stderr *bytes.Buffer) {
	t.Helper()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("serve exited %d: %s", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop on SIGINT")
	}
}

func TestServeLogsRequestsAndStopsOnSignal(t *testing.T) {
	db := buildFixtureDB(t)
	logPath := filepath.Join(t.TempDir(), "logs", "fdc.log")

	done := make(chan int, 1)

	var stderr bytes.Buffer

	go func() {
		done <- run([]string{"serve", "-db", db, "-listen", "127.0.0.1:0", "-log-file", logPath, "-log-json"},
			io.Discard, &stderr)
	}()

	address := listenAddress(t, waitForLog(t, logPath, `"msg":"serving"`))

	status, header := getAbout(t, address)
	if status != http.StatusOK || header.Get("X-Data-Attribution") == "" {
		t.Fatalf("about: status %d headers %v", status, header)
	}

	waitForLog(t, logPath, `"path":"/v1/about"`)
	stopServe(t, done, &stderr)

	content := waitForLog(t, logPath, `"msg":"stopped"`)
	for _, want := range []string{`"msg":"request"`, `"status":200`, `"method":"GET"`} {
		if !strings.Contains(content, want) {
			t.Errorf("log lacks %s:\n%s", want, content)
		}
	}

	if stderr.Len() != 0 {
		t.Fatalf("with a log file nothing goes to stderr: %q", stderr.String())
	}
}

func TestListenHost(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8471": "127.0.0.1", "[::1]:8471": "::1", "192.168.1.5:0": "192.168.1.5", "nonsense": "nonsense",
		":8471": "",
	}
	for listen, want := range cases {
		if got := listenHost(listen); got != want {
			t.Errorf("listenHost(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestHTTPServerErrorLogUsesTheLogger(t *testing.T) {
	var buf bytes.Buffer

	httpServer := newHTTPServer(http.NotFoundHandler(), logging.New(&buf, logging.Options{}))
	httpServer.ErrorLog.Print("http: panic serving")

	if !strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "panic serving") {
		t.Fatalf("net/http errors must land in the logger:\n%s", buf.String())
	}
}

func TestHomeWarningOnlyWithoutExplicitDB(t *testing.T) {
	t.Setenv("HOME", "")
	// The default path must not resolve to a real database, or the second
	// run would serve forever.
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	code, _, stderr := runCapture("serve", "-db", filepath.Join(t.TempDir(), "none.sqlite"))
	if code != exitFailure || strings.Contains(stderr, "home directory unavailable") {
		t.Fatalf("explicit -db must not warn about HOME: code %d stderr %q", code, stderr)
	}

	code, _, stderr = runCapture("serve")
	if code != exitFailure || !strings.Contains(stderr, "home directory unavailable") {
		t.Fatalf("the default -db path depends on HOME, so it must warn: code %d stderr %q", code, stderr)
	}
}
