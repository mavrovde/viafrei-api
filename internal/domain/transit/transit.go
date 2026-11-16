// Package transit is the public-transport context: the stations a client
// finds by name or near a position.
package transit

import (
	"slices"
	"strings"
	"unicode"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// Mode is a mode of transport a station is served by.
type Mode string

// Other is the mode of a stored code the service cannot map (ENV-051).
const Other Mode = "other"

// Modes lists every mode an answer can carry.
var Modes = []Mode{"long_distance_rail", "regional_rail", "suburban_rail", "subway", "tram", "bus", "ferry", Other}

// MapMode maps a stored mode code.
func MapMode(raw string) Mode {
	if i := slices.Index(Modes[:len(Modes)-1], Mode(raw)); i >= 0 {
		return Modes[i]
	}
	return Other
}

// MapModes maps stored mode codes, each mode once, in the order of Modes.
func MapModes(raw []string) []Mode {
	out := []Mode{}
	for _, m := range Modes {
		if slices.ContainsFunc(raw, func(r string) bool { return MapMode(r) == m }) {
			out = append(out, m)
		}
	}
	return out
}

// Station is one station (a stop place).
type Station struct {
	ID       string
	Name     string
	Location geo.Point
	// NationalCode is the national stop code; nil when none is recorded.
	NationalCode *string
	// RegionCode is the official code of the district it lies in; nil when
	// unknown.
	RegionCode *string
	Modes      []Mode
	// DistanceM is the distance from the asked position; nil when the
	// question named no position.
	DistanceM *int
	Family    string
}

// Query is a station search: by name (Text), near a position (At), or both.
type Query struct {
	// Text is what the client typed; empty for none.
	Text string
	// At is the position; nil for none.
	At      *geo.Point
	RadiusM int
	Limit   int
}

// Fold is the search form of a text, the form contract.stop_place keeps in
// search_fold: lower case, umlauts and ß spelled out (ä → ae, ß → ss), every
// other diacritic dropped, and everything but letters and digits removed.
// "Köln Hbf", "koeln hbf" and "KOELN-HBF" fold alike.
func Fold(s string) string {
	s = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "ae", "Ö", "oe", "Ü", "ue", "ß", "ss", "ẞ", "ss").Replace(s)
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteString(strip(r))
		}
	}
	return b.String()
}

// strip is a letter without its diacritic, for the Latin letters a German
// station name carries; any other letter is kept as it is.
func strip(r rune) string {
	for base, marked := range map[string]string{
		"a": "àáâãåāăą", "c": "çćčĉ", "e": "èéêëēėęě", "i": "ìíîïīį", "n": "ñńň",
		"o": "òóôõøō", "s": "śšş", "u": "ùúûūůű", "y": "ýÿ", "z": "źżž", "l": "ł", "r": "ř", "g": "ğ", "d": "ď", "t": "ť",
	} {
		if strings.ContainsRune(marked, r) {
			return base
		}
	}
	return string(r)
}
