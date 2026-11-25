package transit

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestTrainBoardOfReadsTheStoredRowAsThePlatform(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	ts := func(m int) *string { s := at.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano); return &s }
	bad, line, three, zero, yes, p7, p8 := "x", "RE 1", 3, 0, true, "7", "8"
	b := TrainBoardOf("8000105", StoredBoard{EVAs: []string{"8000105"}, ReadAt: at, CoversUntil: at.Add(time.Hour), Departures: []StoredDeparture{
		{Line: &line, PlannedAt: ts(5), DelayMin: &three, PlannedPlatform: &p7, Platform: &p8, PlatformChanged: &yes},
		{Line: &line, PlannedAt: ts(6), DelayMin: &zero, Cancelled: &yes},
		{Line: &line, PlannedAt: &bad},
		{PlannedAt: ts(7)},
	}})
	if !b.Degraded || !b.Stored || len(b.Departures) != 2 || b.Departures[0].DelayMin() != 3 || !b.Departures[0].PlatformChanged() || *b.Departures[0].ChangedPlatform != "8" ||
		b.Departures[1].ChangedAt != nil || !b.Departures[1].Cancelled || !b.CoversUntil.Equal(at.Add(time.Hour)) {
		t.Errorf("board %+v", b)
	}
}

func TestBoardWindowIsTheNextToLeaveInPlannedOrder(t *testing.T) {
	from := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	d := func(planned, delay int, cancelled bool) TrainDeparture {
		v := TrainDeparture{PlannedAt: from.Add(time.Duration(planned) * time.Minute), Cancelled: cancelled}
		if delay != 0 {
			c := v.PlannedAt.Add(time.Duration(delay) * time.Minute)
			v.ChangedAt = &c
		}
		return v
	}
	covers := from.Add(50 * time.Minute)
	b := TrainBoard{CoversUntil: &covers, Departures: []TrainDeparture{
		d(-5, 0, false),  // left before the window
		d(-5, 10, false), // late: in it
		d(-1, 30, true),  // cancelled: by its plan, before
		d(10, 0, false), d(2, 15, false), d(20, 0, true), d(40, 0, false),
		d(55, 0, false), // past what the board covers
	}}
	shown, cut := BoardWindow(b, from, 60, 3)
	var got []int
	for _, x := range shown {
		got = append(got, int(x.PlannedAt.Sub(from)/time.Minute))
	}
	if !slices.Equal(got, []int{-5, 2, 10}) || cut == nil || cut.More != 2 || !cut.Until.Equal(from.Add(40*time.Minute)) {
		t.Errorf("shown %v cut %+v", got, cut)
	}
	if shown, cut = BoardWindow(TrainBoard{Departures: b.Departures}, from, 60, 50); len(shown) != 6 || cut != nil {
		t.Errorf("uncut %d %+v", len(shown), cut)
	}
	same := TrainBoard{Departures: []TrainDeparture{d(30, 2, false), d(30, 0, false)}}
	if shown, _ = BoardWindow(same, from, 60, 5); shown[0].ChangedAt != nil {
		t.Error("a tie in plan goes by the expected time")
	}
}

func TestStationLineGroupsAsThePlatform(t *testing.T) {
	got := StationLineGroups([]StopFamilyLines{
		{Modes: []string{"bus", "rail"}, Lines: []string{"6", "RE1", " ", "S 3", "11"}},
		{Modes: []string{"subway", "tram"}, Lines: []string{"U2", "16", "U10"}},
		{Modes: []string{"tram"}, Lines: []string{"U79", "903"}},
		{Modes: []string{"tram", "bus"}, Lines: []string{"1", "903"}},
		{Modes: []string{"rail"}, Lines: []string{"RB2"}},
	})
	want := []LineGroup{
		{Modes: []string{"subway"}, Lines: []string{"U2", "U10"}},
		{Modes: []string{"tram"}, Lines: []string{"16", "903", "U79"}},
		{Modes: []string{"bus"}, Lines: []string{"6", "11"}},
		{Modes: []string{"tram", "bus"}, Lines: []string{"1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groups %+v", got)
	}
	for _, c := range []struct {
		a, b string
		less bool
	}{{"U2", "U10", true}, {"10", "9", false}, {"N1", "1", false}, {"1", "N1", true}, {"a", "B", true}, {"U1", "U1x", true}} {
		if (NumericCompare(c.a, c.b) < 0) != c.less {
			t.Errorf("%s < %s should be %v", c.a, c.b, c.less)
		}
	}
	if NumericCompare("U2", "u2") != 0 {
		t.Error("equal names")
	}
}

func TestInterchangeOfAsThePlatform(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	call := func(m, rt int, late *int) Departure {
		d := Departure{PlannedAt: now.Add(time.Duration(m) * time.Minute), RouteType: rt}
		if late != nil {
			e := d.PlannedAt.Add(time.Duration(*late) * time.Minute)
			d.ExpectedAt = &e
		}
		return d
	}
	five := 5
	if InterchangeOf(InterchangeRead{}, now, now) != nil || InterchangeOf(InterchangeRead{Stops: true}, now, now) != nil {
		t.Error("no stop, or nothing to name, is no block")
	}
	calls := []Departure{call(-10, 400, nil), call(-3, 0, &five), call(1, 2, nil)}
	for i := range 12 {
		calls = append(calls, call(i+2, 900, nil))
	}
	ic := InterchangeOf(InterchangeRead{Stops: true, Calls: calls}, now, now)
	if ic == nil || len(ic.Departures) != 9 || ic.Departures[0].RouteType != 0 || len(ic.Lines) != 0 {
		t.Errorf("interchange %+v", ic)
	}
	for rt, want := range map[int]Mode{1: "subway", 405: "subway", 0: "tram", 906: "tram", 2: "rail", 700: "rail"} {
		if got := BoardModeOf(&rt); got != want {
			t.Errorf("%d: %s", rt, got)
		}
	}
	if BoardModeOf(nil) != "rail" {
		t.Error("no route type")
	}
}
