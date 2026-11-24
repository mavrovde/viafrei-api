package places

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// KreisFamily is the family whose districts name a place's Kreis, as the
// platform's place search names it (the open BKG district rows).
const KreisFamily = "bkg_gvisys"

// inAKreis are the kinds that lie in a Kreis, below the Land itself.
var inAKreis = []Kind{"municipality", "locality", "station_area", KindJunction}

var gemeindeKey = regexp.MustCompile(`^[0-9]{8}$`)

// kreisNamed is a district name already said as a Kreis, folded as the
// platform folds names: it is said as it is, never "Kreis …".
var kreisNamed = regexp.MustCompile(`kreis|^region |^stadtverband |^stadtregion |^stadteregion `)

// gemeinde is the eight-digit Gemeinde key of a place of a kind that lies in
// a Kreis, below the Land itself (a city-state's 11000000 names none); nil
// else.
func gemeinde(kind Kind, key *string) *string {
	if !slices.Contains(inAKreis, kind) || key == nil || !gemeindeKey.MatchString(*key) || (*key)[2:] == "000000" {
		return nil
	}
	return key
}

// KreisKey is the five-digit key whose district name KreisOf needs for a
// place, or nil when it needs none: a kreisfreie Stadt is said as one
// without a name, a place with no Gemeinde key lies in no Kreis that can be
// named.
func KreisKey(kind Kind, key *string) *string {
	g := gemeinde(kind, key)
	if g == nil || strings.HasSuffix(*g, "000") && kind == "municipality" {
		return nil
	}
	k := (*g)[:5]
	return &k
}

// KreisOf is the Kreis a place lies in, in German, as the platform's place
// search states kreis: "kreisfreie Stadt" for a town that is one, else
// from the name of its district (names, by five-digit key): "kreisfreie
// Stadt X" for a place in one, "Kreis X", or the name as it is when it says
// Kreis or Region itself. Nil when the place lies in no Kreis that can be
// named.
func KreisOf(kind Kind, key *string, names map[string]string) *string {
	g := gemeinde(kind, key)
	if g == nil {
		return nil
	}
	free := strings.HasSuffix(*g, "000")
	if free && kind == "municipality" {
		w := "kreisfreie Stadt"
		return &w
	}
	name, ok := names[(*g)[:5]]
	if !ok {
		return nil
	}
	w := "Kreis " + name
	switch {
	case free:
		w = "kreisfreie Stadt " + name
	case kreisNamed.MatchString(normalizeName(name)):
		w = name
	}
	return &w
}

// normalizeName folds a name as the platform's name fold does: lower case,
// ß as ss, no diacritic, each run of other characters one space, trimmed.
func normalizeName(raw string) string {
	s := strings.ReplaceAll(strings.ToLower(raw), "ß", "ss")
	s, _, _ = transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}
