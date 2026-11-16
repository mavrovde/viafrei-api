package transit

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestTheBoardShowsEachTrainOnceAndNoneThatHasLeft(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return now.Add(time.Duration(m) * time.Minute) }
	ptr := func(v time.Time) *time.Time { return &v }
	ice, re := "ICE", "RE"
	calls := []Departure{
		{JourneyID: "gone", PlannedAt: at(-5), Mode: "regional_rail"},
		{JourneyID: "late", PlannedAt: at(-5), ExpectedAt: ptr(at(3)), Mode: "regional_rail", Category: &re},
		{JourneyID: "cancelled", PlannedAt: at(-2), Cancelled: true, Mode: "bus"},
		{JourneyID: "left-early", PlannedAt: at(1), ExpectedAt: ptr(at(-1)), Mode: "bus"},
		{JourneyID: "ice", PlannedAt: at(4), Mode: "long_distance_rail", Category: &ice},
		{JourneyID: "late", StopPointID: "again", PlannedAt: at(9), Mode: "regional_rail", Category: &re},
		{JourneyID: "bus", PlannedAt: at(10), Mode: "bus"},
	}
	ids := func(ds []Departure) []string {
		out := []string{}
		for _, d := range ds {
			out = append(out, d.JourneyID+"@"+d.StopPointID)
		}
		return out
	}
	for _, c := range []struct {
		name string
		q    DepartureQuery
		want []string
		more bool
	}{
		{"every train once, none that left", DepartureQuery{Limit: 10}, []string{"late@", "cancelled@", "ice@", "bus@"}, false},
		{"the limit says more stand", DepartureQuery{Limit: 2}, []string{"late@", "cancelled@"}, true},
		{"the limit met exactly", DepartureQuery{Limit: 4}, []string{"late@", "cancelled@", "ice@", "bus@"}, false},
		{"a mode", DepartureQuery{Modes: []Mode{"bus"}, Limit: 10}, []string{"cancelled@", "bus@"}, false},
		{"a category keeps no row without one", DepartureQuery{Categories: []string{"ICE"}, Limit: 10}, []string{"ice@"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, more := Board(calls, c.q, now)
			if !slices.Equal(ids(got), c.want) || more != c.more {
				t.Errorf("board %v (more %v), want %v (more %v)", ids(got), more, c.want, c.more)
			}
		})
	}
	if got, more := Board(nil, DepartureQuery{Limit: 3}, now); got == nil || len(got) != 0 || more {
		t.Errorf("an empty board %v, %v", got, more)
	}
}
