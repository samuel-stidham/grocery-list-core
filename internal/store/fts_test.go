package store_test

import (
	"reflect"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

func TestBuildMatch(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"spin", `"spin"*`, true},
		{"spinach raw", `"spinach" "raw"*`, true},
		{"  Crème   fraîche ", `"Crème" "fraîche"*`, true},
		{`cheese AND "cheddar" OR x:y NOT *`, `"cheese" "AND" "cheddar" "OR" "x" "y" "NOT"*`, true},
		{"", "", false},
		{"!!! ... ---", "", false},
		{"2% milk", `"2" "milk"*`, true},
	}
	for _, c := range cases {
		got, ok := store.BuildMatch(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("store.BuildMatch(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestIsGTIN(t *testing.T) {
	yes := []string{"00027000612323", "027000612323", "12345678", " 12345678 "}
	no := []string{"1234567", "123456789012345", "0002700061232a", "", "spinach"}

	for _, s := range yes {
		if !store.IsGTIN(s) {
			t.Errorf("store.IsGTIN(%q) = false", s)
		}
	}

	for _, s := range no {
		if store.IsGTIN(s) {
			t.Errorf("store.IsGTIN(%q) = true", s)
		}
	}
}

func TestGTINVariants(t *testing.T) {
	got := store.GTINVariants("027000612323")

	want := []string{"027000612323", "27000612323", "0027000612323", "00027000612323"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	wantZeros := []string{"00000000", "0", "000000000000", "0000000000000", "00000000000000"}
	if got := store.GTINVariants("00000000"); !reflect.DeepEqual(got, wantZeros) {
		t.Fatalf("all zeros: %v", got)
	}
}
