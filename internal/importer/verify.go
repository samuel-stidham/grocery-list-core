package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// The smoke test: iron for "Spinach, raw" in SR Legacy. These are USDA's
// published values and they have not changed between releases.
const (
	smokeFDCID       = 168462
	smokeNutrientID  = 1089
	smokeNutrientNbr = "303"
	smokeAmount      = 2.71
)

// Report is what import and verify print.
type Report struct {
	Elapsed    time.Duration
	Read       map[string]int64 // rows read per CSV file
	Kept       map[string]int64 // rows written per CSV file
	Counts     map[string]int64 // rows per table
	FoodByType map[string]int64 // foods per short type name
	// SkippedEmptyID counts rows per CSV file dropped for an empty fdc_id.
	SkippedEmptyID map[string]int64
	// DuplicateNutrientPairs counts (fdc_id, nutrient_id) pairs USDA shipped
	// more than once. The API resolves them to the lowest id.
	DuplicateNutrientPairs int64
	Smoke                  SmokeResult
}

// SmokeResult is the outcome of the spinach iron check.
type SmokeResult struct {
	// Applicable is false when SR Legacy was not imported.
	Applicable  bool
	OK          bool
	Amount      float64
	NutrientNbr string
	Detail      string
}

// Verify opens a finished database read-only and gathers the report.
func Verify(ctx context.Context, path string) (*Report, error) {
	database, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	defer func() { _ = database.Close() }()

	report := &Report{}

	report.Counts, err = store.TableCounts(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	byType, err := store.FoodCountByType(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	report.FoodByType = map[string]int64{}
	for usda, count := range byType {
		report.FoodByType[datatypes.ToShort(usda)] = count
	}

	report.DuplicateNutrientPairs, err = store.DuplicateNutrientPairs(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	report.Smoke = smoke(ctx, database, report.FoodByType[datatypes.ShortSRLegacy] > 0)

	return report, nil
}

func smoke(ctx context.Context, database *sql.DB, applicable bool) SmokeResult {
	result := SmokeResult{Applicable: applicable}
	if !applicable {
		result.Detail = "skipped, SR Legacy not imported"

		return result
	}

	var nbr sql.NullString

	err := database.QueryRowContext(ctx, `SELECT nutrient_nbr FROM nutrient WHERE id = ?`, smokeNutrientID).Scan(&nbr)
	if err != nil {
		result.Detail = fmt.Sprintf("nutrient %d missing: %v", smokeNutrientID, err)

		return result
	}

	result.NutrientNbr = nbr.String

	err = database.QueryRowContext(ctx,
		`SELECT amount FROM food_nutrient WHERE fdc_id = ? AND nutrient_id = ? ORDER BY id LIMIT 1`,
		smokeFDCID, smokeNutrientID).Scan(&result.Amount)
	if errors.Is(err, sql.ErrNoRows) {
		result.Detail = fmt.Sprintf("no iron row for fdc_id %d (Spinach, raw)", smokeFDCID)

		return result
	}

	if err != nil {
		result.Detail = err.Error()

		return result
	}

	result.OK = result.Amount == smokeAmount && result.NutrientNbr == smokeNutrientNbr
	result.Detail = fmt.Sprintf("fdc_id %d nutrient %d (nbr %s) amount %g, want nbr %s amount %g",
		smokeFDCID, smokeNutrientID, result.NutrientNbr, result.Amount, smokeNutrientNbr, smokeAmount)

	return result
}

// Print writes the report in the order the tables were loaded. Write errors
// on the report writer are deliberately ignored, as with fmt.Println.
func (report *Report) Print(out io.Writer) {
	say := func(format string, args ...any) {
		_, _ = fmt.Fprintf(out, format, args...)
	}

	say("rows per table:\n")

	for _, table := range store.Tables() {
		say("  %-28s %12d\n", table, report.Counts[table])
	}

	if len(report.Read) > 0 {
		say("rows per file (read, kept):\n")

		for _, name := range sortedKeys(report.Read) {
			say("  %-28s %12d %12d\n", name, report.Read[name], report.Kept[name])
		}
	}

	say("foods per type:\n")

	for _, dataType := range sortedKeys(report.FoodByType) {
		say("  %-28s %12d\n", dataType, report.FoodByType[dataType])
	}

	for _, name := range sortedKeys(report.SkippedEmptyID) {
		say("skipped %d rows of %s with an empty fdc_id\n", report.SkippedEmptyID[name], name)
	}

	say("duplicate (fdc_id, nutrient_id) pairs: %d\n", report.DuplicateNutrientPairs)

	status := "FAIL"
	if report.Smoke.OK || !report.Smoke.Applicable {
		status = "ok"
	}

	say("smoke test (iron, Spinach raw): %s, %s\n", status, report.Smoke.Detail)

	if report.Elapsed > 0 {
		say("elapsed: %s\n", report.Elapsed.Round(time.Millisecond))
	}
}

func sortedKeys(counts map[string]int64) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}
