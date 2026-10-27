package store

// Schema is the DDL for every table and the FTS index. Identifiers are
// lowercase snake_case. Indexes live in Indexes and are created after the bulk
// load, which is faster than maintaining them row by row.
const Schema = `
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE food_category (
  id          INTEGER PRIMARY KEY,
  code        TEXT NOT NULL,
  description TEXT NOT NULL
);

CREATE TABLE wweia_food_category (
  wweia_food_category             INTEGER PRIMARY KEY,
  wweia_food_category_description TEXT NOT NULL
);

CREATE TABLE measure_unit (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL
);

CREATE TABLE nutrient (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  unit_name    TEXT NOT NULL,
  nutrient_nbr TEXT,
  rank         REAL
);

CREATE TABLE food_nutrient_derivation (
  id          INTEGER PRIMARY KEY,
  code        TEXT NOT NULL,
  description TEXT NOT NULL
);

CREATE TABLE food (
  fdc_id           INTEGER PRIMARY KEY,
  data_type        TEXT NOT NULL,
  description      TEXT NOT NULL,
  food_category_id TEXT,
  publication_date TEXT,
  category_name    TEXT
);

CREATE TABLE food_nutrient (
  id                  INTEGER PRIMARY KEY,
  fdc_id              INTEGER NOT NULL REFERENCES food(fdc_id),
  nutrient_id         INTEGER NOT NULL REFERENCES nutrient(id),
  amount              REAL,
  data_points         INTEGER,
  derivation_id       INTEGER,
  min                 REAL,
  max                 REAL,
  median              REAL,
  loq                 REAL,
  footnote            TEXT,
  min_year_acquired   INTEGER,
  percent_daily_value REAL
);

CREATE TABLE food_portion (
  id                  INTEGER PRIMARY KEY,
  fdc_id              INTEGER NOT NULL REFERENCES food(fdc_id),
  seq_num             INTEGER,
  amount              REAL,
  measure_unit_id     INTEGER REFERENCES measure_unit(id),
  portion_description TEXT,
  modifier            TEXT,
  gram_weight         REAL,
  data_points         INTEGER,
  footnote            TEXT,
  min_year_acquired   INTEGER
);

CREATE TABLE sr_legacy_food (
  fdc_id     INTEGER PRIMARY KEY REFERENCES food(fdc_id),
  ndb_number TEXT NOT NULL
);

CREATE TABLE foundation_food (
  fdc_id     INTEGER PRIMARY KEY REFERENCES food(fdc_id),
  ndb_number TEXT,
  footnote   TEXT
);

CREATE TABLE survey_fndds_food (
  fdc_id              INTEGER PRIMARY KEY REFERENCES food(fdc_id),
  food_code           TEXT,
  wweia_category_code INTEGER,
  start_date          TEXT,
  end_date            TEXT
);

CREATE TABLE branded_food (
  fdc_id                      INTEGER PRIMARY KEY REFERENCES food(fdc_id),
  brand_owner                 TEXT,
  brand_name                  TEXT,
  subbrand_name               TEXT,
  gtin_upc                    TEXT,
  ingredients                 TEXT,
  not_a_significant_source_of TEXT,
  serving_size                REAL,
  serving_size_unit           TEXT,
  household_serving_fulltext  TEXT,
  branded_food_category       TEXT,
  data_source                 TEXT,
  package_weight              TEXT,
  modified_date               TEXT,
  available_date              TEXT,
  market_country              TEXT,
  discontinued_date           TEXT,
  preparation_state_code      TEXT,
  trade_channel               TEXT,
  short_description           TEXT,
  material_code               TEXT
);

CREATE VIRTUAL TABLE food_fts USING fts5(
  description, brand_owner, brand_name, gtin_upc,
  content='', tokenize='unicode61 remove_diacritics 2'
);
`

// Indexes are created after the bulk load.
const Indexes = `
CREATE INDEX food_data_type ON food(data_type);
CREATE INDEX food_nutrient_fdc_id_nutrient_id ON food_nutrient(fdc_id, nutrient_id);
CREATE INDEX food_portion_fdc_id ON food_portion(fdc_id);
CREATE INDEX branded_food_gtin_upc ON branded_food(gtin_upc);
`

// UndeterminedMeasureUnit is USDA's measure_unit id for "undetermined", used
// by SR Legacy and Survey foods whose measure text lives in another column.
const UndeterminedMeasureUnit = 9999

// Tables lists every table in load order, used for row counts in reports.
func Tables() []string {
	return []string{
		"meta",
		"food_category",
		"wweia_food_category",
		"measure_unit",
		"nutrient",
		"food_nutrient_derivation",
		"food",
		"food_nutrient",
		"food_portion",
		"sr_legacy_food",
		"foundation_food",
		"survey_fndds_food",
		"branded_food",
	}
}
