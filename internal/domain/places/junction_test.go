package places_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

func TestJunctionCore(t *testing.T) {
	for in, want := range map[string]struct {
		core     string
		prefixed bool
	}{
		"AK Hannover-Ost":            {"hannover-ost", true},
		"Kreuz  Hannover-Ost (A7)":   {"hannover-ost", true},
		"Autobahnkreuz Walldorf A 6": {"walldorf", true},
		"Kreuzberg":                  {"kreuzberg", false},
		"Hilden":                     {"hilden", false},
		"motorway junction Köln":     {"köln", true},
	} {
		if core, p := places.JunctionCore(in); core != want.core || p != want.prefixed {
			t.Errorf("%q: %q %v, want %+v", in, core, p, want)
		}
	}
}

func TestRoadForm(t *testing.T) {
	for in, want := range map[string]string{
		"A7": "A7", "a 7": "A7", "B27": "B27", "Autobahn 9": "A9", "BAB A9": "A9", "A9 motorway": "A9",
		"Bundesautobahn 100": "A100", "A07": "A7", "Hilden": "", "A1234": "", "Kreuz A3": "",
	} {
		if got := places.RoadForm(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
