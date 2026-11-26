package store

import (
	"slices"
	"testing"
)

// A station search reads the river form of a text beside its main-station
// spellings: "Frankfurt am Main" as "Frankfurt (Main) …" folds.
func TestStationSpellingsReadTheRiverForm(t *testing.T) {
	for in, want := range map[string][]string{
		"Frankfurt am Main":     {"frankfurtmain"},
		"Frankfurt am Main Hbf": {"frankfurtammainhbf", "frankfurtammainhauptbahnhof", "frankfurtmainhbf", "frankfurtmainhauptbahnhof"},
		"Berlin Hbf":            {"berlinhbf", "berlinhauptbahnhof"},
		"Berlin":                {},
	} {
		if got := stationSpellingsOf(in); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}
