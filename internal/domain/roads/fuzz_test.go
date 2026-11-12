package roads_test

import (
	"errors"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// FuzzParseRoad (TEST-006, TST-016): the road reference of a motorway's
// conditions is caller text. Whatever it is given, it either refuses it with
// ErrRoadReference or returns a canonical reference that reads back to
// itself; it never panics.
// Seed corpus: testdata/fuzz/FuzzParseRoad (test/fuzz/targets.tsv).
func FuzzParseRoad(f *testing.F) {
	for _, s := range []string{"A8", "a 8", "B2700", "A12345", "C8", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := roads.ParseRoad(s)
		if err != nil {
			if !errors.Is(err, roads.ErrRoadReference) {
				t.Fatalf("ParseRoad(%q): %v is not ErrRoadReference", s, err)
			}
			return
		}
		again, err := roads.ParseRoad(r)
		if err != nil || again != r || roads.Canonical(r) != r {
			t.Fatalf("ParseRoad(%q) = %q does not read back: %q, %v", s, r, again, err)
		}
	})
}
