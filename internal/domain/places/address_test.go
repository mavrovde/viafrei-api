package places_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

func TestFoldsAreThePlatforms(t *testing.T) {
	for in, want := range map[string]string{"Köln-Süd": "koln sud", "Große  Straße": "grosse strasse", "  ": ""} {
		if got := places.NameFold(in); got != want {
			t.Errorf("NameFold(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"Hauptstr.": "hauptstrasse", "Am Markt": "ammarkt", "Bahnhof Str": "bahnhofstrasse"} {
		if got := places.StreetFold(in); got != want {
			t.Errorf("StreetFold(%q) = %q, want %q", in, got, want)
		}
	}
	if got := places.HouseNumberFold("12 A"); got != "12a" {
		t.Errorf("HouseNumberFold: %q", got)
	}
}

func TestParseAddress(t *testing.T) {
	for in, want := range map[string]places.AddressQuery{
		"Hauptstraße 12, 50667 Köln": {Street: "Hauptstraße", HouseNumber: "12", Postcode: "50667", Place: "Köln"},
		"Köln, Hauptstr. 12a":        {Street: "Hauptstr.", HouseNumber: "12a", Place: "Köln"},
		"Am Markt 3 Fulda":           {Street: "Am Markt", HouseNumber: "3", Place: "Fulda"},
		"Ring 1, 36037":              {Street: "Ring", HouseNumber: "1", Postcode: "36037"},
	} {
		if got, ok := places.ParseAddress(in); !ok || got != want {
			t.Errorf("ParseAddress(%q) = %+v %v, want %+v", in, got, ok, want)
		}
	}
	for _, in := range []string{"Hauptstraße 12", "Köln", ""} {
		if _, ok := places.ParseAddress(in); ok {
			t.Errorf("ParseAddress(%q) read an address", in)
		}
	}
}

func FuzzParseAddress(f *testing.F) {
	for _, s := range []string{"Hauptstraße 12, 50667 Köln", "Köln, Hauptstr. 12a", "Ring 1, 36037", "Hauptstraße 12", ",,", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, ok := places.ParseAddress(s)
		if ok && (q.Street == "" || q.HouseNumber == "" || (q.Place == "" && q.Postcode == "")) {
			t.Fatalf("ParseAddress(%q) = %+v: read an address without its parts", s, q)
		}
	})
}
