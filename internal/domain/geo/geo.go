// Package geo holds the positions of the domain: a WGS 84 point, its text
// form in a query and its rounding in an answer.
package geo

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Point is a WGS 84 position in decimal degrees.
type Point struct {
	Lat, Lon float64
}

// ErrPoint is the error of every position that cannot be read.
var ErrPoint = errors.New("geo: not a position")

// ParsePoint reads "lat,lon" in decimal degrees: exactly two numbers
// separated by one comma, no spaces, a latitude in [-90, 90] and a longitude
// in [-180, 180]. Nothing is clamped: a value outside is an error.
func ParsePoint(s string) (Point, error) {
	lat, lon, ok := strings.Cut(s, ",")
	if !ok {
		return Point{}, fmt.Errorf("%w: write it as lat,lon in decimal degrees", ErrPoint)
	}
	la, err := coordinate(lat, 90)
	if err != nil {
		return Point{}, fmt.Errorf("%w: the latitude %w", ErrPoint, err)
	}
	lo, err := coordinate(lon, 180)
	if err != nil {
		return Point{}, fmt.Errorf("%w: the longitude %w", ErrPoint, err)
	}
	return Point{Lat: la, Lon: lo}, nil
}

var errNotDecimal = errors.New("is not a decimal number")

// coordinate reads one decimal number within [-limit, limit]. Only digits,
// one optional leading minus and one optional decimal point followed by at
// least one digit are accepted, so the other forms strconv reads (NaN, Inf,
// exponents, hex, underscores, a sign of plus) are refused.
func coordinate(s string, limit float64) (float64, error) {
	digits := strings.TrimPrefix(s, "-")
	whole, frac, dot := strings.Cut(digits, ".")
	if !decimalDigits(whole) || (dot && !decimalDigits(frac)) {
		return 0, errNotDecimal
	}
	v, _ := strconv.ParseFloat(s, 64) // the form above is always a valid float
	if math.Abs(v) > limit {
		return 0, fmt.Errorf("is outside [-%g, %g]", limit, limit)
	}
	return v, nil
}

// decimalDigits reports whether s is one or more ASCII digits.
func decimalDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Decimals is the precision of a position in an answer (ENV-011): six
// decimals, about 0.1 m.
const Decimals = 6

// Rounded returns the point rounded to Decimals.
func (p Point) Rounded() Point {
	return Point{Lat: Round(p.Lat, Decimals), Lon: Round(p.Lon, Decimals)}
}

// Round rounds v half away from zero to the given number of decimals.
func Round(v float64, decimals int) float64 {
	f := math.Pow10(decimals)
	return math.Round(v*f) / f
}
