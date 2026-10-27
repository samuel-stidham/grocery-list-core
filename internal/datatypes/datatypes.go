// Package datatypes maps the short food type names used on the command line
// and in the API to the data_type values USDA writes in food.csv.
package datatypes

import (
	"fmt"
	"strings"
)

// USDA data_type values of the four food types this project imports.
const (
	SRLegacy   = "sr_legacy_food"
	Foundation = "foundation_food"
	Branded    = "branded_food"
	Survey     = "survey_fndds_food"
)

// Short names used on the command line and in the API.
const (
	ShortSRLegacy   = "sr_legacy"
	ShortFoundation = "foundation"
	ShortBranded    = "branded"
	ShortSurvey     = "survey"
)

// All returns every short name in canonical order.
func All() []string {
	return []string{ShortSRLegacy, ShortFoundation, ShortBranded, ShortSurvey}
}

// AllUSDA returns every USDA data_type value in canonical order.
func AllUSDA() []string {
	return []string{SRLegacy, Foundation, Branded, Survey}
}

// ToUSDA converts a short name. The bool is false for an unknown name.
func ToUSDA(short string) (string, bool) {
	switch short {
	case ShortSRLegacy:
		return SRLegacy, true
	case ShortFoundation:
		return Foundation, true
	case ShortBranded:
		return Branded, true
	case ShortSurvey:
		return Survey, true
	default:
		return "", false
	}
}

// ToShort converts a USDA data_type to its short name. Unknown values are
// returned unchanged so callers never lose information.
func ToShort(usda string) string {
	switch usda {
	case SRLegacy:
		return ShortSRLegacy
	case Foundation:
		return ShortFoundation
	case Branded:
		return ShortBranded
	case Survey:
		return ShortSurvey
	default:
		return usda
	}
}

// ParseList parses a comma separated list of short names into USDA data_type
// values, in canonical order, without duplicates. An empty list is an error.
func ParseList(list string) ([]string, error) {
	seen := map[string]bool{}

	for _, raw := range strings.Split(list, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}

		if _, known := ToUSDA(name); !known {
			return nil, fmt.Errorf("%w: %q (want one of %s)", ErrUnknownType, name, strings.Join(All(), ", "))
		}

		seen[name] = true
	}

	if len(seen) == 0 {
		return nil, fmt.Errorf("%w (want one of %s)", ErrNoTypes, strings.Join(All(), ", "))
	}

	var out []string

	for _, short := range All() {
		if seen[short] {
			usda, _ := ToUSDA(short)
			out = append(out, usda)
		}
	}

	return out, nil
}

// ShortList converts USDA values back to short names, preserving order.
func ShortList(usda []string) []string {
	out := make([]string, len(usda))
	for index, value := range usda {
		out[index] = ToShort(value)
	}

	return out
}
