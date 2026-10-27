package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/importer"
	"github.com/samuel-stidham/grocery-list-core/internal/meta"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

//nolint:gochecknoglobals // one fixture database shared by every query test, built in TestMain
var (
	db     *sql.DB
	dbPath string
)

// TestMain imports the shared fixture once for every query test.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "store-test")
	if err != nil {
		panic(err)
	}

	dbPath = filepath.Join(dir, "fdc.sqlite")

	_, err = importer.Run(context.Background(), importer.Options{Src: "../importer/testdata/fixture", Out: dbPath})
	if err != nil {
		panic(err)
	}

	db, err = store.OpenReadOnly(context.Background(), dbPath)
	if err != nil {
		panic(err)
	}

	code := m.Run()

	_ = db.Close()
	_ = os.RemoveAll(dir)

	os.Exit(code)
}

// check compares two scalar values and reports a mismatch without stopping.
func check(t *testing.T, name string, got, want any) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v (%T), want %v (%T)", name, got, got, want, want)
	}
}

// deref turns a nullable string into a comparable value: the text or nil.
func deref(value *string) any {
	if value == nil {
		return nil
	}

	return *value
}

func ids(hits []store.FoodSummary) []int64 {
	out := make([]int64, len(hits))
	for index, hit := range hits {
		out[index] = hit.FDCID
	}

	return out
}

func TestDSNEscapesURIDelimiters(t *testing.T) {
	cases := map[string]string{
		"/plain/fdc.sqlite":      "file:/plain/fdc.sqlite?mode=ro",
		"/odd?dir#1/100%.sqlite": "file:/odd%3Fdir%231/100%25.sqlite?mode=ro",
	}
	for path, want := range cases {
		if got := store.DSN(path, "mode=ro"); got != want {
			t.Errorf("DSN(%q) = %q, want %q", path, got, want)
		}
	}

	if got := store.DSN("x.sqlite", ""); got != "file:x.sqlite" {
		t.Errorf("DSN without query = %q", got)
	}
}

func TestOpenReadOnlyMissingFileIsErrOpen(t *testing.T) {
	_, err := store.OpenReadOnly(context.Background(), filepath.Join(t.TempDir(), "none.sqlite"))
	if !errors.Is(err, store.ErrOpen) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenReadOnlyPathWithDelimiters(t *testing.T) {
	odd := filepath.Join(t.TempDir(), "odd?dir#1")
	if err := os.MkdirAll(odd, 0o750); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(odd, "100%.sqlite")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}

	database, err := store.OpenReadOnly(context.Background(), target)
	if err != nil {
		t.Fatalf("a path with URI delimiters must open: %v", err)
	}

	defer func() { _ = database.Close() }()

	if _, err := store.Meta(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}

func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	_, err := db.ExecContext(context.Background(), `INSERT INTO meta(key, value) VALUES ('x', 'y')`)
	if err == nil {
		t.Fatal("write succeeded on a read-only database")
	}
}

func TestSearchPrefixAndRanking(t *testing.T) {
	hits, err := store.Search(context.Background(), db, "spin", nil, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) != 1 || hits[0].FDCID != 168462 || hits[0].Brand != nil {
		t.Fatalf("got %+v", hits)
	}

	if hits[0].Category == nil || *hits[0].Category != "Vegetables and Vegetable Products" {
		t.Fatalf("category = %v", hits[0].Category)
	}
}

func TestSearchDiacriticsAndOperators(t *testing.T) {
	for _, query := range []string{"creme", "Crème", "creme fraiche", "fraiche*", `"fraiche"`} {
		hits, err := store.Search(context.Background(), db, query, nil, 10)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}

		if len(hits) != 1 || hits[0].FDCID != 9000004 {
			t.Fatalf("%q: got %v", query, ids(hits))
		}
	}

	// Operators are literal words. "AND" is not in any description, so a
	// query that includes it as a word finds nothing instead of erroring.
	hits, err := store.Search(context.Background(), db, "creme AND fraiche", nil, 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("literal AND: got %v, %v", ids(hits), err)
	}
}

func TestSearchTypeFilterAndLimit(t *testing.T) {
	ctx := context.Background()

	// "t" prefixes Testfood (foundation) and TESTBRAND (branded).
	all, err := store.Search(ctx, db, "t", nil, 100)
	if err != nil {
		t.Fatal(err)
	}

	if len(all) != 2 {
		t.Fatalf("expected two hits for the t prefix, got %v", ids(all))
	}

	if all[0].DataType == datatypes.Branded {
		t.Fatalf("generic foods must rank above branded ones, got %v", ids(all))
	}

	branded, err := store.Search(ctx, db, "t", []string{datatypes.Branded}, 100)
	if err != nil {
		t.Fatal(err)
	}

	if len(branded) != 1 || branded[0].DataType != datatypes.Branded || branded[0].Brand == nil {
		t.Fatalf("type filter: %+v", branded)
	}

	one, err := store.Search(ctx, db, "t", nil, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(one) != 1 {
		t.Fatalf("limit ignored: %v", ids(one))
	}
}

func TestSearchGTIN(t *testing.T) {
	ctx := context.Background()

	for _, query := range []string{"00012345678905", "12345678905", "012345678905"} {
		hits, err := store.Search(ctx, db, query, nil, 10)
		if err != nil {
			t.Fatal(err)
		}

		if len(hits) != 1 || hits[0].FDCID != 1105906 {
			t.Fatalf("%q: got %v", query, ids(hits))
		}

		if hits[0].Brand == nil || *hits[0].Brand.GTINUPC != "00012345678905" {
			t.Fatalf("%q: brand %+v", query, hits[0].Brand)
		}
	}

	hits, err := store.Search(ctx, db, "99999999", nil, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) != 0 {
		t.Fatalf("unknown barcode returned %v", ids(hits))
	}
}

func TestSearchNoTokens(t *testing.T) {
	hits, err := store.Search(context.Background(), db, "!!!", nil, 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("got %v, %v", hits, err)
	}
}

func TestGetFoodResolvesDuplicatesToLowestID(t *testing.T) {
	food, err := store.GetFood(context.Background(), db, 168462)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "description", food.Description, "Spinach, raw")
	check(t, "data type", food.DataType, datatypes.SRLegacy)
	check(t, "distinct nutrients", len(food.Nutrients), 9)

	var calcium []store.NutrientAmount

	for _, nutrient := range food.Nutrients {
		if nutrient.NutrientID == 1087 {
			calcium = append(calcium, nutrient)
		}
	}

	if len(calcium) != 1 {
		t.Fatalf("calcium appears %d times", len(calcium))
	}

	if calcium[0].Amount == nil {
		t.Fatal("calcium amount is null")
	}

	check(t, "calcium from the lower id", *calcium[0].Amount, 99.0)
	check(t, "calcium name", calcium[0].Name, "Calcium, Ca")
	check(t, "calcium unit", calcium[0].Unit, "MG")
}

func TestGetFoodBrandedAndNotFound(t *testing.T) {
	ctx := context.Background()

	food, err := store.GetFood(ctx, db, 1105904)
	if err != nil {
		t.Fatal(err)
	}

	if food.Brand == nil || *food.Brand.Owner != "Richardson Oilseed Products (US) Limited" {
		t.Fatalf("brand %+v", food.Brand)
	}

	if _, err := store.GetFood(ctx, db, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetPortionsSRLegacy(t *testing.T) {
	dataType, rows, serving, err := store.GetPortions(context.Background(), db, 168462)
	if err != nil {
		t.Fatal(err)
	}

	if dataType != datatypes.SRLegacy || serving != nil || len(rows) != 4 {
		t.Fatalf("sr legacy: %s %d %v", dataType, len(rows), serving)
	}

	if rows[0].UnitName != nil {
		t.Fatalf("measure unit 9999 must come back as no unit, got %q", *rows[0].UnitName)
	}

	if rows[0].Modifier == nil || *rows[0].Modifier != "cup" || *rows[0].GramWeight != 30 {
		t.Fatalf("first spinach portion %+v", rows[0])
	}
}

func TestGetPortionsFoundation(t *testing.T) {
	dataType, rows, serving, err := store.GetPortions(context.Background(), db, 321358)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "data type", dataType, datatypes.Foundation)
	check(t, "rows", len(rows), 1)
	check(t, "serving", serving == nil, true)
	check(t, "unit", deref(rows[0].UnitName), "tablespoon")
}

func TestGetPortionsBranded(t *testing.T) {
	dataType, rows, serving, err := store.GetPortions(context.Background(), db, 1105904)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "data type", dataType, datatypes.Branded)
	check(t, "rows", len(rows), 0)

	if serving == nil || serving.ServingSize == nil {
		t.Fatalf("branded serving %+v", serving)
	}

	check(t, "serving size", *serving.ServingSize, 15.0)
	check(t, "unit", deref(serving.Unit), "ml")
	check(t, "household", deref(serving.Household), "1 Tbsp (15 ml)")

	// The store hands USDA's spelling through untouched. The API normalizes.
	_, _, pizza, err := store.GetPortions(context.Background(), db, 2456687)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "raw GRM unit", deref(pizza.Unit), "GRM")
	check(t, "pizza household", deref(pizza.Household), "1 Pizza (156g)")

	_, _, water, err := store.GetPortions(context.Background(), db, 2457171)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "raw MLT unit", deref(water.Unit), "MLT")
	check(t, "empty household is null", deref(water.Household), nil)
}

func TestOpenReadOnlyBoundsThePool(t *testing.T) {
	check(t, "max open connections", db.Stats().MaxOpenConnections, 4)
	check(t, "all connections opened at startup", db.Stats().OpenConnections, 4)
	check(t, "all connections idle at startup", db.Stats().Idle, 4)

	// Hold four connections at once, release them, and all four must stay
	// idle. With the default of two idle connections, two would be closed
	// and reopened on every burst.
	ctx := context.Background()
	held := make([]*sql.Conn, 0, 4)

	for range 4 {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}

		held = append(held, conn)
	}

	for _, conn := range held {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}

	check(t, "idle connections kept", db.Stats().Idle, 4)
}

// importTo builds a database from the fixture at path with the given types.
func importTo(t *testing.T, path string, types ...string) {
	t.Helper()

	options := importer.Options{Src: "../importer/testdata/fixture", Out: path, Types: types}
	if _, err := importer.Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
}

// typesThrough pins every connection of the pool and reads the meta types
// row through each one, so no connection can hide behind another.
func typesThrough(t *testing.T, database *sql.DB) []string {
	t.Helper()

	ctx := context.Background()
	held := make([]*sql.Conn, 0, 4)

	for range 4 {
		conn, err := database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}

		held = append(held, conn)
	}

	seen := make([]string, 0, len(held))

	for _, conn := range held {
		var types string
		if err := conn.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'types'`).Scan(&types); err != nil {
			t.Fatal(err)
		}

		seen = append(seen, types)

		_ = conn.Close()
	}

	return seen
}

// TestReplacedFileIsNotSeenByARunningPool renames a second database over the
// served one, the way make import does, and checks that every connection
// still answers from the file it opened. A lazy pool would open its later
// connections on the new inode and mix two releases.
func TestReplacedFileIsNotSeenByARunningPool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	served := filepath.Join(dir, "fdc.sqlite")
	importTo(t, served)

	database, err := store.OpenReadOnly(ctx, served)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = database.Close() }()

	before, err := store.Meta(ctx, database)
	if err != nil {
		t.Fatal(err)
	}

	// The replacement differs in types only, so its meta row is distinct.
	replacement := filepath.Join(dir, "next.sqlite")
	importTo(t, replacement, datatypes.SRLegacy)

	if err := os.Rename(replacement, served); err != nil {
		t.Fatal(err)
	}

	for index, types := range typesThrough(t, database) {
		check(t, fmt.Sprintf("connection %d still serves the opened release", index), types, before[meta.KeyTypes])
	}

	// A fresh open sees the replacement.
	fresh, err := store.OpenReadOnly(ctx, served)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = fresh.Close() }()

	after, err := store.Meta(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "a restart picks up the new file", after[meta.KeyTypes], datatypes.ToShort(datatypes.SRLegacy))
}

func TestSearchMergesGTINAndTextHitsOnce(t *testing.T) {
	// A full barcode is both an exact GTIN lookup and an FTS prefix match on
	// the indexed gtin_upc, so the same food arrives twice and must be kept
	// once.
	hits, err := store.Search(context.Background(), db, "00012345678905", nil, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) != 1 || hits[0].FDCID != 1105906 {
		t.Fatalf("want the soup once, got %v", ids(hits))
	}
}

func TestGetPortionsMissing(t *testing.T) {
	if _, _, _, err := store.GetPortions(context.Background(), db, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingFoods(t *testing.T) {
	missing, err := store.MissingFoods(context.Background(), db, []int64{168462, 1, 9000002, 321358})
	if err != nil {
		t.Fatal(err)
	}

	if len(missing) != 2 || missing[0] != 1 || missing[1] != 9000002 {
		t.Fatalf("missing = %v", missing)
	}

	none, err := store.MissingFoods(context.Background(), db, nil)
	if err != nil || none != nil {
		t.Fatalf("empty input returned %v, %v", none, err)
	}
}

func TestNutrientAmountsDedupAndNulls(t *testing.T) {
	foods := []int64{168462, 9000001}
	nutrients := []int64{1087, 1008, 2048, 1093}

	amounts, err := store.NutrientAmounts(context.Background(), db, foods, nutrients)
	if err != nil {
		t.Fatal(err)
	}

	if amounts[168462][1087] != 99 {
		t.Fatalf("calcium = %v, want lowest id row", amounts[168462][1087])
	}

	if _, present := amounts[9000001][1008]; present {
		t.Fatal("synthetic food has no 1008 row")
	}

	if amounts[9000001][2048] != 150 {
		t.Fatalf("2048 = %v", amounts[9000001][2048])
	}

	if _, present := amounts[9000001][1093]; present {
		t.Fatal("synthetic food has no sodium row")
	}
}

func TestNutrients(t *testing.T) {
	info, err := store.Nutrients(context.Background(), db, []int64{1089, 1008})
	if err != nil {
		t.Fatal(err)
	}

	check(t, "iron name", info[1089].Name, "Iron, Fe")
	check(t, "iron unit", info[1089].Unit, "MG")
	check(t, "energy unit", info[1008].Unit, "KCAL")
}

func TestMetaAndCounts(t *testing.T) {
	ctx := context.Background()

	metadata, err := store.Meta(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "data_version", metadata["data_version"], "2026-04-30")
	check(t, "attribution present", metadata["attribution"] != "", true)

	byType, err := store.FoodCountByType(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "sr legacy count", byType[datatypes.SRLegacy], int64(3))
	check(t, "branded count", byType[datatypes.Branded], int64(4))

	counts, err := store.TableCounts(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "food rows", counts["food"], int64(10))

	dups, err := store.DuplicateNutrientPairs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	check(t, "duplicate pairs", dups, int64(1))
}

func TestLibraryVersionIsAnEngineVersion(t *testing.T) {
	version := store.LibraryVersion()
	if !strings.HasPrefix(version, "3.") || strings.Count(version, ".") != 2 {
		t.Fatalf("LibraryVersion = %q, want an SQLite 3 version", version)
	}
}

// TestNullLowestRowClaimsADuplicatePair clears the lower row of the duplicate
// calcium pair. GetFood and NutrientAmounts must then agree that the food has
// no calcium value, instead of the score reading the higher row.
func TestNullLowestRowClaimsADuplicatePair(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "null-dup.sqlite")
	importTo(t, path)
	nullOutAmount(t, path, 1362463)

	database, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = database.Close() }()

	amounts, err := store.NutrientAmounts(ctx, database, []int64{168462}, []int64{1087, 1089})
	if err != nil {
		t.Fatal(err)
	}

	if value, present := amounts[168462][1087]; present {
		t.Fatalf("calcium = %v, want the null lowest row to claim the pair", value)
	}

	check(t, "iron is unaffected", amounts[168462][1089], 2.71)

	food, err := store.GetFood(ctx, database, 168462)
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, nutrient := range food.Nutrients {
		if nutrient.NutrientID != 1087 {
			continue
		}

		found = true

		if nutrient.Amount != nil {
			t.Fatalf("GetFood calcium = %v, want null", *nutrient.Amount)
		}
	}

	if !found {
		t.Fatal("GetFood lost the calcium row")
	}
}

// nullOutAmount clears the amount of one food_nutrient row in a finished
// database, standing in for an empty amount cell in a USDA release.
func nullOutAmount(t *testing.T, path string, id int64) {
	t.Helper()

	database, err := sql.Open("sqlite", store.DSN(path, ""))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = database.Close() }()

	_, err = database.ExecContext(context.Background(), `UPDATE food_nutrient SET amount = NULL WHERE id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
}
