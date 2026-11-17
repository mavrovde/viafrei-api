package energy

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestRankFuelIsThePlatformsSearch(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	s := func(v string) *string { return &v }
	cur, old := now.Add(-time.Hour), now.Add(-25*time.Hour)
	stations := []FuelStation{
		{ID: "a", Name: s("ARAL Nord"), Brand: s("ARAL"), DistanceM: 100, Open: "open", Prices: []FuelPrice{{"e5", 1.9, cur}, {"diesel", 1.7, cur}}},
		{ID: "b", Name: s("Shell Mitte"), Brand: s("Shell"), DistanceM: 200, Open: "closed", Prices: []FuelPrice{{"e5", 1.8, cur}}},
		{ID: "c", Brand: s("Jet"), DistanceM: 300, Open: "unknown", Prices: []FuelPrice{{"e5", 1.5, old}}},
		{ID: "d", Name: s("Aral Süd"), Brand: s("ARAL"), DistanceM: 400, Open: "open", Prices: []FuelPrice{{"e5", 1.8, cur}}},
		{ID: "e", DistanceM: 500, Open: "open", Prices: []FuelPrice{{"e5", 1.4, old}}},
		{ID: "f", Name: s(""), DistanceM: 600, Open: "open"},
	}
	ids := func(a FuelAnswer) []string {
		out := []string{}
		for _, s := range a.Stations {
			out = append(out, s.ID)
		}
		return out
	}
	for _, c := range []struct {
		name string
		q    FuelQuery
		want []string
		n    int
	}{
		{"nearest first", FuelQuery{Limit: 10}, []string{"a", "b", "c", "d", "e", "f"}, 6},
		{"the limit", FuelQuery{Limit: 2}, []string{"a", "b"}, 6},
		{"a grade", FuelQuery{Fuel: "diesel", Limit: 10}, []string{"a"}, 1},
		{"a brand, any case", FuelQuery{Brand: "aral", Limit: 10}, []string{"a", "d"}, 2},
		{"a name or brand", FuelQuery{Name: "jet", Limit: 10}, []string{"c"}, 1},
		{"a name", FuelQuery{Name: "mitte", Limit: 10}, []string{"b"}, 1},
		{"by price: current cheapest first, last known after by distance", FuelQuery{Fuel: "e5", Sort: "price", Limit: 10, Window: 24 * time.Hour}, []string{"b", "d", "a", "c", "e"}, 5},
		{"by name", FuelQuery{Sort: "name", Limit: 10}, []string{"a", "d", "e", "f", "c", "b"}, 6},
	} {
		if got := RankFuel(stations, c.q, now); !slices.Equal(ids(got), c.want) || got.Matched != c.n {
			t.Errorf("%s: %v (%d), want %v (%d)", c.name, ids(got), got.Matched, c.want, c.n)
		}
	}
	got := RankFuel(stations, FuelQuery{OpenNow: true, Limit: 10}, now)
	if !slices.Equal(ids(got), []string{"a", "d", "e", "f"}) || got.DroppedClosed != 1 || got.DroppedUnknown != 1 {
		t.Errorf("open now: %v closed %d unknown %d", ids(got), got.DroppedClosed, got.DroppedUnknown)
	}
	if p := stations[0].Price("e10"); p != nil {
		t.Errorf("no e10: %v", p)
	}
}
