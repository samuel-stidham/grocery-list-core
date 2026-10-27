package importer_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/importer"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

const (
	fixture      = "testdata/fixture"
	testFileMode = 0o600
)

// copyFixture copies the fixture directory so a test can corrupt one file.
func copyFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}

	return dir
}

// corruptedFixture copies the fixture directory and swaps one literal in its
// food_nutrient.csv, so a test can break exactly one value.
func corruptedFixture(t *testing.T, old, replacement string) string {
	t.Helper()

	dir := copyFixture(t)
	path := filepath.Join(dir, "food_nutrient.csv")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), testFileMode); err != nil {
		t.Fatal(err)
	}

	return dir
}

// check compares two scalar values and reports a mismatch without stopping.
func check(t *testing.T, name string, got, want any) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v (%T), want %v (%T)", name, got, got, want, want)
	}
}

func runImport(t *testing.T, src string, types ...string) (string, *importer.Report) {
	t.Helper()

	out := filepath.Join(t.TempDir(), "fdc.sqlite")

	report, err := importer.Run(context.Background(), importer.Options{
		Src: src, Out: out, Types: types, BatchSize: 5,
		Now: func() time.Time { return time.Date(2026, 10, 27, 1, 2, 3, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	return out, report
}

func openRO(t *testing.T, path string) *sql.DB {
	t.Helper()

	database, err := store.OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	return database
}

func queryString(t *testing.T, database *sql.DB, query string, args ...any) string {
	t.Helper()

	var value sql.NullString
	if err := database.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}

	return value.String
}

func queryInt(t *testing.T, database *sql.DB, query string, args ...any) int64 {
	t.Helper()

	var count int64
	if err := database.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("%s: %v", query, err)
	}

	return count
}

func TestRunFullFixtureCounts(t *testing.T) {
	_, report := runImport(t, fixture)

	want := map[string]int64{
		"food": 10, "food_nutrient": 62, "food_portion": 9, "nutrient": 12, "measure_unit": 11,
		"food_category": 6, "wweia_food_category": 2, "food_nutrient_derivation": 4,
		"sr_legacy_food": 3, "foundation_food": 2, "survey_fndds_food": 1, "branded_food": 4, "meta": 7,
	}
	for table, count := range want {
		check(t, table+" rows", report.Counts[table], count)
	}

	check(t, "food.csv read", report.Read["food.csv"], int64(12))
	check(t, "food.csv kept", report.Kept["food.csv"], int64(10))

	check(t, "food_portion.csv read", report.Read["food_portion.csv"], int64(11))
	check(t, "food_portion.csv kept", report.Kept["food_portion.csv"], int64(9))
	check(t, "food_portion.csv skipped", report.SkippedEmptyID["food_portion.csv"], int64(1))

	check(t, "branded_food.csv read", report.Read["branded_food.csv"], int64(6))
	check(t, "branded_food.csv kept", report.Kept["branded_food.csv"], int64(4))
	check(t, "branded_food.csv skipped", report.SkippedEmptyID["branded_food.csv"], int64(1))
	check(t, "files with skipped rows", len(report.SkippedEmptyID), 2)

	check(t, "sr_legacy foods", report.FoodByType["sr_legacy"], int64(3))
	check(t, "branded foods", report.FoodByType["branded"], int64(4))
	check(t, "duplicate pairs", report.DuplicateNutrientPairs, int64(1))
	check(t, "smoke applicable", report.Smoke.Applicable, true)
	check(t, "smoke ok", report.Smoke.OK, true)
}

func TestRunFullFixtureDropsSamplingTypes(t *testing.T) {
	out, _ := runImport(t, fixture)
	database := openRO(t, out)

	for _, fdcID := range []int64{9000002, 9000005} {
		if count := queryInt(t, database, `SELECT COUNT(*) FROM food WHERE fdc_id = ?`, fdcID); count != 0 {
			t.Errorf("food %d should have been dropped", fdcID)
		}

		if count := queryInt(t, database, `SELECT COUNT(*) FROM food_nutrient WHERE fdc_id = ?`, fdcID); count != 0 {
			t.Errorf("nutrients of %d should have been dropped", fdcID)
		}
	}

	if count := queryInt(t, database, `SELECT COUNT(*) FROM food_portion WHERE fdc_id = 9000002`); count != 0 {
		t.Error("portion of a sub sample food should have been dropped")
	}

	if count := queryInt(t, database, `SELECT COUNT(*) FROM branded_food WHERE fdc_id = 9000002`); count != 0 {
		t.Error("branded row for a dropped food should have been dropped")
	}
}

func TestRunFullFixtureValues(t *testing.T) {
	out, _ := runImport(t, fixture)
	database := openRO(t, out)

	// The embedded newline survives intact.
	newline := queryString(t, database, `SELECT description FROM food WHERE fdc_id = 9000003`)
	check(t, "embedded newline", newline, "Bread, white,\ncommercially prepared")

	// category_name: three resolution cases plus the branded fallback.
	categories := map[int64]string{
		168462:  "Vegetables and Vegetable Products",
		321358:  "Legumes and Legume Products",
		2705383: "Human milk",
		1105904: "Oils Edible",
		1105906: "Prepared Soups",
	}
	for fdcID, want := range categories {
		got := queryString(t, database, `SELECT category_name FROM food WHERE fdc_id = ?`, fdcID)
		check(t, fmt.Sprintf("category_name(%d)", fdcID), got, want)
	}

	rawCategory := queryString(t, database, `SELECT food_category_id FROM food WHERE fdc_id = 1105904`)
	check(t, "raw food_category_id kept as text", rawCategory, "Oils Edible")

	// Nulls, numbers, and the NDB_number rename.
	pointsType := queryString(t, database, `SELECT typeof(data_points) FROM food_nutrient WHERE id = 2219788`)
	check(t, "empty data_points", pointsType, "null")

	amount := queryString(t, database, `SELECT typeof(amount) || ':' || amount FROM food_nutrient WHERE id = 1362465`)
	check(t, "amount", amount, "real:2.71")

	year := queryString(t, database, `SELECT min_year_acquired FROM food_nutrient WHERE id = 2219748`)
	check(t, "min_year_acquired", year, "2015")

	ndb := queryString(t, database, `SELECT ndb_number FROM sr_legacy_food WHERE fdc_id = 168462`)
	check(t, "ndb_number", ndb, "11457")

	servingType := queryString(t, database, `SELECT typeof(serving_size) FROM branded_food WHERE fdc_id = 1105906`)
	check(t, "serving_size type", servingType, "real")

	// FTS carries brand fields only for branded foods.
	brandHits := queryInt(t, database, `SELECT COUNT(*) FROM food_fts WHERE food_fts MATCH 'testbrand'`)
	check(t, "fts brand hits", brandHits, int64(1))

	diacriticHits := queryInt(t, database, `SELECT COUNT(*) FROM food_fts WHERE food_fts MATCH 'creme'`)
	check(t, "fts diacritics hits", diacriticHits, int64(1))

	// meta rows.
	metaCases := map[string]string{
		"types":        "sr_legacy,foundation,branded,survey",
		"imported_at":  "2026-10-27T01:02:03Z",
		"data_version": "2026-04-30",
	}
	for metaKey, want := range metaCases {
		check(t, "meta "+metaKey, queryString(t, database, `SELECT value FROM meta WHERE key = ?`, metaKey), want)
	}
}

func TestRunLeavesOneServingFile(t *testing.T) {
	out, _ := runImport(t, fixture)
	database := openRO(t, out)

	if got := queryString(t, database, `PRAGMA journal_mode`); got != "delete" {
		t.Errorf("journal_mode = %q, want delete", got)
	}

	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(out + suffix); err == nil {
			t.Errorf("%s%s should not exist", out, suffix)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(out))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("output dir holds %d entries, want only the database", len(entries))
	}
}

func TestRunTypeFilter(t *testing.T) {
	out, report := runImport(t, fixture, datatypes.Foundation)

	if report.Counts["food"] != 2 || report.Counts["sr_legacy_food"] != 0 || report.Counts["branded_food"] != 0 {
		t.Fatalf("counts: %v", report.Counts)
	}

	if report.Counts["food_nutrient"] != 17 {
		t.Errorf("food_nutrient = %d, want 17 (hummus 9 + synthetic 8)", report.Counts["food_nutrient"])
	}

	if report.Smoke.Applicable {
		t.Error("smoke test must be skipped without SR Legacy")
	}

	database := openRO(t, out)
	if got := queryString(t, database, `SELECT value FROM meta WHERE key = 'types'`); got != "foundation" {
		t.Errorf("meta types = %q", got)
	}
}

func TestRunRejectsRenamedHeaderBeforeTouchingOutput(t *testing.T) {
	src := corruptedFixture(t, `"amount"`, `"value"`)

	out := filepath.Join(t.TempDir(), "fdc.sqlite")
	if err := os.WriteFile(out, []byte("keep me"), testFileMode); err != nil {
		t.Fatal(err)
	}

	_, err := importer.Run(context.Background(), importer.Options{Src: src, Out: out})
	if !errors.Is(err, importer.ErrHeaderMismatch) ||
		!strings.Contains(err.Error(), `food_nutrient.csv: header column 4 is "value", want "amount"`) {
		t.Fatalf("got %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "keep me" {
		t.Fatal("existing output was touched by a failed import")
	}
}

func TestRunRequiresSideFileOnlyForSelectedTypes(t *testing.T) {
	src := copyFixture(t)
	if err := os.Remove(filepath.Join(src, "branded_food.csv")); err != nil {
		t.Fatal(err)
	}

	_, err := importer.Run(context.Background(), importer.Options{
		Src: src, Out: filepath.Join(t.TempDir(), "x.sqlite"), Types: []string{datatypes.SRLegacy},
	})
	if err != nil {
		t.Fatalf("branded file must not be required for an SR Legacy import: %v", err)
	}

	_, err = importer.Run(context.Background(), importer.Options{Src: src, Out: filepath.Join(t.TempDir(), "y.sqlite")})
	if err == nil {
		t.Fatal("a full import must fail when branded_food.csv is missing")
	}
}

func TestRunFailsOnBadNumberAndLeavesNoFiles(t *testing.T) {
	src := corruptedFixture(t, `"2.71"`, `"two point seven"`)

	dir := t.TempDir()
	out := filepath.Join(dir, "fdc.sqlite")

	_, err := importer.Run(context.Background(), importer.Options{Src: src, Out: out})
	if !errors.Is(err, importer.ErrBadValue) || !strings.Contains(err.Error(), "food_nutrient.csv row") ||
		!strings.Contains(err.Error(), "amount") {
		t.Fatalf("got %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Fatalf("failed import left files behind: %v", entries)
	}
}

func TestRunSmokeFailureRemovesOutput(t *testing.T) {
	src := corruptedFixture(t, `"2.71"`, `"2.70"`)

	dir := t.TempDir()
	out := filepath.Join(dir, "fdc.sqlite")

	report, err := importer.Run(context.Background(), importer.Options{Src: src, Out: out})
	if !errors.Is(err, importer.ErrSmokeTestFailed) {
		t.Fatalf("got %v", err)
	}

	if report.Read["food.csv"] != 12 || report.Kept["food.csv"] != 10 {
		t.Fatalf("a failing report must still carry the per file counters: %+v", report)
	}

	if report == nil || report.Smoke.OK {
		t.Fatal("report must carry the failed smoke result")
	}

	if _, err := os.Stat(out); err == nil {
		t.Fatal("output must not exist after a smoke failure")
	}
}

func TestRunReplacesExistingDatabase(t *testing.T) {
	out, _ := runImport(t, fixture, datatypes.Foundation)

	report, err := importer.Run(context.Background(), importer.Options{Src: fixture, Out: out})
	if err != nil {
		t.Fatal(err)
	}

	if report.Counts["food"] != 10 {
		t.Fatalf("second import did not replace the first: %v", report.Counts)
	}
}

func TestVerifyOnExistingDatabase(t *testing.T) {
	out, _ := runImport(t, fixture)

	report, err := importer.Verify(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}

	if report.Counts["food"] != 10 || !report.Smoke.OK || report.DuplicateNutrientPairs != 1 {
		t.Fatalf("verify: %+v", report)
	}

	report.SkippedEmptyID = map[string]int64{"food_portion.csv": 1}

	var buf strings.Builder

	report.Print(&buf)

	wants := []string{
		"food_nutrient", "62", "smoke test (iron, Spinach raw): ok",
		"duplicate (fdc_id, nutrient_id) pairs: 1", "skipped 1 rows of food_portion.csv with an empty fdc_id",
	}
	for _, want := range wants {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestValidateSourceMissingFile(t *testing.T) {
	src := copyFixture(t)
	if err := os.Remove(filepath.Join(src, "nutrient.csv")); err != nil {
		t.Fatal(err)
	}

	err := importer.ValidateSource(src, nil)
	if !errors.Is(err, importer.ErrMissingFile) || !strings.Contains(err.Error(), "nutrient.csv") {
		t.Fatalf("got %v", err)
	}
}

func TestRunKeepsExistingOutputWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fdc.sqlite")

	// A non-empty directory at the output path cannot be replaced by rename,
	// so the import fails at the last step. Whatever was there must survive.
	if err := os.MkdirAll(filepath.Join(out, "inner"), 0o750); err != nil {
		t.Fatal(err)
	}

	_, err := importer.Run(context.Background(), importer.Options{Src: fixture, Out: out})
	if err == nil || !strings.Contains(err.Error(), "rename into place") {
		t.Fatalf("got %v", err)
	}

	if _, err := os.Stat(filepath.Join(out, "inner")); err != nil {
		t.Fatalf("the existing output was disturbed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

// deadPID finds a pid no process holds, counting down from the Linux
// maximum, so a stale temp file can carry it.
func deadPID(t *testing.T) int {
	t.Helper()

	for pid := 4_194_000; pid > 1000; pid-- {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return pid
		}
	}

	t.Fatal("no free pid found")

	return 0
}

// touchFiles creates small files with the given names in dir.
func touchFiles(t *testing.T, dir string, names ...string) {
	t.Helper()

	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), testFileMode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunRemovesStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fdc.sqlite")
	dead := strconv.Itoa(deadPID(t))

	touchFiles(t, dir, "fdc.sqlite.tmp-"+dead, "fdc.sqlite.tmp-"+dead+"-wal", "fdc.sqlite.tmp-junk")

	// The test runner's pid is alive, so its temp file belongs to a running
	// import and must survive. So must a neighbour that only looks similar.
	live := "fdc.sqlite.tmp-" + strconv.Itoa(os.Getppid())
	touchFiles(t, dir, live, "fdc.sqlite.bak")

	if _, err := importer.Run(context.Background(), importer.Options{Src: fixture, Out: out}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	if len(names) != 3 || names[0] != "fdc.sqlite" || names[1] != "fdc.sqlite.bak" || names[2] != live {
		t.Fatalf("want the database, the neighbour, and the live import's temp file, got %v", names)
	}
}
