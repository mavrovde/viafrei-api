// Package number reads decimal numbers from text. strconv.ParseFloat also
// accepts NaN and the infinities ("NaN", "Inf", "infinity", any case and
// sign), which no quantity the service reads may be: NaN compares false
// with every bound, so a range check alone lets it through, and the JSON
// encoder cannot write it (#149, #150). Every float the service parses from
// a request, a configuration or a registry goes through ParseFinite.
package number

import (
	"errors"
	"math"
	"strconv"
)

// ErrNotFinite is the error for NaN and the infinities as written. An
// overflow (1e999) is strconv's own range error instead, so a caller can
// tell a value written non-finite from one too large to read.
var ErrNotFinite = errors.New("not a finite number")

// ParseFinite reads s as a 64-bit float and refuses NaN and ±Inf
// (ErrNotFinite), and an overflow such as 1e999 (strconv's ErrRange).
func ParseFinite(s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if !Finite(f) {
		return 0, ErrNotFinite
	}
	return f, nil
}

// Finite reports whether f is neither NaN nor an infinity.
func Finite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
