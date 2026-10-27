package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
)

// FoodSummary is one search hit. DataType is the USDA value.
type FoodSummary struct {
	FDCID       int64
	Description string
	DataType    string
	Category    *string
	Brand       *Brand
}

// Brand is present for branded foods only.
type Brand struct {
	Owner   *string
	Name    *string
	GTINUPC *string
}

// NutrientAmount is one nutrient value of one food, per 100 g.
type NutrientAmount struct {
	NutrientID int64
	Name       string
	Unit       string
	Amount     *float64
}

// FoodDetail is a food with every nutrient it has.
type FoodDetail struct {
	FoodSummary
	Nutrients []NutrientAmount
}

// Portion is one food_portion row joined to its measure unit.
type Portion struct {
	ID          int64
	SeqNum      *int64
	Amount      *float64
	UnitName    *string
	Description *string
	Modifier    *string
	GramWeight  *float64
}

// BrandedServing is the label serving of a branded food. Branded foods have no
// food_portion rows, so this is the only portion information they carry.
type BrandedServing struct {
	ServingSize *float64
	Unit        *string
	Household   *string
}

// NutrientInfo is the name and unit of a nutrient.
type NutrientInfo struct {
	ID   int64
	Name string
	Unit string
}

const summaryColumns = `f.fdc_id, f.description, f.data_type, f.category_name, b.brand_owner, b.brand_name, b.gtin_upc`

func scanSummary(rows *sql.Rows) (FoodSummary, error) {
	var (
		summary           FoodSummary
		owner, name, gtin *string
	)

	err := rows.Scan(&summary.FDCID, &summary.Description, &summary.DataType, &summary.Category, &owner, &name, &gtin)
	if err != nil {
		return summary, fmt.Errorf("scan food: %w", err)
	}

	if summary.DataType == datatypes.Branded {
		summary.Brand = &Brand{Owner: owner, Name: name, GTINUPC: gtin}
	}

	return summary, nil
}

// Placeholders returns count comma separated SQL placeholders, for IN lists.
func Placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func typeFilter(types []string) (string, []any) {
	if len(types) == 0 {
		return "", nil
	}

	args := make([]any, len(types))
	for index, dataType := range types {
		args[index] = dataType
	}

	return " AND f.data_type IN (" + Placeholders(len(types)) + ")", args
}

// Search runs a full text search, with an exact GTIN lookup first when the
// text looks like a barcode. types holds USDA data_type values. Results are
// capped at limit.
func Search(ctx context.Context, database *sql.DB, text string, types []string, limit int) ([]FoodSummary, error) {
	var hits []FoodSummary

	if IsGTIN(text) {
		var err error

		hits, err = searchGTIN(ctx, database, text, types, limit)
		if err != nil {
			return nil, err
		}

		if len(hits) >= limit {
			return hits[:limit], nil
		}
	}

	match, ok := BuildMatch(text)
	if !ok {
		return hits, nil
	}

	seen := make(map[int64]bool, len(hits))
	for _, hit := range hits {
		seen[hit.FDCID] = true
	}

	more, err := searchFTS(ctx, database, match, types, limit)
	if err != nil {
		return nil, err
	}

	for _, hit := range more {
		if seen[hit.FDCID] {
			continue
		}

		hits = append(hits, hit)
		if len(hits) >= limit {
			break
		}
	}

	return hits, nil
}

// searchGTIN finds branded foods by barcode, newest publication first.
func searchGTIN(ctx context.Context, database *sql.DB, text string, types []string, limit int) ([]FoodSummary, error) {
	filter, filterArgs := typeFilter(types)
	variants := GTINVariants(text)

	args := make([]any, 0, len(variants)+len(filterArgs)+1)
	for _, variant := range variants {
		args = append(args, variant)
	}

	args = append(args, filterArgs...)
	args = append(args, limit)

	// Only placeholder markers are concatenated. Every value is bound.
	query := `SELECT ` + summaryColumns +
		` FROM branded_food b JOIN food f ON f.fdc_id = b.fdc_id` +
		` WHERE b.gtin_upc IN (` + Placeholders(len(variants)) + `)` + filter +
		` ORDER BY f.publication_date DESC, f.fdc_id LIMIT ?`

	return querySummaries(ctx, database, "gtin search", query, args...)
}

// searchFTS runs the MATCH query. Generic USDA foods rank above branded
// products, because the picker is choosing the food an alias means and
// "Spinach, raw" should come before "SPINACH DIP". Within a group, description
// carries the most weight, brand fields a little, and the barcode column almost
// none since it is only there for the exact lookup in searchGTIN.
func searchFTS(ctx context.Context, database *sql.DB, match string, types []string, limit int) ([]FoodSummary, error) {
	filter, filterArgs := typeFilter(types)

	args := append([]any{match}, filterArgs...)
	args = append(args, limit)

	// Only placeholder markers are concatenated. Every value is bound.
	query := `SELECT ` + summaryColumns +
		` FROM food_fts JOIN food f ON f.fdc_id = food_fts.rowid` +
		` LEFT JOIN branded_food b ON b.fdc_id = f.fdc_id WHERE food_fts MATCH ?` + filter +
		` ORDER BY f.data_type = '` + datatypes.Branded + `', bm25(food_fts, 10.0, 2.0, 2.0, 0.1), f.description LIMIT ?`

	return querySummaries(ctx, database, "fts search", query, args...)
}

func querySummaries(ctx context.Context, database *sql.DB, what, query string, args ...any) ([]FoodSummary, error) {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = rows.Close() }()

	var hits []FoodSummary

	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}

		hits = append(hits, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}

	return hits, nil
}

// GetFood returns one food with every nutrient value it has. When USDA ships
// the same (fdc_id, nutrient_id) twice, the row with the lowest id wins.
func GetFood(ctx context.Context, database *sql.DB, fdcID int64) (*FoodDetail, error) {
	hits, err := querySummaries(ctx, database, "get food", `SELECT `+summaryColumns+
		` FROM food f LEFT JOIN branded_food b ON b.fdc_id = f.fdc_id WHERE f.fdc_id = ?`, fdcID)
	if err != nil {
		return nil, err
	}

	if len(hits) == 0 {
		return nil, ErrNotFound
	}

	detail := &FoodDetail{FoodSummary: hits[0]}

	rows, err := database.QueryContext(ctx, `
		SELECT fn.nutrient_id, n.name, n.unit_name, fn.amount
		FROM food_nutrient fn JOIN nutrient n ON n.id = fn.nutrient_id
		WHERE fn.fdc_id = ?
		  AND fn.id IN (SELECT MIN(id) FROM food_nutrient WHERE fdc_id = ? GROUP BY nutrient_id)
		ORDER BY n.rank, n.id`, fdcID, fdcID)
	if err != nil {
		return nil, fmt.Errorf("get food nutrients: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var amount NutrientAmount
		if err := rows.Scan(&amount.NutrientID, &amount.Name, &amount.Unit, &amount.Amount); err != nil {
			return nil, fmt.Errorf("scan nutrient: %w", err)
		}

		detail.Nutrients = append(detail.Nutrients, amount)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get food nutrients: %w", err)
	}

	return detail, nil
}

// GetPortions returns the food's data type, its food_portion rows in seq_num
// order, and the branded label serving when the food is branded. Measure unit
// 9999 is USDA's "undetermined" and comes back as no unit at all.
func GetPortions(ctx context.Context, database *sql.DB, fdcID int64) (string, []Portion, *BrandedServing, error) {
	var dataType string

	err := database.QueryRowContext(ctx, `SELECT data_type FROM food WHERE fdc_id = ?`, fdcID).Scan(&dataType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil, ErrNotFound
	}

	if err != nil {
		return "", nil, nil, fmt.Errorf("get food type: %w", err)
	}

	portions, err := queryPortions(ctx, database, fdcID)
	if err != nil {
		return "", nil, nil, err
	}

	var serving *BrandedServing

	if dataType == datatypes.Branded {
		serving = &BrandedServing{}

		err := database.QueryRowContext(ctx, `SELECT serving_size, serving_size_unit, household_serving_fulltext
			FROM branded_food WHERE fdc_id = ?`, fdcID).Scan(&serving.ServingSize, &serving.Unit, &serving.Household)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", nil, nil, fmt.Errorf("get branded serving: %w", err)
		}
	}

	return dataType, portions, serving, nil
}

func queryPortions(ctx context.Context, database *sql.DB, fdcID int64) ([]Portion, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT p.id, p.seq_num, p.amount,
		       CASE WHEN p.measure_unit_id = ? THEN NULL ELSE m.name END,
		       p.portion_description, p.modifier, p.gram_weight
		FROM food_portion p LEFT JOIN measure_unit m ON m.id = p.measure_unit_id
		WHERE p.fdc_id = ?
		ORDER BY p.seq_num, p.id`, UndeterminedMeasureUnit, fdcID)
	if err != nil {
		return nil, fmt.Errorf("get portions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var portions []Portion

	for rows.Next() {
		var portion Portion

		err := rows.Scan(&portion.ID, &portion.SeqNum, &portion.Amount, &portion.UnitName,
			&portion.Description, &portion.Modifier, &portion.GramWeight)
		if err != nil {
			return nil, fmt.Errorf("scan portion: %w", err)
		}

		portions = append(portions, portion)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get portions: %w", err)
	}

	return portions, nil
}

// MissingFoods returns the ids from the list that are not in the food table.
func MissingFoods(ctx context.Context, database *sql.DB, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, len(ids))
	for index, fdcID := range ids {
		args[index] = fdcID
	}

	// Only placeholder markers are concatenated. Every value is bound.
	query := `SELECT fdc_id FROM food WHERE fdc_id IN (` + Placeholders(len(ids)) + `)` // #nosec G202 -- bound values

	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("check foods: %w", err)
	}
	defer func() { _ = rows.Close() }()

	found := map[int64]bool{}

	for rows.Next() {
		var fdcID int64
		if err := rows.Scan(&fdcID); err != nil {
			return nil, fmt.Errorf("scan fdc_id: %w", err)
		}

		found[fdcID] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("check foods: %w", err)
	}

	var missing []int64

	for _, fdcID := range ids {
		if !found[fdcID] {
			missing = append(missing, fdcID)
		}
	}

	return missing, nil
}

// AmountTable maps fdc_id to nutrient_id to amount per 100 g.
type AmountTable = map[int64]map[int64]float64

// NutrientAmounts returns amount per 100 g for the given foods and nutrients,
// as amounts[fdc_id][nutrient_id]. Duplicate pairs resolve to the lowest id,
// the row GetFood reports, even when that row's amount is null. A null amount
// is left out, so the food counts as missing the nutrient on both paths.
func NutrientAmounts(ctx context.Context, database *sql.DB, fdcIDs, nutrientIDs []int64) (AmountTable, error) {
	out := AmountTable{}
	if len(fdcIDs) == 0 || len(nutrientIDs) == 0 {
		return out, nil
	}

	// Only placeholder markers are concatenated. Every value is bound.
	query := `SELECT fdc_id, nutrient_id, amount FROM food_nutrient WHERE fdc_id IN (` + // #nosec G202 -- bound values
		Placeholders(len(fdcIDs)) + `) AND nutrient_id IN (` + Placeholders(len(nutrientIDs)) + `)` +
		` ORDER BY fdc_id, nutrient_id, id`

	rows, err := database.QueryContext(ctx, query, bindIDs(fdcIDs, nutrientIDs)...)
	if err != nil {
		return nil, fmt.Errorf("nutrient amounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	seen := map[nutrientPair]bool{}

	for rows.Next() {
		var (
			pair   nutrientPair
			amount sql.NullFloat64
		)

		if err := rows.Scan(&pair.fdcID, &pair.nutrientID, &amount); err != nil {
			return nil, fmt.Errorf("scan nutrient amount: %w", err)
		}

		keepFirst(out, seen, pair, amount)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nutrient amounts: %w", err)
	}

	return out, nil
}

// bindIDs flattens two id lists into one argument list, foods first.
func bindIDs(fdcIDs, nutrientIDs []int64) []any {
	args := make([]any, 0, len(fdcIDs)+len(nutrientIDs))
	for _, fdcID := range fdcIDs {
		args = append(args, fdcID)
	}

	for _, nutrientID := range nutrientIDs {
		args = append(args, nutrientID)
	}

	return args
}

// nutrientPair names one (fdc_id, nutrient_id) cell of the amount table.
type nutrientPair struct {
	fdcID      int64
	nutrientID int64
}

// keepFirst records the amount of a pair's first row and ignores the rest.
// Rows arrive ordered by id, so the first row is the lowest id, the same row
// GetFood reports. A null first row still claims the pair. A later duplicate
// must not fill it in, or a score would count a value the detail shows as
// null.
func keepFirst(out AmountTable, seen map[nutrientPair]bool, pair nutrientPair, amount sql.NullFloat64) {
	if seen[pair] {
		return
	}

	seen[pair] = true

	if !amount.Valid {
		return
	}

	byNutrient := out[pair.fdcID]
	if byNutrient == nil {
		byNutrient = map[int64]float64{}
		out[pair.fdcID] = byNutrient
	}

	byNutrient[pair.nutrientID] = amount.Float64
}

// Nutrients returns name and unit for the given nutrient ids.
func Nutrients(ctx context.Context, database *sql.DB, ids []int64) (map[int64]NutrientInfo, error) {
	out := map[int64]NutrientInfo{}
	if len(ids) == 0 {
		return out, nil
	}

	args := make([]any, len(ids))
	for index, nutrientID := range ids {
		args[index] = nutrientID
	}

	// Only placeholder markers are concatenated. Every value is bound.
	query := `SELECT id, name, unit_name FROM nutrient WHERE id IN (` + // #nosec G202 -- bound values
		Placeholders(len(ids)) + `)`

	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("nutrients: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var info NutrientInfo
		if err := rows.Scan(&info.ID, &info.Name, &info.Unit); err != nil {
			return nil, fmt.Errorf("scan nutrient: %w", err)
		}

		out[info.ID] = info
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nutrients: %w", err)
	}

	return out, nil
}

// Meta returns every row of the meta table.
func Meta(ctx context.Context, database *sql.DB) (map[string]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT key, value FROM meta`)
	if err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan meta: %w", err)
		}

		out[key] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}

	return out, nil
}

// FoodCountByType returns the number of foods per USDA data_type.
func FoodCountByType(ctx context.Context, database *sql.DB) (map[string]int64, error) {
	rows, err := database.QueryContext(ctx, `SELECT data_type, COUNT(*) FROM food GROUP BY data_type`)
	if err != nil {
		return nil, fmt.Errorf("count foods: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int64{}

	for rows.Next() {
		var (
			dataType string
			count    int64
		)

		if err := rows.Scan(&dataType, &count); err != nil {
			return nil, fmt.Errorf("scan food count: %w", err)
		}

		out[dataType] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count foods: %w", err)
	}

	return out, nil
}

// TableCounts returns the row count of every table in Tables.
func TableCounts(ctx context.Context, database *sql.DB) (map[string]int64, error) {
	out := map[string]int64{}

	for _, table := range Tables() {
		var count int64

		// Table names come from the constant list above, never from input.
		query := `SELECT COUNT(*) FROM ` + table
		if err := database.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}

		out[table] = count
	}

	return out, nil
}

// DuplicateNutrientPairs counts (fdc_id, nutrient_id) pairs that appear more
// than once in food_nutrient.
func DuplicateNutrientPairs(ctx context.Context, database *sql.DB) (int64, error) {
	var count int64

	err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT fdc_id, nutrient_id FROM food_nutrient GROUP BY fdc_id, nutrient_id HAVING COUNT(*) > 1)`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("duplicate pairs: %w", err)
	}

	return count, nil
}
