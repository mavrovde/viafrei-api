package transit_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestWithTrainBoardSpeaksForTheTrainsItLists(t *testing.T) {
	at := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	s := func(v string) *string { return &v }
	until := at.Add(time.Hour)
	rows := []transit.Departure{
		{JourneyID: "far", PlannedAt: at.Add(5 * time.Minute), Line: s("RE 1"), RouteType: 106, Destination: s("Altdorf"), Family: "t"},
		{JourneyID: "near", PlannedAt: at.Add(6 * time.Minute), Line: s("RE 1"), RouteType: 106, Destination: s("Neustadt"), Family: "t"},
		{JourneyID: "bus", PlannedAt: at.Add(6 * time.Minute), Line: s("RE 1"), RouteType: 3, Family: "t"},
		{JourneyID: "num", PlannedAt: at.Add(20 * time.Minute), Line: s("IC"), TrainNumber: s("2023"), RouteType: 102, Family: "t"},
		{JourneyID: "late", PlannedAt: at.Add(3 * time.Hour), Line: s("RE 9"), RouteType: 106, Family: "t"},
	}
	moved := at.Add(8 * time.Minute)
	b := &transit.TrainBoard{Departures: []transit.TrainDeparture{
		// The same town as the farther row of its line: that row it is.
		{PlannedAt: at.Add(6 * time.Minute), ChangedAt: &moved, Line: s("RE 1"), Destination: s("Altdorf Hbf"), Platform: s("2"), ChangedPlatform: s("3")},
		// The row of its line left is taken by it, the nearest.
		{PlannedAt: at.Add(6 * time.Minute), Line: s("RE 1"), Destination: s("Neustadt"), Cancelled: true},
		// A third of the line finds no row left: DB alone lists it.
		{PlannedAt: at.Add(7 * time.Minute), Line: s("RE 1")},
		// The train's number names it.
		{PlannedAt: at.Add(21 * time.Minute), Category: s("IC"), Number: s("2023")},
		// DB alone: an S-Bahn, a bus, and one past the window.
		{PlannedAt: at.Add(30 * time.Minute), Line: s("S 3")},
		{PlannedAt: at.Add(31 * time.Minute), Category: s("Bus"), Number: s("7")},
		{PlannedAt: at.Add(2 * time.Hour), Line: s("RE 9")},
	}}
	out, spoke := transit.WithTrainBoard(rows, b, at, until)
	if !spoke || len(out) != 8 {
		t.Fatalf("%v %d", spoke, len(out))
	}
	by := map[string]transit.Departure{}
	for _, d := range out {
		by[d.JourneyID] = d
	}
	if d := by["far"]; d.Family != transit.TrainBoardFamily || *d.DelayMin != 2 || *d.Platform != "3" || *d.PlannedPlatform != "2" || !d.PlatformChanged || !d.Live {
		t.Errorf("far %+v", d)
	}
	if d := by["near"]; !d.Cancelled || d.DelayMin != nil || d.ExpectedAt != nil || d.Family != transit.TrainBoardFamily {
		t.Errorf("near %+v", d)
	}
	if by["bus"].Family != "t" || by["late"].Family != "t" || by["num"].Family != transit.TrainBoardFamily || *by["num"].Line != "IC 2023" {
		t.Errorf("rest %+v %+v %+v", by["bus"], by["late"], by["num"])
	}
	if d := by["db_timetables:2026-03-30T00:30:00Z:S 3"]; d.Mode != "sbahn" || d.RouteType != 2 {
		t.Errorf("S-Bahn %+v", d)
	}
	if d := by["db_timetables:2026-03-30T00:31:00Z:Bus 7"]; d.Mode != "bus" || d.RouteType != 3 {
		t.Errorf("bus %+v", d)
	}
	if d := by["db_timetables:2026-03-30T00:07:00Z:RE 1"]; d.Mode != "rail" || *d.DelayMin != 0 {
		t.Errorf("third %+v", d)
	}
	if !slices.IsSortedFunc(out, func(x, y transit.Departure) int { return x.PlannedAt.Compare(y.PlannedAt) }) {
		t.Error("order")
	}
	// Before what a stored board covers only: a later departure and a row
	// past it are not read.
	covers := at.Add(10 * time.Minute)
	b.CoversUntil = &covers
	if out, _ = transit.WithTrainBoard(rows, b, at, until); len(out) != 6 || by["num"].JourneyID == "" {
		t.Errorf("covered %d", len(out))
	}
	// No board, a degraded one or one that lists nothing here speaks for nothing.
	for _, x := range []*transit.TrainBoard{nil, {Degraded: true}, {Departures: []transit.TrainDeparture{{PlannedAt: at.Add(5 * time.Hour), Line: s("X")}}}} {
		if out, spoke := transit.WithTrainBoard(rows, x, at, until); spoke || len(out) != len(rows) {
			t.Errorf("%+v: %v %d", x, spoke, len(out))
		}
	}
	if transit.TrainLabel(transit.TrainDeparture{}) != nil {
		t.Error("no label")
	}
}
