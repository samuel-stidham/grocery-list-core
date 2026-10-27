package api

import "testing"

func TestNormalizeServingUnit(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"g", "g"},
		{"G", "g"},
		{"GRM", "g"},
		{"grm", "g"},
		{"GM", "g"},
		{" g ", "g"},
		{"ml", "ml"},
		{"ML", "ml"},
		{"MLT", "ml"},
		{"MG", "mg"},
		{"IU", "iu"},
		{"MC", "mc"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeServingUnit(tc.raw); got != tc.want {
			t.Errorf("normalizeServingUnit(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
