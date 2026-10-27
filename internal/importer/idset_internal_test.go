package importer

import "testing"

func TestIDSet(t *testing.T) {
	var set idSet

	if set.Has(0) || set.Has(5000000) {
		t.Fatal("empty set reports membership")
	}

	for _, fdcID := range []int64{0, 1, 63, 64, 2768939, 1, 63} {
		set.Add(fdcID)
	}

	if set.Len() != 5 {
		t.Fatalf("Len = %d, want 5 distinct ids", set.Len())
	}

	for _, fdcID := range []int64{0, 1, 63, 64, 2768939} {
		if !set.Has(fdcID) {
			t.Fatalf("missing %d", fdcID)
		}
	}

	for _, fdcID := range []int64{2, 62, 65, 2768938, 3000000, -1} {
		if set.Has(fdcID) {
			t.Fatalf("unexpected %d", fdcID)
		}
	}

	set.Add(-5)

	if set.Len() != 5 {
		t.Fatal("negative ids must be ignored")
	}
}
