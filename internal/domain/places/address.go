package places

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

var diacritics = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// NameFold is the platform's search form of a name, the form contract.poi
// and contract.address keep in their fold columns: lower case, ß as ss,
// every diacritic dropped (ä → a), every run of other characters one space.
func NameFold(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ß", "ss")
	s, _, _ = transform.String(diacritics, s)
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
// 100 km.
const (
	AddressReachM      = 5000
	MunicipalityReachM = 50000
	DistrictReachM     = 100000
)

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
	// State is the Land of the district.
	State *Place
}
