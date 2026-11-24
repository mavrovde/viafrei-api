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
	// The open state is the published hours' (now is a Monday, 14:00 in
	// Berlin), never the reported one: b reports open but its hours have it
	// shut, c reports open and has no hours.
	yes := true
	allDay, saturday := Hours{WholeDay: &yes}, Hours{Times: []OpeningTime{{Text: "Sa", Start: "08:00", End: "12:00"}}}
	stations := []FuelStation{
		{ID: "a", Name: s("ARAL Nord"), Brand: s("ARAL"), DistanceM: 100, Open: "open", Hours: allDay, Prices: []FuelPrice{{"e5", 1.9, cur}, {"diesel", 1.7, cur}}},
		{ID: "b", Name: s("Shell Mitte"), Brand: s("Shell"), DistanceM: 200, Open: "open", Hours: saturday, Prices: []FuelPrice{{"e5", 1.8, cur}}},
		{ID: "c", Brand: s("Jet"), DistanceM: 300, Open: "open", Prices: []FuelPrice{{"e5", 1.5, old}}},
		{ID: "d", Name: s("Aral Süd"), Brand: s("ARAL"), DistanceM: 400, Open: "open", Hours: allDay, Prices: []FuelPrice{{"e5", 1.8, cur}}},
		{ID: "e", DistanceM: 500, Open: "closed", Hours: allDay, Prices: []FuelPrice{{"e5", 1.4, old}}},
		{ID: "f", Name: s(""), DistanceM: 600, Hours: allDay},
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
	for id, want := range map[int]string{0: "open", 1: "closed", 2: "unknown"} {
		if got := stations[id].OpenStateAt(now); got != want {
			t.Errorf("%s: %s, want %s", stations[id].ID, got, want)
		}
	}
	if p := stations[0].Price("e10"); p != nil {
		t.Errorf("no e10: %v", p)
	}
}

// The coverage state is the platform's one classification: a held station
// wins, then a priced one beyond the radius, else nothing is known.
func TestFuelCoverageState(t *testing.T) {
	m := 6000.0
	for _, c := range []struct {
		c    FuelCoverage
		want string
	}{
		{FuelCoverage{StationsInRadius: 2, NearestPricedM: &m}, CoverageHeld},
		{FuelCoverage{StationsInRadius: 1}, CoverageHeld},
		{FuelCoverage{NearestPricedM: &m}, CoveragePricedBeyond},
		{FuelCoverage{}, CoverageUnknown},
	} {
		if got := c.c.State(); got != c.want {
			t.Errorf("%+v: %s, want %s", c.c, got, c.want)
		}
	}
}
