// Package importer streams the FoodData Central CSV files into a SQLite
// database. It reads only the files the API needs, keeps only the food types
// asked for, and writes the result atomically.
package importer

import "github.com/samuel-stidham/grocery-list-core/internal/datatypes"

// File names inside the extracted archive.
const (
	fileFood                   = "food.csv"
	fileNutrient               = "nutrient.csv"
	fileFoodNutrient           = "food_nutrient.csv"
	fileFoodPortion            = "food_portion.csv"
	fileMeasureUnit            = "measure_unit.csv"
	fileFoodCategory           = "food_category.csv"
	fileWweiaFoodCategory      = "wweia_food_category.csv"
	fileFoodNutrientDerivation = "food_nutrient_derivation.csv"
	fileSRLegacyFood           = "sr_legacy_food.csv"
	fileFoundationFood         = "foundation_food.csv"
	fileSurveyFnddsFood        = "survey_fndds_food.csv"
	fileBrandedFood            = "branded_food.csv"
)

// Column names that appear in more than one header.
const (
	colFDCID       = "fdc_id"
	colID          = "id"
	colDescription = "description"
	colAmount      = "amount"
	colFootnote    = "footnote"
	colDataPoints  = "data_points"
	colMinYear     = "min_year_acquired"
	colNDBNumber   = "NDB_number"
)

// expectedHeaders is the exact header row of every file read, as shipped in
// the April 2026 release. A different header means a different release, and
// the importer refuses it rather than guessing at columns.
func expectedHeaders() map[string][]string {
	return map[string][]string{
		fileFood:     {colFDCID, "data_type", colDescription, "food_category_id", "publication_date"},
		fileNutrient: {colID, "name", "unit_name", "nutrient_nbr", "rank"},
		fileFoodNutrient: {
			colID, colFDCID, "nutrient_id", colAmount, colDataPoints, "derivation_id",
			"min", "max", "median", "loq", colFootnote, colMinYear, "percent_daily_value",
		},
		fileFoodPortion: {
			colID, colFDCID, "seq_num", colAmount, "measure_unit_id", "portion_description",
			"modifier", "gram_weight", colDataPoints, colFootnote, colMinYear,
		},
		fileMeasureUnit:            {colID, "name"},
		fileFoodCategory:           {colID, "code", colDescription},
		fileWweiaFoodCategory:      {"wweia_food_category", "wweia_food_category_description"},
		fileFoodNutrientDerivation: {colID, "code", colDescription},
		fileSRLegacyFood:           {colFDCID, colNDBNumber},
		fileFoundationFood:         {colFDCID, colNDBNumber, colFootnote},
		fileSurveyFnddsFood:        {colFDCID, "food_code", "wweia_category_code", "start_date", "end_date"},
		fileBrandedFood: {
			colFDCID, "brand_owner", "brand_name", "subbrand_name", "gtin_upc", "ingredients",
			"not_a_significant_source_of", "serving_size", "serving_size_unit", "household_serving_fulltext",
			"branded_food_category", "data_source", "package_weight", "modified_date", "available_date",
			"market_country", "discontinued_date", "preparation_state_code", "trade_channel",
			"short_description", "material_code",
		},
	}
}

// coreFiles are read on every import, in load order.
func coreFiles() []string {
	return []string{
		fileFoodCategory,
		fileWweiaFoodCategory,
		fileMeasureUnit,
		fileNutrient,
		fileFoodNutrientDerivation,
		fileFood,
		fileFoodNutrient,
		fileFoodPortion,
	}
}

// sideFile names the file that holds the extra columns of a data type.
func sideFile(dataType string) string {
	switch dataType {
	case datatypes.SRLegacy:
		return fileSRLegacyFood
	case datatypes.Foundation:
		return fileFoundationFood
	case datatypes.Survey:
		return fileSurveyFnddsFood
	case datatypes.Branded:
		return fileBrandedFood
	default:
		return ""
	}
}

// requiredFiles lists every file an import of the given types must find.
func requiredFiles(types []string) []string {
	files := coreFiles()

	for _, dataType := range types {
		if name := sideFile(dataType); name != "" {
			files = append(files, name)
		}
	}

	return files
}
