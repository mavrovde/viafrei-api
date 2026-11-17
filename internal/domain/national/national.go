// Package national holds the national picture: the counts and boards the
// site's home tiles, its live map and its national pages state for the whole
// of Germany, each read from the families of one domain.
package national

// The sections of the national picture.
const (
	SectionSituations  = "situations"  // road situations in force, by kind
	SectionStates      = "states"      // road situations in force, by Land
	SectionFuel        = "fuel"        // current prices, by grade
	SectionCharging    = "charging"    // charging sites and points, by family
	SectionParking     = "parking"     // parking sites, by kind
	SectionFacilities  = "facilities"  // station lifts and escalators, by kind and state
	SectionPunctuality = "punctuality" // regional punctuality, by area
	SectionWarnings    = "warnings"    // weather warnings in force, by level
)

// Domains are the domains the national picture reads, in answer order.
var Domains = []string{"roads", "energy", "parking", "transit", "weather"}

// Row is one figure of a section, of one family.
type Row struct {
	Section string
	// Key is what the figure counts: a kind, a Land, a grade, an area.
	Key    string
	Name   *string
	Family string
	// N is the count; M a second count (charge points, stations, late
	// trips, areas), nil when the section has none.
	N int
	M *int
	// Low and Mid are a grade's cheapest and median current price, or an
	// area's median delay in seconds (Mid).
	Low, Mid *float64
}

// Families are the families of rows, each once, in order of first
// appearance.
func Families(rows []Row) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		if !seen[r.Family] {
			seen[r.Family] = true
			out = append(out, r.Family)
		}
	}
	return out
}
