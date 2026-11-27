package places

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// diacritics is a new chain per call: a transform.Chain keeps its buffers in
// itself, so one shared by concurrent requests cut a name with another's
// bounds (a panic, answered 500, under the benchmark's concurrency).
func diacritics() transform.Transformer {
	return transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
}

// NameFold is the platform's search form of a name, the form contract.poi
// and contract.address keep in their fold columns: lower case, ß as ss,
// every diacritic dropped (ä → a), every run of other characters one space.
func NameFold(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ß", "ss")
	s, _, _ = transform.String(diacritics(), s)
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// StreetFold is the platform's search form of a street: its NameFold with
// "str" written out as "strasse", the words run together.
func StreetFold(s string) string {
	words := strings.Fields(NameFold(s))
	for i, w := range words {
		if w == "str" || strings.HasSuffix(w, "str") {
			words[i] = strings.TrimSuffix(w, "str") + "strasse"
		}
	}
	return strings.Join(words, "")
}

// HouseNumberFold is a house number's search form: lower case, no space.
func HouseNumberFold(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), "")
}

// AddressQuery is a typed address read as the platform reads it: a street,
// a house number, and a postcode or a town or both.
type AddressQuery struct {
	Street, HouseNumber string
	Postcode, Place     string
	Limit               int
}

var (
	streetAndNumber  = regexp.MustCompile(`(?i)^(?P<street>.*[\p{L}.])\s+(?P<number>\d{1,4}\s*[a-z]?)$`)
	streetNumberRest = regexp.MustCompile(`(?i)^(?P<street>.*?[\p{L}.])\s+(?P<number>\d{1,4}\s*[a-z]?)\b[,\s]+(?P<rest>.+)$`)
	postcode         = regexp.MustCompile(`\b\d{5}\b`)
)

// ParseAddress reads "Street 12, 12345 Town" as the platform does, or
// returns false: a street and a number with no postcode and no town is the
// one shape refused, since such an address exists in thousands of towns.
func ParseAddress(raw string) (AddressQuery, bool) {
	in := strings.TrimSpace(raw)
	var segs []string
	for _, s := range strings.Split(in, ",") {
		if s = strings.TrimSpace(s); s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) > 1 {
		for i, s := range segs {
			if m := streetAndNumber.FindStringSubmatch(s); m != nil {
				rest := append(append([]string{}, segs[:i]...), segs[i+1:]...)
				return build(m[1], m[2], strings.Join(rest, " "))
			}
		}
	}
	if m := streetNumberRest.FindStringSubmatch(in); m != nil {
		return build(m[1], m[2], m[3])
	}
	return AddressQuery{}, false
}

func build(street, number, rest string) (AddressQuery, bool) {
	q := AddressQuery{Street: strings.TrimSpace(street), HouseNumber: strings.TrimSpace(number), Postcode: postcode.FindString(rest)}
	q.Place = strings.Join(strings.Fields(postcode.ReplaceAllString(rest, "")), " ")
	return q, q.Place != "" || q.Postcode != ""
}

// Address is an address OpenStreetMap maps.
type Address struct {
	ID          string
	Street      *string
	HouseNumber *string
	Postcode    *string
	Place       *string
	Land        *string
	Location    geo.Point
	DataAsOf    *time.Time
	// DistanceM is the distance from the position asked; nil when none was.
	DistanceM *int
	Family    string
}

// The reaches of a description of a position, as on the platform: the
// nearest address within 5 km, municipality within 50 km, district within
// 100 km, and, with no district, the nearest Land within 250 km.
const (
	AddressReachM      = 5000
	MunicipalityReachM = 50000
	DistrictReachM     = 100000
	StateReachM        = 250000
)

// A position may lie outside Germany, as the platform judges it, when no
// address is within AbroadNearestM and at least AbroadEmptyRun neighbouring
// of AbroadSectors wedges of AbroadScanM around it hold no address.
const (
	AbroadNearestM = 1500
	AbroadScanM    = 15000
	AbroadSectors  = 8
	AbroadEmptyRun = 4
)

// landAreas are the Land keys of the names Germany's Land areas are held
// under (the OpenStreetMap extracts' names of the Länder).
var landAreas = map[string]string{
	"schleswig-holstein": "01", "hamburg": "02", "niedersachsen": "03", "bremen": "04",
	"nordrhein-westfalen": "05", "hessen": "06", "rheinland-pfalz": "07", "baden-wuerttemberg": "08",
	"bayern": "09", "saarland": "10", "berlin": "11", "brandenburg": "12",
	"mecklenburg-vorpommern": "13", "sachsen": "14", "sachsen-anhalt": "15", "thueringen": "16",
}

// LandOfArea is the Land key of the Land area held under name; false for a
// name that is no Land's.
func LandOfArea(name string) (string, bool) {
	key, ok := landAreas[name]
	return key, ok
}

// LandArea is the Land area nearest a position: the name it is held under,
// its family, whether it covers the position, and the stated distance to it
// (0 inside).
type LandArea struct {
	Name      string
	Family    string
	Covers    bool
	DistanceM int
}

// SectorWedges returns n wedges of radiusM around at, clockwise from north,
// as WKT polygons: the platform's sectors, each arc of six steps on a flat
// projection of the position's latitude.
func SectorWedges(at geo.Point, radiusM float64, n int) []string {
	const perDegLat, steps = 111320.0, 6
	perDegLon := perDegLat * math.Cos(at.Lat*math.Pi/180)
	centre := fmt.Sprintf("%.6f %.6f", at.Lon, at.Lat)
	out := make([]string, n)
	for i := range n {
		pts := []string{centre}
		// A range, not j++: the mutation run's j-- of a counted loop appended
		// without end and took the runner's memory (the 0.3.1 release).
		for j := range steps + 1 {
			az := (float64(i) + float64(j)/steps) * 2 * math.Pi / float64(n)
			pts = append(pts, fmt.Sprintf("%.6f %.6f", at.Lon+radiusM*math.Sin(az)/perDegLon, at.Lat+radiusM*math.Cos(az)/perDegLat))
		}
		out[i] = "POLYGON((" + strings.Join(append(pts, centre), ",") + "))"
	}
	return out
}

// LongestEmptyRun is the longest run of neighbouring sectors without a hit,
// the circle closed.
func LongestEmptyRun(hits []bool) int {
	best, run := 0, 0
	for range 2 {
		for _, h := range hits {
			run++
			if h {
				run = 0
			}
			best = max(best, run)
		}
	}
	return min(best, len(hits))
}

// LocationQuery asks what lies at a position.
type LocationQuery struct {
	At geo.Point
}

// Location is what lies at a position: each fact independent, nil when
// none is within its reach.
type Location struct {
	Address      *Address
	Municipality *Place
	District     *Place
	// State is the Land of the district; with no district, the Land whose
	// area is nearest (its distance that of the area), or, with no Land
	// area held, the nearest Land's point.
	State *Place
	// StateArea is the family of the Land area State was chosen by; empty
	// when it was not.
	StateArea string
	// MayLieAbroad is the judgement that the position may lie outside
	// Germany: no Land area covers it, or, with no Land area held, the
	// platform's address scan (AbroadSectors). AbroadFamily is then the
	// family of the areas, or of the nearest address.
	MayLieAbroad bool
	AbroadFamily string
	// Gemeinde is the Gemeinde the position lies in; nil when the position
	// may lie abroad or none is read.
	Gemeinde *Gemeinde
	// Junction is the nearest motorway junction within JunctionReachM and
	// JunctionFrom the source it came from; nil when none.
	Junction     *Junction
	JunctionFrom string
}

// JunctionReachM is how far the nearest junction a location description
// names may lie, as today's (AROUND_MAX_M.junction).
const JunctionReachM = 100_000
