package number_test

import (
	"errors"
	"math"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/number"
)

func TestParseFinite(t *testing.T) {
	for _, v := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "Infinity", "-infinity"} {
		if f, err := number.ParseFinite(v); !errors.Is(err, number.ErrNotFinite) || f != 0 {
			t.Errorf("%s: %v %v", v, f, err)
		}
	}
	for _, v := range []string{"", "x", "+NaN", "1e999", "1,5"} {
		if _, err := number.ParseFinite(v); err == nil || errors.Is(err, number.ErrNotFinite) {
			t.Errorf("%q: %v", v, err)
		}
	}
	for v, want := range map[string]float64{"0": 0, "-1.5": -1.5, "1e30": 1e30, "0.25": 0.25} {
		if f, err := number.ParseFinite(v); err != nil || f != want {
			t.Errorf("%s: %v %v", v, f, err)
		}
	}
}

func TestFinite(t *testing.T) {
	for f, want := range map[float64]bool{0: true, -1e300: true, math.Inf(1): false, math.Inf(-1): false} {
		if number.Finite(f) != want {
			t.Errorf("%v", f)
		}
	}
	if number.Finite(math.NaN()) {
		t.Error("NaN")
	}
}
