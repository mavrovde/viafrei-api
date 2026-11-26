// Package transit is the public-transport context: the stations a client
// finds by name or near a position.
package transit

import (
	"errors"
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
var Modes = []Mode{"rail", "sbahn", "subway", "tram", "bus", "ferry", Other}

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
	// PlacedBy is the family that places the station in its district and
	// Land (contract.stop_place's placement_source_ref); nil when none does.
	PlacedBy *string
	// InTown marks a station found as a stop in the town the text names
	// (TownSplits): its district placed it there.
	InTown bool
	// TownBy is the family whose places named the town the station was
	// found in (the gazetteer's Gemeinden), credited with it; nil when the
	// town was a district's own name.
	TownBy *string
	// RegisterName is the station's name in DB's station register, of the
	// family RegisterBy; nil for a stop the register does not list.
	RegisterName *string
	RegisterBy   *string
	// EVA is the railway's main station number (DB StaDa); nil for a stop
	// that is no DB station. EVANumbers are every number of the station, the
	// main one first: a station of several parts files each part's trains
	// under the part's own number (G2).
	EVA        *string
	EVANumbers []string
	// StopIDs are the stops a board of the station reads: the station
	// first, then its stop points (FamilyOf); nil when not read.
	StopIDs []string
}

// Query is a station search: by name (Text), near a position (At), or both.
type Query struct {
	// Text is what the client typed; empty for none.
	Text string
	// Code is a station code (EVA number or national stop code) the
	// station must carry; empty for none.
	Code string
	// At is the position; nil for none.
	At      *geo.Point
	RadiusM int
	Limit   int
	// Railway keeps the railway stations only, those of the DB station
	// register (an EVA number): what the platform's nearby search lists as
	// a station.
	Railway bool
	// Area is an official region key (two digits a Land, five a Kreis) the
	// station's district code must start with; empty for any.
	Area string
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

// MainStationSpellings are the folded spellings under which a register may
// hold the main station a text names, as the platform states them
// (germanStationQuery and mainStationSpellings, #502 and #526). The text is
// first read as the gazetteer spells it: the English city names (Munich,
// Cologne, Nuremberg, Hanover, Brunswick, Frankfort) become the German, and
// the main-station words ("Hauptbahnhof", "main station", "central
// station", "main railway station", "central railway station", "main st",
// "central") become "Hbf", each phrase whole and the longest first. DB
// writes almost every Hauptbahnhof "<town> Hbf" and the timetable some
// "<town> Hauptbahnhof" ("S+U Berlin Hauptbahnhof"), so "Berlin Hbf",
// "Hauptbahnhof, Berlin" and "Berlin main station" each give "berlinhbf"
// and "berlinhauptbahnhof", "Cologne main station" "koelnhbf" and
// "koelnhauptbahnhof". None for a text that names no main station
// ("Berlin Ostbahnhof", "Kölner Dom, Köln"), or no town beside it ("Hbf"
// alone stays the list it is).
func MainStationSpellings(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var place strings.Builder
	main := false
	for i := 0; i < len(words); {
		if n := mainStationPhraseAt(words[i:]); n > 0 {
			main = true
			i += n
			continue
		}
		w := words[i]
		if german, ok := englishCityNames[w]; ok {
			w = german
		}
		place.WriteString(Fold(w))
		i++
	}
	if !main || place.Len() == 0 {
		return nil
	}
	return []string{place.String() + "hbf", place.String() + "hauptbahnhof"}
}

// RiverForm is the text with the words that join a town to its river left
// out ("am", "an der", "a. d."), as the timetables write the river, in
// brackets after the town: "Frankfurt am Main" gives "frankfurt main",
// which folds as "Frankfurt (Main) Hbf" starts, "Halle an der Saale" gives
// "halle saale". "" when the text holds no such words between two others.
func RiverForm(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := make([]string, 0, len(words))
	found := false
	for i := 0; i < len(words); i++ {
		n := 0
		switch {
		case words[i] == "am":
			n = 1
		case i+1 < len(words) && (words[i] == "an" && words[i+1] == "der" || words[i] == "a" && words[i+1] == "d"):
			n = 2
		}
		if n > 0 && i > 0 && i+n < len(words) {
			found = true
			i += n - 1
			continue
		}
		out = append(out, words[i])
	}
	if !found {
		return ""
	}
	return strings.Join(out, " ")
}

// englishCityNames are the English names of German cities the platform
// reads as the German (ENGLISH_FORMS).
var englishCityNames = map[string]string{
	"munich": "münchen", "cologne": "köln", "nuremberg": "nürnberg",
	"hanover": "hannover", "brunswick": "braunschweig", "frankfort": "frankfurt",
}

// mainStationPhrases are the words that name a main station
// (MAIN_STATION_WORDS, and "hbf" itself), the most words first, so a phrase
// is taken whole before a part of it.
var mainStationPhrases = [][]string{
	{"central", "railway", "station"}, {"main", "railway", "station"},
	{"central", "station"}, {"main", "station"}, {"main", "st"},
	{"hauptbahnhof"}, {"central"}, {"hbf"},
}

// mainStationPhraseAt is the number of words of the main-station phrase the
// words start with, 0 for none.
func mainStationPhraseAt(words []string) int {
	for _, p := range mainStationPhrases {
		if len(p) <= len(words) && slices.Equal(words[:len(p)], p) {
			return len(p)
		}
	}
	return 0
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

// ErrNotFound is returned for a station id no permitted family holds.
var ErrNotFound = errors.New("transit: no such station")

// FamilyOf is the family a station's board reads, as the platform's
// departure board states stop.stopIds: the station, then each of its stop
// points once, in their order (a stop that is its own stop point is listed
// once).
func FamilyOf(station string, points []string) []string {
	out := []string{station}
	for _, p := range points {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}
