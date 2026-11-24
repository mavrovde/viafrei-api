package transit

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestTheBoardIsThePlatformsBoard(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	base := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	s := func(v string) *string { return &v }
	ice := "ICE"
	calls := []Departure{
		// Listed out of order: the board orders them.
		{JourneyID: "j9", StopPointID: "p1", PlannedAt: at(10), Line: s("RE 5"), Headsign: s("Köln"), PlannedPlatform: s("2"), Mode: "rail", RouteType: 2},
		// One line to one destination at one instant, from another stop point: kept once, the lowest journey.
		{JourneyID: "j1", StopPointID: "p1", PlannedAt: at(0), Line: s("120"), Headsign: s("Mitte"), Mode: "bus", RouteType: 3},
		{JourneyID: "j2", StopPointID: "p2", PlannedAt: at(0), Line: s("120"), Headsign: s("Mitte"), Mode: "bus", RouteType: 3},
		// No headsign: the stop point is in the key, so two stop points are two rows.
		{JourneyID: "j3", StopPointID: "p1", PlannedAt: at(0), Line: s("M85"), Mode: "bus", RouteType: 3},
		{JourneyID: "j4", StopPointID: "p2", PlannedAt: at(0), Line: s("M85"), Mode: "bus", RouteType: 3},
		// One destination from one platform at one instant under two line names: one vehicle.
		{JourneyID: "j5", StopPointID: "p3", PlannedAt: at(5), Line: s("IC 2434"), Headsign: s("Emden"), PlannedPlatform: s("3"), Category: &ice, Mode: "rail", RouteType: 102},
		{JourneyID: "j6", StopPointID: "p3", PlannedAt: at(5), Line: s("RE IC56"), Headsign: s("Emden"), PlannedPlatform: s("3"), Mode: "rail", RouteType: 2},
		// No platform: its own key on the second pass.
		{JourneyID: "j7", StopPointID: "p4", PlannedAt: at(5), Line: s("T1"), Headsign: s("Emden"), Mode: "tram", RouteType: 0},
		// No line sorts last among its instant.
		{JourneyID: "j0", StopPointID: "p5", PlannedAt: at(5), Headsign: s("Hafen"), Mode: "ferry", RouteType: 4},
	}
	ids := func(ds []Departure) []string {
		out := []string{}
		for _, d := range ds {
			out = append(out, d.JourneyID)
		}
		return out
	}
	for _, c := range []struct {
		name         string
		q            DepartureQuery
		want         []string
		matched, all int
		dropped      map[int]int
	}{
		{"every departure once", DepartureQuery{Limit: 10}, []string{"j1", "j3", "j4", "j5", "j7", "j0", "j9"}, 7, 7, map[int]int{}},
		{"the limit keeps the earliest", DepartureQuery{Limit: 2}, []string{"j1", "j3"}, 7, 7, map[int]int{}},
		{"a mode, with what it left out", DepartureQuery{Modes: []Mode{"rail"}, Limit: 10}, []string{"j5", "j9"}, 2, 7, map[int]int{3: 3, 0: 1, 4: 1}},
		{"a category keeps no row without one", DepartureQuery{Categories: []string{"ICE"}, Limit: 10}, []string{"j5"}, 1, 7, map[int]int{3: 3, 0: 1, 4: 1, 2: 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Board(calls, c.q)
			if !slices.Equal(ids(got.Rows), c.want) || got.Matched != c.matched || got.MatchedAll != c.all || !maps.Equal(got.Dropped, c.dropped) {
				t.Errorf("board %v (%d of %d, dropped %v), want %v (%d of %d, dropped %v)", ids(got.Rows), got.Matched, got.MatchedAll, got.Dropped, c.want, c.matched, c.all, c.dropped)
			}
		})
	}
	if got := Board(nil, DepartureQuery{Limit: 3}); got.Rows == nil || len(got.Rows) != 0 || got.Matched != 0 || got.Dropped == nil {
		t.Errorf("an empty board %+v", got)
	}
	// One journey listed twice at one instant on one line is one departure:
	// the first call is kept.
	twice := []Departure{
		{JourneyID: "r", StopPointID: "p1", CallOrder: 4, PlannedAt: at(0), Line: s("U2"), Headsign: s("Ring"), Mode: "subway"},
		{JourneyID: "r", StopPointID: "p1", CallOrder: 2, PlannedAt: at(0), Line: s("U2"), Headsign: s("Ring"), PlannedPlatform: s("1"), Mode: "subway"},
	}
	if got := Board(twice, DepartureQuery{Limit: 5}); len(got.Rows) != 1 || got.Rows[0].CallOrder != 2 {
		t.Errorf("one line to one destination at one instant is one departure: %+v", got.Rows)
	}
}

func TestLineOrder(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	a, b := "A", "B"
	for _, c := range []struct {
		x, y *string
		want int
	}{{nil, nil, 0}, {nil, &a, 1}, {&a, nil, -1}, {&a, &b, -1}, {&b, &a, 1}} {
		if got := lineOrder(c.x, c.y); got != c.want {
			t.Errorf("lineOrder(%v, %v) = %d", c.x, c.y, got)
		}
	}
}

// A delay is in minutes as the platform's Math.round states it: half a
// minute up, -90 s to -1, never as the contract's truncated or away-from-zero
// minute.
func TestDelayMinOfRoundsAsThePlatform(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	if DelayMinOf(nil) != nil {
		t.Error("no figure was given a minute")
	}
	for s, want := range map[int]int{0: 0, 29: 0, 30: 1, 89: 1, 90: 2, 100: 2, 150: 3, -29: 0, -30: 0, -31: -1, -90: -1, -91: -2} {
		if got := DelayMinOf(&s); *got != want {
			t.Errorf("%d s: %d, want %d", s, *got, want)
		}
	}
}
