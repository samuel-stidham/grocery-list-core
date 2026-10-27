// Package meta holds the constants shared by the importer, the API, and the
// command: the USDA attribution text, the pinned data release, and the keys of
// the meta table.
package meta

// Attribution is the citation USDA asks for. It is served by /v1/about and in
// the X-Data-Attribution header of every response.
const Attribution = "U.S. Department of Agriculture, Agricultural Research Service. " +
	"FoodData Central. https://fdc.nal.usda.gov"

// AttributionHeader is the response header that carries Attribution.
const AttributionHeader = "X-Data-Attribution"

// The pinned release. data/fetch.sh downloads exactly this archive.
const (
	DataVersion   = "2026-04-30"
	DataSourceURL = "https://fdc.nal.usda.gov/fdc-datasets/FoodData_Central_csv_2026-04-30.zip"
	DataSHA256    = "c33d91e5c44ddbcd71ed6038714fb648b26089b3f91fabfa3be63d96d5763a01"
)

// Keys of the meta table written by the importer.
const (
	KeyDataVersion     = "data_version"
	KeySourceURL       = "source_url"
	KeySourceSHA256    = "source_sha256"
	KeyImportedAt      = "imported_at"
	KeyTypes           = "types"
	KeyImporterVersion = "importer_version"
	KeyAttribution     = "attribution"
)
