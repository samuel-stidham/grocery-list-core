package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// Request bounds. The grams limit is one tonne per item and the servings
// bounds keep a sum finite and a division sane. None of them is a nutrition
// number, only a guard against overflow and nonsense.
const (
	maxScoreItems       = 500
	maxScoreBody  int64 = 1 << 20
	maxGrams            = 1_000_000
	minServings         = 0.001
	maxServings         = 1_000_000
)

type scoreRequest struct {
	Servings float64     `json:"servings"`
	Items    []scoreItem `json:"items"`
}

type scoreItem struct {
	FDCID int64   `json:"fdc_id"`
	Grams float64 `json:"grams"`
}

// scoreValue is one summed nutrient. Amount is never null here: a food with
// no value contributes zero and is listed under missing instead.
type scoreValue struct {
	Name   string  `json:"name"`
	Unit   string  `json:"unit"`
	Amount float64 `json:"amount"`
}

type missingValue struct {
	FDCID      int64 `json:"fdc_id"`
	NutrientID int64 `json:"nutrient_id"`
}

type scoreResponse struct {
	Servings   float64               `json:"servings"`
	TotalGrams float64               `json:"total_grams"`
	PerServing map[string]scoreValue `json:"per_serving"`
	Total      map[string]scoreValue `json:"total"`
	Missing    []missingValue        `json:"missing"`
}

func (server *Server) handleScore(writer http.ResponseWriter, request *http.Request) {
	var payload scoreRequest

	if !decodeBody(writer, request, &payload) {
		return
	}

	if err := validateScore(payload); err != nil {
		writeError(writer, http.StatusBadRequest, codeBadRequest, err.Error())

		return
	}

	ids := make([]int64, 0, len(payload.Items))
	seen := map[int64]bool{}

	for _, item := range payload.Items {
		if !seen[item.FDCID] {
			seen[item.FDCID] = true
			ids = append(ids, item.FDCID)
		}
	}

	missingFoods, err := store.MissingFoods(request.Context(), server.database, ids)
	if err != nil {
		server.internalError(writer, request, err)

		return
	}

	if len(missingFoods) > 0 {
		writeError(writer, http.StatusBadRequest, codeBadRequest, "unknown fdc_id: "+joinIDs(missingFoods))

		return
	}

	amounts, err := store.NutrientAmounts(request.Context(), server.database, ids, scoredNutrientIDs())
	if err != nil {
		server.internalError(writer, request, err)

		return
	}

	writeJSON(writer, http.StatusOK, server.score(payload, amounts))
}

// decodeBody reads one JSON object and nothing else. The body is capped, so
// an oversized request is a 413, and trailing data after the object is a 400
// rather than silently ignored.
func decodeBody(writer http.ResponseWriter, request *http.Request, payload *scoreRequest) bool {
	body := http.MaxBytesReader(writer, request.Body, maxScoreBody)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(payload); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, codeBadRequest,
				fmt.Sprintf("body must be at most %d bytes", maxScoreBody))

			return false
		}

		writeError(writer, http.StatusBadRequest, codeBadRequest, decodeMessage(err))

		return false
	}

	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, codeBadRequest, "body: trailing data after the JSON object")

		return false
	}

	return true
}

// decodeMessage names the JSON problem in the caller's terms. The
// encoding/json messages mention Go types and struct fields, which mean
// nothing to an API client.
func decodeMessage(err error) string {
	var (
		syntax    *json.SyntaxError
		wrongType *json.UnmarshalTypeError
	)

	switch {
	case errors.Is(err, io.EOF):
		return "body: empty, want a JSON object"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "body: malformed JSON, the document ends early"
	case errors.As(err, &syntax):
		return fmt.Sprintf("body: malformed JSON at offset %d", syntax.Offset)
	case errors.As(err, &wrongType) && wrongType.Field == "":
		return "body: must be a JSON object"
	case errors.As(err, &wrongType):
		return fmt.Sprintf("body: field %q has the wrong type", wrongType.Field)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return "body: " + strings.TrimPrefix(err.Error(), "json: ")
	default:
		return "body: not a valid score request"
	}
}

func validateScore(payload scoreRequest) error {
	if !(payload.Servings >= minServings && payload.Servings <= maxServings) {
		return fmt.Errorf("servings must be between %g and %d", minServings, maxServings)
	}

	if len(payload.Items) == 0 {
		return errors.New("items must hold at least one entry")
	}

	if len(payload.Items) > maxScoreItems {
		return fmt.Errorf("items must hold at most %d entries", maxScoreItems)
	}

	for index, item := range payload.Items {
		if item.FDCID < 1 {
			return fmt.Errorf("items[%d].fdc_id must be a positive integer", index)
		}

		if !(item.Grams >= 0 && item.Grams <= maxGrams) {
			return fmt.Errorf("items[%d].grams must be between 0 and %d", index, maxGrams)
		}
	}

	return nil
}

// score sums the minimum nutrients over the items. Each value is per 100 g,
// so a food contributes amount * grams / 100. Energy falls back to the Atwater
// ids per food, the same way a single food response does.
func (server *Server) score(payload scoreRequest, amounts store.AmountTable) scoreResponse {
	totals := map[int64]float64{}
	response := scoreResponse{Servings: payload.Servings, Missing: []missingValue{}}
	// The same food may appear twice in a list. Its grams add up, but a
	// missing nutrient is reported once per food.
	reported := map[missingValue]bool{}

	for _, item := range payload.Items {
		response.TotalGrams += item.Grams
		values := amounts[item.FDCID]

		for _, nutrientID := range minimumNutrientIDs() {
			per100, present := values[nutrientID]
			if !present && nutrientID == energyID {
				per100, present = fallbackValue(values)
			}

			if !present {
				pair := missingValue{FDCID: item.FDCID, NutrientID: nutrientID}
				if !reported[pair] {
					reported[pair] = true
					response.Missing = append(response.Missing, pair)
				}

				continue
			}

			totals[nutrientID] += per100 * item.Grams / per100Grams
		}
	}

	response.Total = make(map[string]scoreValue, len(minimumNutrientIDs()))
	response.PerServing = make(map[string]scoreValue, len(minimumNutrientIDs()))

	for _, nutrientID := range minimumNutrientIDs() {
		info := server.info[nutrientID]
		response.Total[key(nutrientID)] = scoreValue{Name: info.Name, Unit: info.Unit, Amount: totals[nutrientID]}
		response.PerServing[key(nutrientID)] = scoreValue{
			Name: info.Name, Unit: info.Unit, Amount: totals[nutrientID] / payload.Servings,
		}
	}

	return response
}

func fallbackValue(values map[int64]float64) (float64, bool) {
	for _, nutrientID := range energyFallbacks() {
		if value, present := values[nutrientID]; present {
			return value, true
		}
	}

	return 0, false
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for index, fdcID := range ids {
		parts[index] = strconv.FormatInt(fdcID, 10)
	}

	return strings.Join(parts, ", ")
}
