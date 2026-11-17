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
