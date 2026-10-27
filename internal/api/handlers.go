package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/meta"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

const (
	defaultLimit = 25
	maxLimit     = 200
	// maxQueryRunes bounds the search text. Longer than this is not a food
	// name, and every token becomes part of an FTS expression.
	maxQueryRunes = 200
)

type aboutResponse struct {
	Version         string           `json:"version"`
	DataVersion     string           `json:"data_version"`
	DataSourceURL   string           `json:"data_source_url"`
	DataSHA256      string           `json:"data_sha256"`
	ImportedAt      string           `json:"imported_at"`
	ImporterVersion string           `json:"importer_version"`
	Types           []string         `json:"types"`
	FoodCount       int64            `json:"food_count"`
	FoodCountByType map[string]int64 `json:"food_count_by_type"`
	Attribution     string           `json:"attribution"`
}

// buildAbout reads the import metadata and the per type counts once. The
// database is immutable while served, and the count is a scan over two
// million rows that no request should repeat.
func buildAbout(ctx context.Context, database *sql.DB, version string) (aboutResponse, error) {
	metadata, err := store.Meta(ctx, database)
	if err != nil {
		return aboutResponse{}, fmt.Errorf("read import metadata: %w", err)
	}

	byType, err := store.FoodCountByType(ctx, database)
	if err != nil {
		return aboutResponse{}, fmt.Errorf("count foods per type: %w", err)
	}

	response := aboutResponse{
		Version:         version,
		DataVersion:     metadata[meta.KeyDataVersion],
		DataSourceURL:   metadata[meta.KeySourceURL],
		DataSHA256:      metadata[meta.KeySourceSHA256],
		ImportedAt:      metadata[meta.KeyImportedAt],
		ImporterVersion: metadata[meta.KeyImporterVersion],
		Types:           []string{},
		FoodCountByType: map[string]int64{},
		Attribution:     meta.Attribution,
	}
	if metadata[meta.KeyTypes] != "" {
		response.Types = strings.Split(metadata[meta.KeyTypes], ",")
	}

	for usda, count := range byType {
		response.FoodCountByType[datatypes.ToShort(usda)] = count
		response.FoodCount += count
	}

	return response, nil
}

// Imported reports the data release and then the import time of the served
// database, for the startup log line, so the command does not read the
// metadata twice.
func (server *Server) Imported() (string, string) {
	return server.about.DataVersion, server.about.ImportedAt
}

func (server *Server) handleAbout(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, server.about)
}

type brandJSON struct {
	Owner   *string `json:"owner"`
	Name    *string `json:"name"`
	GTINUPC *string `json:"gtin_upc"`
}

type foodSummaryJSON struct {
	FDCID       int64      `json:"fdc_id"`
	Description string     `json:"description"`
	DataType    string     `json:"data_type"`
	Brand       *brandJSON `json:"brand"`
	Category    *string    `json:"category"`
}

func summaryJSON(food store.FoodSummary) foodSummaryJSON {
	out := foodSummaryJSON{
		FDCID:       food.FDCID,
		Description: food.Description,
		DataType:    datatypes.ToShort(food.DataType),
		Category:    food.Category,
	}
	if food.Brand != nil {
		out.Brand = &brandJSON{Owner: food.Brand.Owner, Name: food.Brand.Name, GTINUPC: food.Brand.GTINUPC}
	}

	return out
}

type searchResponse struct {
	Items []foodSummaryJSON `json:"items"`
	Limit int               `json:"limit"`
}

func (server *Server) handleSearch(writer http.ResponseWriter, request *http.Request) {
	text := strings.TrimSpace(request.URL.Query().Get("q"))
	if text == "" {
		writeError(writer, http.StatusBadRequest, codeBadRequest, "q is required")

		return
	}

	if utf8.RuneCountInString(text) > maxQueryRunes {
		writeError(writer, http.StatusBadRequest, codeBadRequest,
			fmt.Sprintf("q must be at most %d characters", maxQueryRunes))

		return
	}

	var types []string

	if raw := request.URL.Query().Get("type"); raw != "" {
		parsed, err := datatypes.ParseList(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, codeBadRequest, err.Error())

			return
		}

		types = parsed
	}

	limit := defaultLimit

	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeError(writer, http.StatusBadRequest, codeBadRequest, "limit must be a whole number of at least 1")

			return
		}

		limit = min(parsed, maxLimit)
	}

	hits, err := store.Search(request.Context(), server.database, text, types, limit)
	if err != nil {
		server.internalError(writer, request, err)

		return
	}

	response := searchResponse{Items: make([]foodSummaryJSON, 0, len(hits)), Limit: limit}
	for _, hit := range hits {
		response.Items = append(response.Items, summaryJSON(hit))
	}

	writeJSON(writer, http.StatusOK, response)
}

// fdcID parses the path value, writing a 400 on failure.
func fdcID(writer http.ResponseWriter, request *http.Request) (int64, bool) {
	parsed, err := strconv.ParseInt(request.PathValue("fdc_id"), 10, 64)
	if err != nil || parsed < 1 {
		writeError(writer, http.StatusBadRequest, codeBadRequest, "fdc_id must be a positive integer")

		return 0, false
	}

	return parsed, true
}

type foodResponse struct {
	foodSummaryJSON

	PerGrams  int                      `json:"per_grams"`
	Nutrients map[string]NutrientValue `json:"nutrients"`
}

func (server *Server) handleFood(writer http.ResponseWriter, request *http.Request) {
	foodID, valid := fdcID(writer, request)
	if !valid {
		return
	}

	food, err := store.GetFood(request.Context(), server.database, foodID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, codeNotFound, "fdc_id "+strconv.FormatInt(foodID, 10)+" not found")

		return
	}

	if err != nil {
		server.internalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, foodResponse{
		foodSummaryJSON: summaryJSON(food.FoodSummary),
		PerGrams:        per100Grams,
		Nutrients:       server.nutrientMap(food.Nutrients),
	})
}

type portionsResponse struct {
	FDCID int64         `json:"fdc_id"`
	Items []portionItem `json:"items"`
}

func (server *Server) handlePortions(writer http.ResponseWriter, request *http.Request) {
	foodID, valid := fdcID(writer, request)
	if !valid {
		return
	}

	dataType, rows, serving, err := store.GetPortions(request.Context(), server.database, foodID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, codeNotFound, "fdc_id "+strconv.FormatInt(foodID, 10)+" not found")

		return
	}

	if err != nil {
		server.internalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, portionsResponse{FDCID: foodID, Items: portionItems(dataType, rows, serving)})
}
