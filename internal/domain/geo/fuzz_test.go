package geo_test

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// FuzzParsePoint (TEST-006, TST-016): the position parser of every near
// search reads caller text. Whatever it is given, it either refuses it with
// ErrPoint or returns a finite point inside the WGS 84 ranges whose text form
// reads back to the same point; it never panics.
// Seed corpus: testdata/fuzz/FuzzParsePoint (test/fuzz/targets.tsv).
func FuzzParsePoint(f *testing.F) {
	for _, s := range []string{"52.52,13.405", "-90,180", "91,0", "NaN,1", "1,2,3", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := geo.ParsePoint(s)
		if err != nil {
			if !errors.Is(err, geo.ErrPoint) {
				t.Fatalf("ParsePoint(%q): %v is not ErrPoint", s, err)
			}
			return
		}
		if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || math.Abs(p.Lat) > 90 || math.Abs(p.Lon) > 180 {
			t.Fatalf("ParsePoint(%q) = %v, outside the ranges", s, p)
		}
		again, err := geo.ParsePoint(strconv.FormatFloat(p.Lat, 'f', -1, 64) + "," + strconv.FormatFloat(p.Lon, 'f', -1, 64))
		if err != nil || again != p {
			t.Fatalf("ParsePoint(%q) = %v does not read back: %v, %v", s, p, again, err)
		}
	})
}
