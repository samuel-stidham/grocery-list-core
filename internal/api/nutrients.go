package api

import (
	"strconv"

	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// Energy. Most Foundation foods have no 1008 row and only carry the Atwater
// values, so energy falls back to 2048 (specific factors) then 2047 (general).
const (
	energyID              int64 = 1008
	energyAtwaterSpecific int64 = 2048
	energyAtwaterGeneral  int64 = 2047
	proteinID             int64 = 1003
	carbohydrateID        int64 = 1005
	totalSugarsID         int64 = 2000
	fiberID               int64 = 1079
	calciumID             int64 = 1087
	ironID                int64 = 1089
	sodiumID              int64 = 1093
	per100Grams                 = 100
)

// minimumNutrientIDs are the nutrients every food and score response carries,
// even when the food has no value. Order is the order the frontend shows them.
func minimumNutrientIDs() []int64 {
	return []int64{energyID, proteinID, carbohydrateID, totalSugarsID, fiberID, calciumID, ironID, sodiumID}
}

// energyFallbacks are tried in order when a food has no 1008 row.
func energyFallbacks() []int64 {
	return []int64{energyAtwaterSpecific, energyAtwaterGeneral}
}

// scoredNutrientIDs is the minimum set plus the energy fallbacks.
func scoredNutrientIDs() []int64 {
	return append(minimumNutrientIDs(), energyFallbacks()...)
}

// NutrientValue is one entry of a nutrients map, per 100 g.
type NutrientValue struct {
	Name string `json:"name"`
	Unit string `json:"unit"`
	// Amount is null when USDA has no value for the food.
	Amount *float64 `json:"amount"`
	// DerivedFrom names the nutrient id that supplied an energy fallback.
	DerivedFrom *int64 `json:"derived_from"`
}

// nutrientMap builds the response map for a food. Every nutrient the food has
// is included, and the minimum ids are filled in with null or a fallback.
func (server *Server) nutrientMap(rows []store.NutrientAmount) map[string]NutrientValue {
	byID := make(map[int64]*float64, len(rows))
	out := make(map[string]NutrientValue, len(rows)+len(minimumNutrientIDs()))

	for _, row := range rows {
		byID[row.NutrientID] = row.Amount
		out[key(row.NutrientID)] = NutrientValue{Name: row.Name, Unit: row.Unit, Amount: row.Amount}
	}

	for _, nutrientID := range minimumNutrientIDs() {
		if amount, present := byID[nutrientID]; present && amount != nil {
			continue
		}

		info := server.info[nutrientID]
		value := NutrientValue{Name: info.Name, Unit: info.Unit}

		if nutrientID == energyID {
			value.Amount, value.DerivedFrom = energyFallback(byID)
		}

		out[key(nutrientID)] = value
	}

	return out
}

// energyFallback returns the first Atwater energy value present.
func energyFallback(byID map[int64]*float64) (*float64, *int64) {
	for _, nutrientID := range energyFallbacks() {
		if amount, present := byID[nutrientID]; present && amount != nil {
			from := nutrientID

			return amount, &from
		}
	}

	return nil, nil
}

func key(nutrientID int64) string {
	return strconv.FormatInt(nutrientID, 10)
}
