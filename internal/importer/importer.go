package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/meta"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// DefaultBatchSize is the number of rows per transaction.
const DefaultBatchSize = 50000

// outputDirMode is the permission of a directory the importer has to create.
const outputDirMode = 0o750

// Options configures one import.
type Options struct {
	// Src is the extracted archive directory holding the CSV files.
	Src string
	// Out is the database path. It is replaced atomically on success.
	Out string
	// Types holds the USDA data_type values to keep. Empty means all four.
	Types []string
	// BatchSize is rows per transaction. Zero means DefaultBatchSize.
	BatchSize int
	// Version is written to meta as importer_version.
	Version string
	// Logf receives one progress line per file. Nil silences progress.
	Logf func(format string, args ...any)
	// Now supplies the imported_at timestamp. Nil means time.Now.
	Now func() time.Time
}

func (opts *Options) defaults() {
	if len(opts.Types) == 0 {
		opts.Types = datatypes.AllUSDA()
	}

	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}

	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}

	if opts.Now == nil {
		opts.Now = time.Now
	}

	if opts.Version == "" {
		opts.Version = "dev"
	}
}

// ValidateSource checks that every file an import of the given types needs
// exists under src with exactly the expected header.
func ValidateSource(src string, types []string) error {
	if len(types) == 0 {
		types = datatypes.AllUSDA()
	}

	for _, name := range requiredFiles(types) {
		file, err := openCSV(src, name)
		if err != nil {
			return err
		}

		if err := file.Close(); err != nil {
			return err
		}
	}

	return nil
}

// Run performs the import and returns the verification report. On any error
// the output path is left exactly as it was.
func Run(ctx context.Context, opts Options) (*Report, error) {
	opts.defaults()

	start := time.Now()

	if err := ValidateSource(opts.Src, opts.Types); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(opts.Out), outputDirMode); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	removeStaleTemps(opts.Out)

	tmp := fmt.Sprintf("%s.tmp-%d", opts.Out, os.Getpid())
	removeDB(tmp)

	counts, err := buildDatabase(ctx, opts, tmp)
	if err != nil {
		removeDB(tmp)

		return nil, err
	}

	report, err := Verify(ctx, tmp)
	if err != nil {
		removeDB(tmp)

		return nil, err
	}

	// The per file counters go in before the smoke check, so a failing
	// report still shows what was read and kept. That is when it matters.
	report.Read, report.Kept, report.SkippedEmptyID = counts.read, counts.kept, counts.skipped

	if report.Smoke.Applicable && !report.Smoke.OK {
		removeDB(tmp)

		return report, fmt.Errorf("%w: %s", ErrSmokeTestFailed, report.Smoke.Detail)
	}

	// Only the sidecars of the old database are removed. The rename itself
	// replaces the main file atomically, and if it fails the old database is
	// still there, untouched.
	removeSidecars(opts.Out)

	if err := os.Rename(tmp, opts.Out); err != nil {
		removeDB(tmp)

		return nil, fmt.Errorf("rename into place: %w", err)
	}

	report.Elapsed = time.Since(start)

	return report, nil
}

// buildDatabase loads every table into the temp file, finalizes it, and
// returns the per-file counters for the report.
func buildDatabase(ctx context.Context, opts Options, tmp string) (fileCounts, error) {
	database, err := store.OpenForImport(ctx, tmp)
	if err != nil {
		return fileCounts{}, fmt.Errorf("open temp database: %w", err)
	}

	if _, err := database.ExecContext(ctx, store.Schema); err != nil {
		_ = database.Close()

		return fileCounts{}, fmt.Errorf("create schema: %w", err)
	}

	load := newLoader(database, opts)

	steps := []func(context.Context) error{
		load.loadLookups,
		load.loadFood,
		load.loadFoodNutrient,
		load.loadFoodPortion,
		load.loadSideTables,
		load.fillBrandedCategories,
		load.buildIndexes,
		load.buildFTS,
		load.writeMeta,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			_ = database.Close()

			return fileCounts{}, err
		}
	}

	if err := store.Finalize(ctx, database); err != nil {
		_ = database.Close()

		return fileCounts{}, fmt.Errorf("finalize: %w", err)
	}

	if err := database.Close(); err != nil {
		return fileCounts{}, fmt.Errorf("close: %w", err)
	}

	return load.counts, nil
}

// removeStaleTemps deletes temp files left by an import that was killed,
// which otherwise sit next to the database at 2.7 GB each. Every <out>.tmp-*
// goes, unless its name carries the pid of a live process, which is a
// concurrent import that keeps its file.
func removeStaleTemps(out string) {
	entries, err := os.ReadDir(filepath.Dir(out))
	if err != nil {
		return
	}

	prefix := filepath.Base(out) + ".tmp-"

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}

		if pid, ok := tempPID(strings.TrimPrefix(entry.Name(), prefix)); ok && processAlive(pid) {
			continue
		}

		_ = os.Remove(filepath.Join(filepath.Dir(out), entry.Name()))
	}
}

// tempPID reads the pid out of the tail of a temp file name, which is
// "<pid>" for the database and "<pid>-wal", "<pid>-shm" or "<pid>-journal"
// for a sidecar.
func tempPID(tail string) (int, bool) {
	digits, _, _ := strings.Cut(tail, "-")

	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 {
		return 0, false
	}

	return pid, true
}

// processAlive asks the kernel whether pid exists. Signal 0 delivers nothing.
// EPERM means the process exists under another user, which still counts.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)

	return err == nil || errors.Is(err, syscall.EPERM)
}

// removeSidecars deletes the journal sidecars of a database, leaving the
// database itself alone. Missing files are not errors.
func removeSidecars(path string) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
}

// removeDB deletes a database file and any journal sidecars. Missing files
// are not errors.
func removeDB(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
}

// fileCounts tracks rows per CSV file.
type fileCounts struct {
	read map[string]int64
	kept map[string]int64
	// skipped counts rows whose fdc_id is empty. USDA ships a few, and a row
	// that belongs to no food is dropped and reported rather than fatal.
	skipped map[string]int64
}

type loader struct {
	database *sql.DB
	opts     Options
	ids      *idSet
	keep     map[string]bool
	counts   fileCounts

	foodCategory  map[int64]string
	wweiaCategory map[int64]string
}

func newLoader(database *sql.DB, opts Options) *loader {
	load := &loader{
		database: database,
		opts:     opts,
		ids:      &idSet{},
		keep:     map[string]bool{},
		counts: fileCounts{
			read:    map[string]int64{},
			kept:    map[string]int64{},
			skipped: map[string]int64{},
		},
		foodCategory:  map[int64]string{},
		wweiaCategory: map[int64]string{},
	}
	for _, dataType := range opts.Types {
		load.keep[dataType] = true
	}

	return load
}

func (load *loader) log(name string, started time.Time) {
	line := fmt.Sprintf("%-28s read %10d  kept %10d", name, load.counts.read[name], load.counts.kept[name])
	if skipped := load.counts.skipped[name]; skipped > 0 {
		line += fmt.Sprintf("  skipped %d with empty fdc_id", skipped)
	}

	load.opts.Logf("%s  %s", line, time.Since(started).Round(time.Millisecond))
}

// childID parses the fdc_id of a row in a table that hangs off food. An empty
// id is counted and skipped. A non-empty id that is not a number is an error.
// The bool is true when the row belongs to a kept food.
func (load *loader) childID(file *csvFile, raw string) (int64, bool, error) {
	if raw == "" {
		load.counts.skipped[file.name]++

		return 0, false, nil
	}

	fdcID, err := file.mustInt(raw, colFDCID)
	if err != nil {
		return 0, false, err
	}

	return fdcID, load.ids.Has(fdcID), nil
}

// rowFunc turns one CSV record into insert arguments. A nil slice skips the
// row.
type rowFunc func(file *csvFile, record []string) ([]any, error)

// insert streams name through a batchWriter, feeding every record to row.
func (load *loader) insert(ctx context.Context, name, query string, row rowFunc) error {
	started := time.Now()

	file, err := openCSV(load.opts.Src, name)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	writer := newBatchWriter(load.database, query, load.opts.BatchSize)

	for {
		record, err := file.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			writer.Abort()

			return err
		}

		load.counts.read[name]++

		args, err := row(file, record)
		if err != nil {
			writer.Abort()

			return err
		}

		if args == nil {
			continue
		}

		if err := writer.Write(ctx, args...); err != nil {
			writer.Abort()

			return fmt.Errorf("%s row %d: %w", name, file.row, err)
		}

		load.counts.kept[name]++
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	load.log(name, started)

	return nil
}

func (load *loader) loadLookups(ctx context.Context) error {
	steps := []func(context.Context) error{
		load.loadFoodCategory,
		load.loadWweiaFoodCategory,
		load.loadMeasureUnit,
		load.loadNutrient,
		load.loadDerivation,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (load *loader) loadFoodCategory(ctx context.Context) error {
	return load.insert(ctx, fileFoodCategory, `INSERT INTO food_category(id, code, description) VALUES (?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			categoryID, err := file.mustInt(record[0], colID)
			if err != nil {
				return nil, err
			}

			load.foodCategory[categoryID] = record[2]

			return []any{categoryID, record[1], record[2]}, nil
		})
}

func (load *loader) loadWweiaFoodCategory(ctx context.Context) error {
	return load.insert(ctx, fileWweiaFoodCategory,
		`INSERT INTO wweia_food_category(wweia_food_category, wweia_food_category_description) VALUES (?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			code, err := file.mustInt(record[0], "wweia_food_category")
			if err != nil {
				return nil, err
			}

			load.wweiaCategory[code] = record[1]

			return []any{code, record[1]}, nil
		})
}

func (load *loader) loadMeasureUnit(ctx context.Context) error {
	return load.insert(ctx, fileMeasureUnit, `INSERT INTO measure_unit(id, name) VALUES (?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			unitID, err := file.mustInt(record[0], colID)
			if err != nil {
				return nil, err
			}

			return []any{unitID, record[1]}, nil
		})
}

func (load *loader) loadNutrient(ctx context.Context) error {
	return load.insert(ctx, fileNutrient,
		`INSERT INTO nutrient(id, name, unit_name, nutrient_nbr, rank) VALUES (?,?,?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			nutrientID, err := file.mustInt(record[0], colID)
			if err != nil {
				return nil, err
			}

			rank, err := file.nullFloat(record[4], "rank")
			if err != nil {
				return nil, err
			}

			return []any{nutrientID, record[1], record[2], text(record[3]), rank}, nil
		})
}

func (load *loader) loadDerivation(ctx context.Context) error {
	return load.insert(ctx, fileFoodNutrientDerivation,
		`INSERT INTO food_nutrient_derivation(id, code, description) VALUES (?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			derivationID, err := file.mustInt(record[0], colID)
			if err != nil {
				return nil, err
			}

			return []any{derivationID, record[1], record[2]}, nil
		})
}

// categoryName resolves the polymorphic food_category_id column. SR Legacy
// and Foundation foods carry a food_category id, Survey foods carry a WWEIA
// category code, and Branded foods carry the category name itself.
func (load *loader) categoryName(dataType, raw string) any {
	if raw == "" {
		return nil
	}

	switch dataType {
	case datatypes.SRLegacy, datatypes.Foundation:
		categoryID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil
		}

		return text(load.foodCategory[categoryID])
	case datatypes.Survey:
		code, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil
		}

		return text(load.wweiaCategory[code])
	case datatypes.Branded:
		return raw
	default:
		return nil
	}
}

func (load *loader) loadFood(ctx context.Context) error {
	return load.insert(ctx, fileFood,
		`INSERT INTO food(fdc_id, data_type, description, food_category_id, publication_date, category_name)
		 VALUES (?,?,?,?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			dataType := record[1]
			if !load.keep[dataType] {
				return nil, nil
			}

			fdcID, err := file.mustInt(record[0], colFDCID)
			if err != nil {
				return nil, err
			}

			load.ids.Add(fdcID)

			return []any{
				fdcID, dataType, record[2], text(record[3]), text(record[4]), load.categoryName(dataType, record[3]),
			}, nil
		})
}

func (load *loader) loadFoodNutrient(ctx context.Context) error {
	return load.insert(ctx, fileFoodNutrient,
		`INSERT INTO food_nutrient(id, fdc_id, nutrient_id, amount, data_points, derivation_id,
		 min, max, median, loq, footnote, min_year_acquired, percent_daily_value)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			fdcID, kept, err := load.childID(file, record[1])
			if err != nil || !kept {
				return nil, err
			}

			rowID, err := file.mustInt(record[0], colID)
			if err != nil {
				return nil, err
			}

			nutrientID, err := file.mustInt(record[2], "nutrient_id")
			if err != nil {
				return nil, err
			}

			amount, err := file.nullFloat(record[3], colAmount)
			if err != nil {
				return nil, err
			}

			points, err := file.nullInt(record[4], colDataPoints)
			if err != nil {
				return nil, err
			}

			derivation, err := file.nullInt(record[5], "derivation_id")
			if err != nil {
				return nil, err
			}

			tail, err := nutrientTail(file, record)
			if err != nil {
				return nil, err
			}

			return append([]any{rowID, fdcID, nutrientID, amount, points, derivation}, tail...), nil
		})
}

// nutrientTail parses columns 6 to 12 of food_nutrient.csv: the four
// statistics, the footnote, the year, and the percent daily value.
func nutrientTail(file *csvFile, record []string) ([]any, error) {
	const firstStatColumn = 6

	// footnote, min_year_acquired and percent_daily_value follow the statistics.
	const trailingColumns = 3

	statColumns := []string{"min", "max", "median", "loq"}
	tail := make([]any, 0, len(statColumns)+trailingColumns)

	for index, column := range statColumns {
		value, err := file.nullFloat(record[firstStatColumn+index], column)
		if err != nil {
			return nil, err
		}

		tail = append(tail, value)
	}

	year, err := file.nullInt(record[11], colMinYear)
	if err != nil {
		return nil, err
	}

	dailyValue, err := file.nullFloat(record[12], "percent_daily_value")
	if err != nil {
		return nil, err
	}

	return append(tail, text(record[10]), year, dailyValue), nil
}

func (load *loader) loadFoodPortion(ctx context.Context) error {
	return load.insert(ctx, fileFoodPortion,
		`INSERT INTO food_portion(id, fdc_id, seq_num, amount, measure_unit_id, portion_description,
		 modifier, gram_weight, data_points, footnote, min_year_acquired) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			fdcID, kept, err := load.childID(file, record[1])
			if err != nil || !kept {
				return nil, err
			}

			rowID, err := file.mustInt(record[0], colID)
			if err != nil {
				return nil, err
			}

			seq, err := file.nullInt(record[2], "seq_num")
			if err != nil {
				return nil, err
			}

			amount, err := file.nullFloat(record[3], colAmount)
			if err != nil {
				return nil, err
			}

			unit, err := file.nullInt(record[4], "measure_unit_id")
			if err != nil {
				return nil, err
			}

			grams, err := file.nullFloat(record[7], "gram_weight")
			if err != nil {
				return nil, err
			}

			points, err := file.nullInt(record[8], colDataPoints)
			if err != nil {
				return nil, err
			}

			year, err := file.nullInt(record[10], colMinYear)
			if err != nil {
				return nil, err
			}

			return []any{
				rowID, fdcID, seq, amount, unit, text(record[5]), text(record[6]), grams, points, text(record[9]), year,
			}, nil
		})
}

func (load *loader) loadSideTables(ctx context.Context) error {
	for _, dataType := range load.opts.Types {
		var err error

		switch dataType {
		case datatypes.SRLegacy:
			err = load.loadSRLegacy(ctx)
		case datatypes.Foundation:
			err = load.loadFoundation(ctx)
		case datatypes.Survey:
			err = load.loadSurvey(ctx)
		case datatypes.Branded:
			err = load.loadBranded(ctx)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (load *loader) loadSRLegacy(ctx context.Context) error {
	return load.insert(ctx, fileSRLegacyFood, `INSERT INTO sr_legacy_food(fdc_id, ndb_number) VALUES (?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			fdcID, kept, err := load.childID(file, record[0])
			if err != nil || !kept {
				return nil, err
			}

			return []any{fdcID, record[1]}, nil
		})
}

func (load *loader) loadFoundation(ctx context.Context) error {
	return load.insert(ctx, fileFoundationFood, `INSERT INTO foundation_food(fdc_id, ndb_number, footnote) VALUES (?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			fdcID, kept, err := load.childID(file, record[0])
			if err != nil || !kept {
				return nil, err
			}

			return []any{fdcID, text(record[1]), text(record[2])}, nil
		})
}

func (load *loader) loadSurvey(ctx context.Context) error {
	return load.insert(ctx, fileSurveyFnddsFood,
		`INSERT INTO survey_fndds_food(fdc_id, food_code, wweia_category_code, start_date, end_date) VALUES (?,?,?,?,?)`,
		func(file *csvFile, record []string) ([]any, error) {
			fdcID, kept, err := load.childID(file, record[0])
			if err != nil || !kept {
				return nil, err
			}

			code, err := file.nullInt(record[2], "wweia_category_code")
			if err != nil {
				return nil, err
			}

			return []any{fdcID, text(record[1]), code, text(record[3]), text(record[4])}, nil
		})
}

func (load *loader) loadBranded(ctx context.Context) error {
	columns := expectedHeaders()[fileBrandedFood]
	// Column names come from the header table above, never from input.
	query := `INSERT INTO branded_food(` + strings.Join(columns, ", ") + `) VALUES (` +
		store.Placeholders(len(columns)) + `)`

	const servingSizeColumn = 7

	return load.insert(ctx, fileBrandedFood, query,
		func(file *csvFile, record []string) ([]any, error) {
			fdcID, kept, err := load.childID(file, record[0])
			if err != nil || !kept {
				return nil, err
			}

			args := make([]any, len(columns))
			args[0] = fdcID

			for index := 1; index < len(columns); index++ {
				args[index] = text(record[index])
			}

			serving, err := file.nullFloat(record[servingSizeColumn], "serving_size")
			if err != nil {
				return nil, err
			}

			args[servingSizeColumn] = serving

			return args, nil
		})
}

// fillBrandedCategories covers the branded rows whose food_category_id was
// empty by falling back to branded_food.branded_food_category.
func (load *loader) fillBrandedCategories(ctx context.Context) error {
	if !load.keep[datatypes.Branded] {
		return nil
	}

	_, err := load.database.ExecContext(ctx, `UPDATE food SET category_name =
		(SELECT b.branded_food_category FROM branded_food b WHERE b.fdc_id = food.fdc_id)
		WHERE data_type = ? AND category_name IS NULL`, datatypes.Branded)
	if err != nil {
		return fmt.Errorf("fill branded categories: %w", err)
	}

	return nil
}

func (load *loader) buildIndexes(ctx context.Context) error {
	started := time.Now()

	if _, err := load.database.ExecContext(ctx, store.Indexes); err != nil {
		return fmt.Errorf("create indexes: %w", err)
	}

	load.opts.Logf("%-28s %s", "indexes", time.Since(started).Round(time.Millisecond))

	return nil
}

func (load *loader) buildFTS(ctx context.Context) error {
	started := time.Now()

	_, err := load.database.ExecContext(ctx, `INSERT INTO food_fts(rowid, description, brand_owner, brand_name, gtin_upc)
		SELECT f.fdc_id, f.description, b.brand_owner, b.brand_name, b.gtin_upc
		FROM food f LEFT JOIN branded_food b ON b.fdc_id = f.fdc_id`)
	if err != nil {
		return fmt.Errorf("build fts: %w", err)
	}

	load.opts.Logf("%-28s %s", "food_fts", time.Since(started).Round(time.Millisecond))

	return nil
}

func (load *loader) writeMeta(ctx context.Context) error {
	rows := map[string]string{
		meta.KeyDataVersion:     meta.DataVersion,
		meta.KeySourceURL:       meta.DataSourceURL,
		meta.KeySourceSHA256:    meta.DataSHA256,
		meta.KeyImportedAt:      load.opts.Now().UTC().Format(time.RFC3339),
		meta.KeyTypes:           strings.Join(datatypes.ShortList(load.opts.Types), ","),
		meta.KeyImporterVersion: load.opts.Version,
		meta.KeyAttribution:     meta.Attribution,
	}
	for key, value := range rows {
		if _, err := load.database.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES (?,?)`, key, value); err != nil {
			return fmt.Errorf("write meta %s: %w", key, err)
		}
	}

	return nil
}
