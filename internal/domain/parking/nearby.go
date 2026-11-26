package parking

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// What is near a listed site, the OpenStreetMap car parks around a place,
// and the city's coach places and on-street bays: the platform's parking
// search's own blocks beside its list (its nearby rule, its OpenStreetMap car
// parks and its road context).

// OSMFamily is the family of OpenStreetMap's places, the nearby amenities and
// the car parks among them.
const OSMFamily = "osm"

const (
	// NearbyCityM is the radius around a city car park, a park and ride or
	// another site the platform reads amenities in.
	NearbyCityM = 500
	// NearbyMotorwayM is the radius around any other site, and around every
	// site along a motorway.
	NearbyMotorwayM = 300
	// NearbyPerSiteMax bounds the amenities named per site, one per amenity.
	NearbyPerSiteMax = 5
	// OSMOverfetch is how many more OpenStreetMap car parks are read than
	// listed, so the folded list still fills its limit.
	OSMOverfetch = 4
	// OSMTwinM: an OpenStreetMap car park this near a listed site is that
	// site.
	OSMTwinM = 75
	// OSMNamedTwinM: an OpenStreetMap car park this near a listed site of
	// the same name is that site.
	OSMNamedTwinM = 200
	// OSMSameSiteM: two OpenStreetMap car parks of one name this near are
	// one car park.
	OSMSameSiteM = 150
)

// NearbyAmenities are the amenities read near a site, in the platform's
// words.
var NearbyAmenities = []string{"toilet", "shower", "restaurant", "snack", "cafe", "shop", "drinking_water"}

// cityKinds are the kinds under the city radius: the platform's car_park,
// park_and_ride and other.
var cityKinds = []Kind{KindGarage, KindParkAndRide, KindOther}

// NearbyRadiusM is the radius a site's amenities are read in.
func NearbyRadiusM(k Kind, alongRoad bool) int {
	if alongRoad || !slices.Contains(cityKinds, k) {
		return NearbyMotorwayM
	}
	return NearbyCityM
}

// NearbyRow is one amenity read near the site numbered Site (from 1, in the
// order the sites were asked): the nearest of its amenity.
type NearbyRow struct {
	Site        int
	Amenity     string
	Name        *string
	Metres      int
	At          geo.Point
	ExtractDate *string
}

// Nearby is an amenity named near a site.
type Nearby struct {
	Amenity     string
	Name        string
	Metres      int
	At          geo.Point
	ExtractDate *string
}

// NearbyOf names the amenities near the site numbered site: nearest first
// (then by amenity), one per amenity, none its own publisher states it has,
// none without a name, at most NearbyPerSiteMax; the platform's nearbyOsmOf.
func NearbyOf(rows []NearbyRow, site int, stated Stated) []Nearby {
	mine := slices.DeleteFunc(slices.Clone(rows), func(r NearbyRow) bool { return r.Site != site })
	slices.SortStableFunc(mine, func(a, b NearbyRow) int {
		if a.Metres != b.Metres {
			return a.Metres - b.Metres
		}
		return strings.Compare(a.Amenity, b.Amenity)
	})
	out := []Nearby{}
	for _, r := range mine {
		if !slices.Contains(NearbyAmenities, r.Amenity) || slices.Contains(stated.Present, r.Amenity) ||
			slices.ContainsFunc(out, func(n Nearby) bool { return n.Amenity == r.Amenity }) {
			continue
		}
		if r.Name == nil || strings.TrimSpace(*r.Name) == "" {
			continue
		}
		out = append(out, Nearby{Amenity: r.Amenity, Name: strings.TrimSpace(*r.Name), Metres: r.Metres, At: r.At, ExtractDate: r.ExtractDate})
		if len(out) >= NearbyPerSiteMax {
			break
		}
	}
	return out
}

// OSMParkAndRidePattern keeps the OpenStreetMap car parks a park and ride
// question lists: those named P+R or park and ride.
const OSMParkAndRidePattern = `(\mp\s*[+&]\s*r\M)|(\mpark\s*(\+|&|and|und|n)\s*ride\M)`

// OSMRestrictedNamePattern leaves out the OpenStreetMap car parks whose name
// says they are not for a visitor's car.
const OSMRestrictedNamePattern = `(bewohner|anwohner|behinderten|mitarbeiter|personal|privat|dienstwagen|dienstfahrzeug|lehrer|reserviert|nur für|feuerwehr|polizei|taxi|wohnmobil|reisebus|bus ?parkplatz|lkw|fahrrad|\mrad(park|station|garage|abstell)|\mbike|\mvelo\M|jelbi|sharing|\mhunde(park|auslauf|wiese))`

// OSMQuery is whether q lists OpenStreetMap's car parks, and the pattern
// their name must match (nil: any name): a question at a place for any kind,
// a car park or a park and ride; never along a road, for a rest area or for a
// lorry.
func OSMQuery(q Query) (pattern *string, ok bool) {
	if q.Road != "" {
		return nil, false
	}
	switch q.Kind {
	case "", "car_park":
		return nil, true
	case "park_and_ride":
		p := OSMParkAndRidePattern
		return &p, true
	}
	return nil, false
}

// OSMCarPark is a car park OpenStreetMap maps.
type OSMCarPark struct {
	ID          string
	Name        *string
	Operator    *string
	Street      *string
	HouseNumber *string
	Postcode    *string
	Place       *string
	At          geo.Point
	ExtractDate *string
	DistanceM   float64
}

// FoldOSM is the OpenStreetMap car parks as the platform lists them, from
// rows nearest first: one per name within OSMSameSiteM by the name key, none
// that is a listed site (within OSMTwinM, or within OSMNamedTwinM under the
// same parking name), one per parking name within OSMSameSiteM, at most
// limit.
func FoldOSM(rows []OSMCarPark, held []Site, limit int) []OSMCarPark {
	type keyed struct {
		row OSMCarPark
		key string
	}
	var named []keyed
	for _, r := range rows {
		key := ""
		if r.Name != nil {
			key = nameKey(*r.Name)
		}
		if key == "" || !slices.ContainsFunc(named, func(k keyed) bool { return k.key == key && geo.MetresBetween(k.row.At, r.At) <= OSMSameSiteM }) {
			named = append(named, keyed{r, key})
		}
	}
	out := []OSMCarPark{}
	for _, k := range named {
		r := k.row
		if slices.ContainsFunc(held, func(h Site) bool {
			d := metresBetween(h.Location, r.At)
			return d <= OSMTwinM || (d <= OSMNamedTwinM && SameParkingName(h.Name, r.Name))
		}) {
			continue
		}
		if slices.ContainsFunc(out, func(k OSMCarPark) bool {
			return SameParkingName(k.Name, r.Name) && metresBetween(k.At, r.At) <= OSMSameSiteM
		}) {
			continue
		}
		out = append(out, r)
	}
	return out[:min(len(out), limit)]
}

// OSMDataAsOf is the newest extract date of the car parks listed; nil when
// none is listed or none states one.
func OSMDataAsOf(rows []OSMCarPark) *string {
	var out *string
	for _, r := range rows {
		if r.ExtractDate != nil && (out == nil || *r.ExtractDate > *out) {
			out = r.ExtractDate
		}
	}
	return out
}

var notLetterOrDigit = regexp.MustCompile(`[^\p{L}\p{N}]+`)

var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")

// nameKey is a name as a comparison key, as the platform's nameKey.
func nameKey(name string) string {
	s := umlauts.Replace(strings.ToLower(strings.TrimSpace(name)))
	return strings.TrimSpace(notLetterOrDigit.ReplaceAllString(s, " "))
}

// metresBetween is the twin rule's own distance: a haversine on a sphere of
// 6 371 000 m, as the platform's osm-twins.
func metresBetween(a, b geo.Point) float64 {
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 2 * 6_371_000 * math.Asin(math.Sqrt(h))
}

var (
	genericWords = setOf("parkhaus", "parkhaeuser", "parkplatz", "parkplaetze", "parkgarage", "tiefgarage", "garage", "hochgarage", "parkdeck", "parkpalette",
		"parken", "parking", "park", "ride", "pr", "p", "und", "am", "an", "der", "die", "das", "den", "dem", "zum", "zur", "im", "in", "bei", "beim",
		"the", "of", "car", "lot", "gmbh", "center", "centre", "zentrum", "bf", "bahnhof", "hbf", "station", "strasse", "str", "platz", "weg") //nolint:misspell // the platform's stop word, spelled as names spell it
	compassWords = setOf("nord", "sued", "ost", "west", "north", "south", "east", "mitte", "oben", "unten")
	roman        = regexp.MustCompile(`^(?:i|ii|iii|iv|v|vi|vii|viii|ix|x)$`)
	nameSuffix   = regexp.MustCompile(`(garage|platz|strasse|str)$`)
	parkAndRide  = regexp.MustCompile(`\bp\s*[+&]\s*r\b`)
	digit        = regexp.MustCompile(`\d`)
	leadingZeros = regexp.MustCompile(`(\D|^)0+(\d)`)
)

func setOf(words ...string) map[string]bool {
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}

func foldWords(name string) []string {
	s := umlauts.Replace(strings.ToLower(name))
	s = parkAndRide.ReplaceAllString(s, " pr ")
	return strings.Fields(notLetterOrDigit.ReplaceAllString(s, " "))
}

func isMark(w string) bool { return digit.MatchString(w) || roman.MatchString(w) || compassWords[w] }

// nameWords are a parking name's own words: no generic or marking word, none
// shorter than three letters, a type suffix cut where four letters stay.
func nameWords(name string) map[string]bool {
	out := map[string]bool{}
	for _, w := range foldWords(name) {
		if genericWords[w] || isMark(w) || utf8.RuneCountInString(w) < 3 {
			continue
		}
		cut := nameSuffix.ReplaceAllString(w, "")
		if utf8.RuneCountInString(cut) >= 4 {
			out[cut] = true
		} else {
			out[w] = true
		}
	}
	return out
}

// nameMarks are a parking name's numbers, roman numerals and compass words.
func nameMarks(name string) map[string]bool {
	out := map[string]bool{}
	for _, w := range foldWords(name) {
		if isMark(w) {
			out[leadingZeros.ReplaceAllString(w, "${1}${2}")] = true
		}
	}
	return out
}

func subset(a, b map[string]bool) bool {
	for x := range a {
		if !b[x] {
			return false
		}
	}
	return true
}

// SameParkingName is whether two names name one car park, as the platform's
// sameParkingName: the own words of one are among the other's, and their
// marks are the same.
func SameParkingName(a, b *string) bool {
	if a == nil || b == nil {
		return false
	}
	wa, wb := nameWords(*a), nameWords(*b)
	if len(wa) == 0 || len(wb) == 0 || (!subset(wa, wb) && !subset(wb, wa)) {
		return false
	}
	ma, mb := nameMarks(*a), nameMarks(*b)
	return len(ma) == len(mb) && subset(ma, mb)
}

// ContextRule is one kind of the road context a parking question at a place
// reads: the radius, how many are named, the variant named first, whether
// one is named per name, and whether a row's parts inside the radius are
// counted.
type ContextRule struct {
	Kind       string
	RadiusM    float64
	Limit      int
	Prefer     *string
	OnePerName bool
	CountParts bool
	Flag       *string
}

var preferParking = "parking"

// ContextRules are the platform's road context kinds of a parking question:
// the coach places within 2 km, the on-street bays within 300 m.
var ContextRules = []ContextRule{
	{Kind: "coach_parking", RadiusM: 2000, Limit: 3, Prefer: &preferParking},
	{Kind: "street_parking", RadiusM: 300, Limit: 3, CountParts: true},
}

// ContextAsked is whether q reads the road context: a question at a place
// for every kind, with no free-space or amenity filter.
func ContextAsked(q Query) bool {
	return q.Road == "" && q.Kind == "" && q.FreeMin == 0 && len(q.Amenities) == 0
}

// ContextRow is one road context row near the place: of its kind and family,
// with the publisher's facts; Total, Variants and Flagged count every row of
// its kind in the radius.
type ContextRow struct {
	Kind       string
	Family     string
	ID         string
	Variant    *string
	Name       *string
	Ref        *string
	Detail     *string
	Facts      map[string]any
	At         geo.Point
	DistanceM  float64
	LastSeenAt time.Time
	Total      int
	Variants   map[string]int
	PartsIn    int
	Flagged    *int
}

// OccupancyCaveatFamilies are the families whose publisher warns about its
// live figures: the warning (its register condition) is shown only with a
// figure of theirs.
var OccupancyCaveatFamilies = []string{"hh_parkhaeuser", "park_dortmund_portal"}

// Around is what a parking answer carries beside its list: per listed site
// the amenities near it, the OpenStreetMap car parks (nil when not asked)
// and the road context (nil when not asked).
type Around struct {
	Nearby      [][]Nearby
	OSMCarParks []OSMCarPark
	OSMAsked    bool
	Context     []ContextRow
	ContextRead bool
}

// AroundQuery is what the store reads beside a parking list: the amenities
// within Radii[i] of each Sites[i] and the car parks of OpenStreetMap, both
// of the families OSMFamilies (none read when empty); and the road context
// of the families ContextFamilies near At (not read when Context is unset).
type AroundQuery struct {
	At              geo.Point
	Now             time.Time
	Sites           []geo.Point
	Radii           []float64
	OSMFamilies     []string
	CarParks        bool
	CarParkRadiusM  float64
	Pattern         *string
	CarParkFetch    int
	Context         bool
	ContextFamilies []string
}

// AroundRead is what an AroundQuery read.
type AroundRead struct {
	Nearby   []NearbyRow
	CarParks []OSMCarPark
	Context  []ContextRow
}
