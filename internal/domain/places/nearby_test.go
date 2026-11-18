package places_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func near(ms ...int) []places.NeedPlace {
	out := make([]places.NeedPlace, len(ms))
	for i, m := range ms {
		out[i] = places.NeedPlace{Name: string(rune('z' - i)), DistanceM: m}
	}
	return out
}

func TestPickNeedWidensAsThePlatformsCards(t *testing.T) {
	req.Covers(t, "FR-PLACE-001")
	for name, c := range map[string]struct {
		need    string
		found   []places.NeedPlace
		radius  int
		widened bool
		shown   int
	}{
		"enough inside the first radius": {"food", near(900, 100, 500, 1200), 1000, false, 3},
		"widened to the third's km":      {"food", near(300, 1500, 2100, 4000), 3000, true, 3},
		"never beyond the widest":        {"health", near(4900, 4950, 4990), 5000, true, 3},
		"fewer than enough":              {"police", near(100), 5000, true, 1},
		"none":                           {"toilets", nil, 5000, true, 0},
		"at most five":                   {"groceries", near(1, 2, 3, 4, 5, 6), 1000, false, 5},
		"the base when the third is in":  {"lodging", near(100, 200, 1900), 2000, false, 3},
	} {
		p := places.PickNeed(c.need, c.found)
		if p.RadiusM != c.radius || p.Widened != c.widened || len(p.Places) != c.shown || p.Need != c.need {
			t.Errorf("%s: %+v", name, p)
		}
	}
	p := places.PickNeed("food", []places.NeedPlace{{Name: "b", DistanceM: 5}, {Name: "a", DistanceM: 5}})
	if p.Places[0].Name != "a" {
		t.Errorf("a tie goes by name: %+v", p.Places)
	}
}

func TestRestAreasAreNoJunctions(t *testing.T) {
	req.Covers(t, "FR-PLACE-001")
	for name, want := range map[string]bool{"Rastplatz Hammerbach": true, "Tank und Rastanlage Köln": true, "Raststätte Nord": true,
		"Parkplatz": true, "Köln-Ost": false, "Autohofstraße": false} {
		if got := places.IsRestAreaName(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
