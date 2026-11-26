package transit_test

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestFold(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	for in, want := range map[string]string{
		"Köln Hbf":          "koelnhbf",
		"KOELN-HBF":         "koelnhbf",
		"Straße":            "strasse",
		"GROẞ Gerau":        "grossgerau",
		"Fürth (Bay) Hbf":   "fuerthbayhbf",
		"Château-d'Oex":     "chateaudoex",
		"Łódź Fabryczna":    "lodzfabryczna",
		"Αθήνα":             "αθήνα",
		"  ":                "",
		"Berlin Ostkreuz 2": "berlinostkreuz2",
		"Ærøskøbing":        "æroskobing",
	} {
		if got := transit.Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapModes(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	if got := transit.MapMode("tram"); got != "tram" {
		t.Errorf("tram: %q", got)
	}
	if got := transit.MapMode("cable_car"); got != transit.Other {
		t.Errorf("an unknown mode: %q", got)
	}
	got := transit.MapModes([]string{"bus", "cable_car", "rail", "bus", "funicular"})
	if want := []transit.Mode{"rail", "bus", transit.Other}; !slices.Equal(got, want) {
		t.Errorf("MapModes = %v, want %v", got, want)
	}
	if got := transit.MapModes(nil); got == nil || len(got) != 0 {
		t.Errorf("no modes: %#v", got)
	}
}

// A station's family is the station, then each stop point once, in order.
func TestFamilyOfListsTheStationFirst(t *testing.T) {
	if got := transit.FamilyOf("f:S", []string{"f:A", "f:S", "f:B", "f:A"}); !slices.Equal(got, []string{"f:S", "f:A", "f:B"}) {
		t.Errorf("%v", got)
	}
	if got := transit.FamilyOf("f:S", nil); !slices.Equal(got, []string{"f:S"}) {
		t.Errorf("no stop points %v", got)
	}
}

// A text naming a main station gives both spellings a register writes it
// under, the town first; one naming none, or no town, gives none.
func TestMainStationSpellings(t *testing.T) {
	for in, want := range map[string][]string{
		"Berlin Hbf":            {"berlinhbf", "berlinhauptbahnhof"},
		"Hauptbahnhof, Potsdam": {"potsdamhbf", "potsdamhauptbahnhof"},
		"München Hauptbahnhof":  {"muenchenhbf", "muenchenhauptbahnhof"},
		"KÖLN-HBF":              {"koelnhbf", "koelnhauptbahnhof"},
		"Hbf":                   nil,
		"Hauptbahnhof":          nil,
		"Berlin":                nil,
		"Dom/Hbfweg":            nil,
		// The platform's own cases (#526): the English forms.
		"Cologne main station":         {"koelnhbf", "koelnhauptbahnhof"},
		"Munich main station":          {"muenchenhbf", "muenchenhauptbahnhof"},
		"Nuremberg central station":    {"nuernberghbf", "nuernberghauptbahnhof"},
		"Hanover main railway station": {"hannoverhbf", "hannoverhauptbahnhof"},
		"Frankfort central":            {"frankfurthbf", "frankfurthauptbahnhof"},
		"Brunswick main st":            {"braunschweighbf", "braunschweighauptbahnhof"},
		"Main station":                 nil,
		"Berlin Ostbahnhof":            nil,
		"Mitte, Berlin":                nil,
		"Kölner Dom, Köln":             nil,
		"Cologne station":              nil,
	} {
		if got := transit.MainStationSpellings(in); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

// The river form leaves out the words joining a town to its river, between
// two words only; a text with none gives none.
func TestRiverForm(t *testing.T) {
	for in, want := range map[string]string{
		"Frankfurt am Main":        "frankfurt main",
		"Frankfurt am Main Hbf":    "frankfurt main hbf",
		"Halle an der Saale":       "halle saale",
		"Frankfurt a. d. Oder":     "frankfurt oder",
		"Weil am Rhein, Bahnhof":   "weil rhein bahnhof",
		"Frankfurt (Main)":         "",
		"am Main":                  "",
		"Frankfurt am":             "",
		"An der Mühle":             "",
		"Berlin":                   "",
		"Rothenburg ob der Tauber": "",
	} {
		if got := transit.RiverForm(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
