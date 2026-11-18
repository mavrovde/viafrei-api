package places_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
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

func TestTheAbroadJudgementIsThePlatforms(t *testing.T) {
	req.Covers(t, "ENV-040")
	for _, c := range []struct {
		hits []bool
		want int
	}{
		{[]bool{false, false, false, false, false, false, false, false}, 8},
		{[]bool{true, true, true, true, true, true, true, true}, 0},
		{[]bool{false, false, true, true, true, true, false, false}, 4},
		{[]bool{true, false, false, false, true, false, false, true}, 3},
	} {
		if got := places.LongestEmptyRun(c.hits); got != c.want {
			t.Errorf("%v: %d, want %d", c.hits, got, c.want)
		}
	}
	w := places.SectorWedges(geo.Point{Lat: 60, Lon: 10}, 111320, 4)
	// North of the centre one degree, east two (cos 60° halves a degree of longitude).
	if len(w) != 4 || !strings.Contains(w[1], ",12.000000 60.000000,") || !strings.HasSuffix(w[3], ",10.000000 60.000000))") {
		t.Errorf("wedges %q", w)
	}
	// One whole wedge: the centre, the seven arc points from north to east, the centre again.
	if want := "POLYGON((10.000000 60.000000,10.000000 61.000000,10.517638 60.965926,11.000000 60.866025,11.414214 60.707107," +
		"11.732051 60.500000,11.931852 60.258819,12.000000 60.000000,10.000000 60.000000))"; len(w) == 4 && w[0] != want {
		t.Errorf("wedge 0 %q, want %q", w[0], want)
	}
}

// TestNameFoldIsSafeConcurrently: NameFold is called by concurrent requests
// (the watch's ask, the address and POI searches); a transformer shared
// between them panicked with another call's bounds.
func TestNameFoldIsSafeConcurrently(t *testing.T) {
	names := []string{"Sag mir Bescheid, wenn sich ein Stau bildet", "A7", "Zülzürode", "Marktweg 1, 20000", "Straße der Einheit"}
	want := make([]string, len(names))
	for i, n := range names {
		want[i] = places.NameFold(n)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				k := (g + i) % len(names)
				if got := places.NameFold(names[k]); got != want[k] {
					errs <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for got := range errs {
		t.Errorf("a concurrent fold answered %q", got)
	}
}
