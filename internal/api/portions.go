package api

import (
	"strconv"
	"strings"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// portionItem is one entry of a portions response. gram_weight is null only
// for a branded serving measured in millilitres.
type portionItem struct {
	ID          *int64   `json:"id"`
	SeqNum      *int64   `json:"seq_num"`
	Amount      *float64 `json:"amount"`
	Unit        *string  `json:"unit"`
	Label       string   `json:"label"`
	Description *string  `json:"description"`
	Modifier    *string  `json:"modifier"`
	GramWeight  *float64 `json:"gram_weight"`
}

// portionItems converts store rows to response items. Rows without a usable
// gram weight are dropped, since they cannot convert anything.
func portionItems(dataType string, rows []store.Portion, serving *store.BrandedServing) []portionItem {
	items := make([]portionItem, 0, len(rows)+1)

	for _, portion := range rows {
		if portion.GramWeight == nil || *portion.GramWeight <= 0 {
			continue
		}

		portionID := portion.ID
		items = append(items, portionItem{
			ID:          &portionID,
			SeqNum:      portion.SeqNum,
			Amount:      portion.Amount,
			Unit:        portion.UnitName,
			Label:       portionLabel(dataType, portion),
			Description: portion.Description,
			Modifier:    portion.Modifier,
			GramWeight:  portion.GramWeight,
		})
	}

	if serving != nil && serving.ServingSize != nil {
		items = append(items, brandedItem(serving))
	}

	return items
}

// portionLabel composes the household text per data type. SR Legacy puts the
// measure in modifier, Foundation uses a real unit plus a modifier and keeps
// portion_description for comments, and Survey writes the whole thing in
// portion_description.
func portionLabel(dataType string, portion store.Portion) string {
	switch dataType {
	case datatypes.Survey:
		return deref(portion.Description)
	case datatypes.SRLegacy:
		return joinNonEmpty(num(portion.Amount), deref(portion.Modifier))
	default:
		return joinNonEmpty(num(portion.Amount), deref(portion.UnitName), deref(portion.Modifier))
	}
}

// Serving unit spellings in branded_food.serving_size_unit. USDA writes grams
// four ways and millilitres two ways. Everything else (MG, IU, MC) is left
// lowercased and never gets a gram weight.
const (
	unitGrams       = "g"
	unitMillilitres = "ml"
)

// normalizeServingUnit maps the spellings USDA uses for one unit onto the
// one the API promises: g for grams, ml for millilitres.
func normalizeServingUnit(raw string) string {
	unit := strings.ToLower(strings.TrimSpace(raw))

	switch unit {
	case "g", "grm", "gm":
		return unitGrams
	case "ml", "mlt":
		return unitMillilitres
	default:
		return unit
	}
}

// brandedItem synthesizes the label serving of a branded food. Only a serving
// in grams gets a gram weight. A millilitre serving keeps its unit so the
// caller can see it is a volume, never a weight. The label is the household
// text when USDA has one, else the size and unit.
func brandedItem(serving *store.BrandedServing) portionItem {
	seq := int64(1)
	unit := normalizeServingUnit(deref(serving.Unit))

	label := deref(serving.Household)
	if label == "" {
		label = joinNonEmpty(num(serving.ServingSize), unit)
	}

	item := portionItem{SeqNum: &seq, Amount: serving.ServingSize, Label: label}
	if unit != "" {
		item.Unit = &unit
	}

	if unit == unitGrams {
		item.GramWeight = serving.ServingSize
	}

	return item
}

func num(value *float64) string {
	if value == nil {
		return ""
	}

	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func joinNonEmpty(parts ...string) string {
	var kept []string

	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, strings.TrimSpace(part))
		}
	}

	return strings.Join(kept, " ")
}
