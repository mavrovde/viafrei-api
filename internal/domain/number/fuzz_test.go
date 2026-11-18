package number_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/number"
)

// FuzzParseFinite (TEST-006, TST-016): every float the service reads from a
// request, a configuration or a registry goes through ParseFinite. Whatever
// it is given, it refuses it or returns a finite number that reads back to
// itself; NaN and the infinities are ErrNotFinite; it never panics.
// Seed corpus: testdata/fuzz/FuzzParseFinite (test/fuzz/targets.tsv).
func FuzzParseFinite(f *testing.F) {
	for _, s := range []string{"1.5", "NaN", "-Inf", "1e999", "0x1p-2", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := number.ParseFinite(s)
		if err != nil {
			if g, perr := strconv.ParseFloat(s, 64); perr == nil && number.Finite(g) {
				t.Fatalf("ParseFinite(%q) refused the finite %v: %v", s, g, err)
			}
			if errors.Is(err, number.ErrNotFinite) && v != 0 {
				t.Fatalf("ParseFinite(%q) = %v with %v", s, v, err)
			}
			return
		}
		if !number.Finite(v) {
			t.Fatalf("ParseFinite(%q) = %v", s, v)
		}
		if again, err := number.ParseFinite(strconv.FormatFloat(v, 'g', -1, 64)); err != nil || again != v {
			t.Fatalf("ParseFinite(%q) = %v does not read back: %v %v", s, v, again, err)
		}
	})
}
