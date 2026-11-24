package transit_test

import (
	"reflect"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// A text of more than one word is read as a town and a stop in it, as the
// platform's stop resolver reads "town, stop": each run of up to three words
// a town, the other words the stop.
func TestATextIsReadAsATownAndAStop(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	if got := transit.TownSplits("Marienplatz"); got != nil {
		t.Errorf("one word: %v", got)
	}
	want := []transit.TownSplit{
		{Runs: []string{"munchen"}, Rest: "marienplatz"},
		{Runs: []string{"marienplatz"}, Rest: "munchen"},
	}
	if got := transit.TownSplits("München, Marienplatz"); !reflect.DeepEqual(got, want) {
		t.Errorf("two words: %+v", got)
	}
	got := transit.TownSplits("Muenchen Ost Bf")
	if len(got) != 5 || !reflect.DeepEqual(got[0], transit.TownSplit{Runs: []string{"muenchen ost", "munchen ost"}, Rest: "bf"}) ||
		!reflect.DeepEqual(got[4], transit.TownSplit{Runs: []string{"bf"}, Rest: "muenchen ost"}) {
		t.Errorf("three words: %+v", got)
	}
	if got := transit.NameFold("  Groß-Gerau (Süd) "); got != "gross gerau sud" {
		t.Errorf("fold: %q", got)
	}
	for rest, want := range map[string][]string{"Hbf": {"hbf", "hauptbahnhof"}, "main station": {"hbf", "hauptbahnhof"}, "Marienplatz": nil, "Hbf Nord": nil, "": nil} {
		if got := transit.StopSpellings(rest); !reflect.DeepEqual(got, want) {
			t.Errorf("StopSpellings(%q) = %v, want %v", rest, got, want)
		}
	}
}
