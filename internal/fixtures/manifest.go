package fixtures

import (
	"fmt"
	"slices"
)

// Manifest describes one generated set: enough to reproduce it exactly and
// to check that it covers what the tests rely on.
type Manifest struct {
	GeneratorVersion string            `json:"generatorVersion"`
	Seed             uint64            `json:"seed"`
	Profile          string            `json:"profile"`
	ContractVersion  string            `json:"contractVersion"`
	ReferenceInstant string            `json:"referenceInstant"`
	Rows             map[string]int    `json:"rows"`
	RowsPerLand      map[string]int    `json:"rowsPerLand"`
	RowsPerFamily    map[string]int    `json:"rowsPerFamily"`
	LicenceClasses   map[string]string `json:"licenceClasses"`
	EdgeCases        map[string]int    `json:"edgeCases"`
	EmptyArea        [4]float64        `json:"emptyArea"`
	Inputs           []Input           `json:"inputs"`
	Checksum         string            `json:"checksum"`
}

// Input is one input the generator reads, with its licence.
type Input struct {
	Name    string `json:"name"`
	Licence string `json:"licence"`
}

// Families are the fictional source families and the licence class each
// one stands for. Together they cover every licence category of the
// licence gate, plus a family no plan may use.
var Families = []struct{ Name, Category string }{
	{"alpha", "attribution"},
	{"beta", "share_alike"},
	{"gamma", "restricted_purpose"},
	{"delta", "acceptance"},
	{"epsilon", "not_permitted"},
	{"zeta", "zero"},
	{"eta", "official"},
}

// EdgeCases are the named situations every profile plants.
var EdgeCases = []string{
	"dst_spring", "dst_autumn", "utc_midnight", "berlin_midnight", "month_end", "year_end",
	"umlaut_eszett", "long_compound_name", "ambiguous_place",
	"empty_area", "road_without_events", "road_in_two_parts", "bbox_edge", "land_border",
	"stale_family", "unknown_code",
	"zero_occupancy", "full_occupancy", "max_decimal_price",
}

// LandCodes are the sixteen Land codes.
func LandCodes() []string {
	out := make([]string, 16)
	for i := range out {
		out[i] = twoDigits(i + 1)
	}
	return out
}

// Check lists every gap in the set's coverage (TST-009): a Land, a family,
// a licence class or a named edge case with no row, or an empty table. An
// empty list means the set covers everything.
func (m *Manifest) Check() []string {
	var gaps []string
	for _, code := range LandCodes() {
		if m.RowsPerLand[code] == 0 {
			gaps = append(gaps, "Land "+code+" has no rows")
		}
	}
	categories := map[string]bool{}
	for _, f := range Families {
		if m.RowsPerFamily[f.Name] == 0 {
			gaps = append(gaps, "family "+f.Name+" has no rows")
		}
		if m.LicenceClasses[f.Name] == f.Category {
			categories[f.Category] = true
		}
	}
	for _, f := range Families {
		if !categories[f.Category] {
			gaps = append(gaps, "licence class "+f.Category+" is not represented")
		}
	}
	for _, e := range EdgeCases {
		if m.EdgeCases[e] == 0 {
			gaps = append(gaps, "edge case "+e+" is missing")
		}
	}
	for _, t := range Tables {
		if m.Rows[t] == 0 {
			gaps = append(gaps, "table "+t+" is empty")
		}
	}
	slices.Sort(gaps)
	return gaps
}

// Summary is the success line: the counts written.
func (m *Manifest) Summary() string {
	total := 0
	for _, n := range m.Rows {
		total += n
	}
	return fmt.Sprintf("fixtures %s seed %d: %d rows in %d tables, %d Länder, %d families, %d edge cases, %s",
		m.Profile, m.Seed, total, len(m.Rows), len(m.RowsPerLand), len(m.RowsPerFamily), len(m.EdgeCases), m.Checksum)
}
