package transit_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func dep(line string, rt int, at time.Time, delay int, cancelled, moved bool) transit.Departure {
	d := transit.Departure{Line: &line, RouteType: rt, PlannedAt: at, Cancelled: cancelled, PlatformChanged: moved}
	if delay >= 0 {
		d.DelayMin = &delay
	}
	return d
}

func TestTravelOfAsToday(t *testing.T) {
	cat := func(c string) *string { return &c }
	for want, d := range map[string]transit.Departure{
		"sev":      {Line: strp("SEV RE1"), RouteType: 3},
		"subway":   {Line: strp("U1"), RouteType: 400},
		"tram":     {Line: strp("12"), RouteType: 0},
		"bus":      {Line: strp("100"), RouteType: 3},
		"long":     {Line: strp("ICE 5"), RouteType: 2},
		"sbahn":    {Line: strp("S1"), RouteType: 109},
		"regional": {Line: strp("RE 1"), RouteType: 106, Category: cat("re")},
	} {
		if got := transit.TravelOf(d); got != want {
			t.Errorf("%s: %s", want, got)
		}
	}
	for d, want := range map[*transit.Departure]string{
		{Line: strp("IC 2"), RouteType: 102}:                 "long",
		{Line: strp("S 3"), RouteType: 2}:                    "sbahn",
		{Line: strp("Bus RE 7"), RouteType: 3}:               "sev",
		{Line: strp("EV1"), RouteType: 3}:                    "sev",
		{Line: strp("X"), RouteType: 714}:                    "sev",
		{Line: strp("X"), RouteType: 3, Category: cat("ev")}: "sev",
		{RouteType: 7}: "regional",
	} {
		if got := transit.TravelOf(*d); got != want {
			t.Errorf("%v: %s, want %s", *d.Line, got, want)
		}
	}
}

func TestRailAmpelOfAsToday(t *testing.T) {
	now := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return now.Add(time.Duration(m) * time.Minute) }
	// Too few trains, all well: unknown; a bus and a train past the hour do not count.
	a := transit.RailAmpelOf(now, []transit.Departure{dep("RE1", 106, at(5), 0, false, false), dep("100", 3, at(6), 30, false, false),
		dep("RE1", 106, at(70), 30, false, false)}, true, [][]string{})
	if a.Lamp != "unknown" || a.Trips != 1 || len(a.Reasons) != 0 {
		t.Errorf("few %+v", a)
	}
	// Green with three on time.
	ok := []transit.Departure{dep("RE1", 106, at(1), 0, false, false), dep("RE2", 106, at(2), -1, false, false), dep("ICE 5", 2, at(3), 2, false, false)}
	if a := transit.RailAmpelOf(now, ok, true, [][]string{}); a.Lamp != "green" || len(a.Lines) != 3 || !a.Lines[0].Long {
		t.Errorf("green %+v", a)
	}
	// A blind source and an unread board: yellow sight on the station only.
	if a := transit.RailAmpelOf(now, ok, false, nil); a.Lamp != "yellow" || a.Reasons[0].Kind != "sight" || len(a.Reasons[0].Sources) != 2 || a.Lines[0].Lamp != "yellow" {
		t.Errorf("sight %+v", a)
	}
	// Red: cancellations, lateness and the median; a line raises nothing past the station's red.
	bad := []transit.Departure{dep("RE1", 106, at(1), 20, false, true), dep("RE1", 106, at(2), 20, false, true), dep("RE1", 106, at(3), 16, false, true),
		dep("RE1", 106, at(4), 0, true, false), dep("RE2", 106, at(5), -1, false, false)}
	a = transit.RailAmpelOf(now, bad, true, [][]string{{"RE 1"}})
	kinds := map[string]bool{}
	for _, r := range a.Reasons {
		kinds[r.Kind+r.Lamp] = true
	}
	if a.Lamp != "red" || !kinds["cancelledred"] || !kinds["latered"] || !kinds["medianred"] || !kinds["platformyellow"] || !kinds["alertyellow"] || a.Reasons[0].Lamp != "red" {
		t.Errorf("red %+v", a)
	}
	if l := a.Lines[0]; l.Line != "RE1" || l.Lamp != "red" || l.Trips != 4 {
		t.Errorf("line %+v", a.Lines)
	}
	// One cancellation in many is yellow; one late of ten is yellow; a red line raises a green station.
	many := []transit.Departure{dep("RE9", 106, at(1), 0, true, false)}
	for i := range 9 {
		many = append(many, dep("S1", 109, at(i+2), 0, false, false))
	}
	if a := transit.RailAmpelOf(now, many, true, [][]string{}); a.Lamp != "yellow" || a.Reasons[0].Kind != "cancelled" {
		t.Errorf("yellow %+v", a)
	}
	many[0] = dep("RE9", 106, at(1), 6, false, false)
	if a := transit.RailAmpelOf(now, many, true, [][]string{}); a.Lamp != "yellow" || a.Reasons[0].Kind != "late" {
		t.Errorf("late yellow %+v", a)
	}
	mixed := []transit.Departure{dep("RB5", 106, at(1), 0, true, false), dep("RB5", 106, at(2), 0, true, false), dep("RB5", 106, at(3), 0, false, false)}
	for i := range 12 {
		mixed = append(mixed, dep("S2", 109, at(i+4), 0, false, false))
	}
	if a := transit.RailAmpelOf(now, mixed, true, [][]string{}); a.Lamp != "red" || a.Reasons[0].Kind != "line" || a.Reasons[0].Line != "RB5" {
		t.Errorf("raised %+v", a)
	}
	// A line without a name is in the station's figures only; an even count's median is the mean of the middle two.
	even := []transit.Departure{dep("", 106, at(1), 14, false, false), dep("RE1", 106, at(2), 16, false, false), dep("RE1", 106, at(3), 16, false, false), dep("RE1", 106, at(4), 14, false, false)}
	if a := transit.RailAmpelOf(now, even, true, [][]string{}); a.Lamp != "red" || len(a.Lines) != 1 {
		t.Errorf("even %+v", a)
	}
}
