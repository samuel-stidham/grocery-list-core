package datatypes_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/samuel-stidham/grocery-list-core/internal/datatypes"
)

func TestParseListCanonicalOrderAndDedup(t *testing.T) {
	got, err := datatypes.ParseList("branded, sr_legacy,branded,survey")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"sr_legacy_food", "branded_food", "survey_fndds_food"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseListRejectsUnknown(t *testing.T) {
	_, err := datatypes.ParseList("sr_legacy,experimental")
	if !errors.Is(err, datatypes.ErrUnknownType) || !strings.Contains(err.Error(), `"experimental"`) {
		t.Fatalf("got %v", err)
	}
}

func TestParseListRejectsEmpty(t *testing.T) {
	for _, in := range []string{"", " , "} {
		if _, err := datatypes.ParseList(in); !errors.Is(err, datatypes.ErrNoTypes) {
			t.Fatalf("%q: got %v", in, err)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, short := range datatypes.All() {
		usda, ok := datatypes.ToUSDA(short)
		if !ok {
			t.Fatalf("%s has no USDA value", short)
		}

		if back := datatypes.ToShort(usda); back != short {
			t.Fatalf("%s -> %s -> %s", short, usda, back)
		}
	}

	if got := datatypes.ToShort("market_acquistion"); got != "market_acquistion" {
		t.Fatalf("unknown values must pass through, got %q", got)
	}

	if got := datatypes.ShortList(datatypes.AllUSDA()); !reflect.DeepEqual(got, datatypes.All()) {
		t.Fatalf("datatypes.ShortList(datatypes.AllUSDA()) = %v", got)
	}
}
