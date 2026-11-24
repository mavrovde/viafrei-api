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
	if got := transit.TownRuns("München, Marienplatz"); !reflect.DeepEqual(got, []string{"munchen", "marienplatz", "munchen marienplatz"}) {
		t.Errorf("runs: %v", got)
	}
	want := []string{"kreis", "muenchen", "munchen", "muenchen u", "munchen u"}
	if got := transit.TownRuns("Kreis Muenchen U"); !reflect.DeepEqual(got, want) {
		t.Errorf("runs with an administrative word: %v", got)
	}
	for text, want := range map[string][]string{
		"Frankfurt Konstablerwache": {"frankfurt"},
		"Bahnhof, Neustadt":         {"neustadt"},
		"Hbf Bad Ems":               nil,
		"Nord":                      nil,
	} {
		if got := transit.TownPrefixes(text); !reflect.DeepEqual(got, want) {
			t.Errorf("TownPrefixes(%q) = %v, want %v", text, got, want)
		}
	}
	if got := transit.TownRest("Landkreis Fulda Bahnhof", []string{"fulda"}); got != "bahnhof" {
		t.Errorf("rest after an administrative word: %q", got)
	}
	if got := transit.TownRest("Muenchen, Marienplatz", []string{"munchen"}); got != "marienplatz" {
		t.Errorf("rest: %q", got)
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
