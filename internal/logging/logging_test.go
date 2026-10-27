package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/logging"
)

func TestNewTextInfoLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := logging.New(&buf, logging.Options{})
	logger.Debug("hidden")
	logger.Info("shown", "path", "/v1/about")

	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Fatalf("debug must be hidden at the default level:\n%s", out)
	}

	for _, want := range []string{"level=INFO", "msg=shown", "path=/v1/about"} {
		if !strings.Contains(out, want) {
			t.Errorf("text record lacks %s:\n%s", want, out)
		}
	}
}

func TestNewVerboseShowsDebug(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logging.New(&buf, logging.Options{Verbose: true}).Debug("now shown")

	if !strings.Contains(buf.String(), "now shown") {
		t.Fatalf("verbose must enable debug:\n%s", buf.String())
	}
}

func TestNewJSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logging.New(&buf, logging.Options{JSON: true}).Warn("probe", "status", 503)

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("not one JSON object per line: %v\n%s", err, buf.String())
	}

	if record["level"] != "WARN" || record["msg"] != "probe" || record["status"] != float64(503) {
		t.Fatalf("json record is wrong: %v", record)
	}
}

func TestSetupInstallsDefault(t *testing.T) {
	var buf bytes.Buffer

	previous := slog.Default()

	t.Cleanup(func() { slog.SetDefault(previous) })

	logging.Setup(&buf, logging.Options{})
	slog.Info("through the default")

	if !strings.Contains(buf.String(), "through the default") {
		t.Fatalf("Setup did not install the default logger:\n%s", buf.String())
	}
}
