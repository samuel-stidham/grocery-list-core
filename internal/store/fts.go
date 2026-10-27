package store

import (
	"strings"
	"unicode"
)

// GTIN and UPC codes are between 8 and 14 digits long.
const (
	minGTINDigits = 8
	maxGTINDigits = 14
)

// BuildMatch turns free text into an FTS5 MATCH expression. Every token is
// quoted so FTS operators typed by a user (AND, OR, NOT, *, ^, :) stay literal.
// The last token gets a prefix star so "spin" already finds "spinach". The
// bool is false when the text holds no searchable token.
func BuildMatch(text string) (string, bool) {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return "", false
	}

	// Tokens hold letters and digits only, so a quote can never appear inside
	// one and no escaping is needed.
	parts := make([]string, len(tokens))
	for index, token := range tokens {
		quoted := `"` + token + `"`
		if index == len(tokens)-1 {
			quoted += "*"
		}

		parts[index] = quoted
	}

	return strings.Join(parts, " "), true
}

// tokenize splits on everything that is not a letter or a digit, which is how
// the unicode61 tokenizer splits the indexed text.
func tokenize(text string) []string {
	return strings.FieldsFunc(text, func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsNumber(char)
	})
}

// IsGTIN reports whether the text looks like a GTIN or UPC: 8 to 14 digits.
func IsGTIN(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) < minGTINDigits || len(text) > maxGTINDigits {
		return false
	}

	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}

	return true
}

// GTINVariants returns the spellings a code may have in branded_food.gtin_upc.
// USDA mostly stores 14 digits with leading zeros, so the bare code is padded
// to the common lengths. Duplicates are removed and order is kept.
func GTINVariants(text string) []string {
	text = strings.TrimSpace(text)

	bare := strings.TrimLeft(text, "0")
	if bare == "" {
		bare = "0"
	}

	seen := map[string]bool{}

	var out []string

	add := func(variant string) {
		if !seen[variant] {
			seen[variant] = true
			out = append(out, variant)
		}
	}

	add(text)
	add(bare)

	for _, width := range []int{8, 12, 13, 14} {
		if len(bare) <= width {
			add(strings.Repeat("0", width-len(bare)) + bare)
		}
	}

	return out
}
