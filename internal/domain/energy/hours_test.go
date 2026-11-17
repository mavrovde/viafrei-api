package energy_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestParseDays(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002")
	for text, want := range map[string][]int{
		"Mo-Fr":                  {0, 1, 2, 3, 4},
		"Sa, So":                 {5, 6},
		"täglich":                {0, 1, 2, 3, 4, 5, 6},
		"Sonn- und Feiertage":    {6},
		"Fr bis Mo":              {0, 4, 5, 6},
		"Mo.+Di. und täglich":    {0, 1, 2, 3, 4, 5, 6},
		"Mittwoch, , Donnerstag": {2, 3},
	} {
		if got, ok := energy.ParseDays(text); !ok || !slices.Equal(got, want) {
			t.Errorf("%q: %v, %v; want %v", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "Werktags", "Mo-Xy", "Xy-Mo", "Feiertage"} {
		if got, ok := energy.ParseDays(text); ok {
			t.Errorf("%q read as %v", text, got)
		}
	}
}

func TestParseClock(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002")
	for s, want := range map[string]int{"06:00": 360, "24:00": 1440, " 7:30:00 ": 450} {
		if got, ok := energy.ParseClock(s); !ok || got != want {
			t.Errorf("%q: %d, %v", s, got, ok)
		}
	}
	for _, s := range []string{"24:30", "12:60", "25:00", "noon"} {
		if _, ok := energy.ParseClock(s); ok {
			t.Errorf("%q read", s)
		}
	}
}

func TestScheduleOpenAt(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002")
	berlin, _ := time.LoadLocation("Europe/Berlin")
	at := func(d, h, m int) time.Time { return time.Date(2026, 3, d, h, m, 0, 0, berlin) } // 2026-03-30 is a Monday
	yes := true
	if s, ok := energy.ReadSchedule(energy.Hours{WholeDay: &yes}); !ok || !s.WholeDay {
		t.Fatal("whole day")
	} else if open, known := s.OpenAt(at(30, 3, 0)); !open || !known {
		t.Error("whole day is open")
	}
	s, ok := energy.ReadSchedule(energy.Hours{Times: []energy.OpeningTime{
		{Text: "Mo-Fr", Start: "06:00", End: "22:00"},
		{Text: "Sa", Start: "20:00", End: "02:00"},
		{Text: "So", Start: "00:00", End: "00:00"},
	}})
	if !ok {
		t.Fatal("not read")
	}
	for _, c := range []struct {
		at         time.Time
		open, know bool
	}{
		{at(30, 6, 0), true, true}, {at(30, 22, 0), false, true}, {at(28, 21, 0), true, true},
		{at(29, 1, 30), true, true}, {at(29, 12, 0), true, true}, {at(31, 1, 0), false, true},
	} {
		if open, known := s.OpenAt(c.at); open != c.open || known != c.know {
			t.Errorf("%v: %v %v", c.at, open, known)
		}
	}
	s, ok = energy.ReadSchedule(energy.Hours{Times: []energy.OpeningTime{
		{Text: "Mo", Start: "06:00", End: "22:00"}, {Text: "", Start: "06:00", End: "07:00"}, {Text: "Di", Start: "10:00", End: "10:00"},
	}})
	if open, known := s.OpenAt(at(31, 12, 0)); !ok || open || known || !slices.Equal(s.Unreadable, []string{"(ohne Text)", "Di"}) {
		t.Errorf("an unreadable entry leaves it unknown: %v %v %+v", open, known, s)
	}
	if _, ok := energy.ReadSchedule(energy.Hours{}); ok {
		t.Error("no hours read")
	}
}

func TestResolveOpenAt(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002")
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 3, 30, 22, 0, 0, 0, berlin)
	for text, want := range map[string]time.Time{
		"23:30":            time.Date(2026, 3, 30, 23, 30, 0, 0, berlin),
		"22:00":            now,
		"06:15":            time.Date(2026, 3, 31, 6, 15, 0, 0, berlin),
		"2026-09-30 06:15": time.Date(2026, 9, 30, 6, 15, 0, 0, berlin),
		"2026-10-25T02:30": time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), // the fold: the later 02:30, CET
		"2026-03-29 02:30": time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC), // the gap: moved forward, 03:30 CEST
	} {
		if got, ok := energy.ResolveOpenAt(text, now); !ok || !got.Equal(want) {
			t.Errorf("%q: %v, %v; want %v", text, got, ok, want)
		}
	}
	for _, text := range []string{"24:00", "12:60", "2026-02-30 10:00", "2026-13-01 10:00", "2026-01-01 25:00", "soon"} {
		if _, ok := energy.ResolveOpenAt(text, now); ok {
			t.Errorf("%q resolved", text)
		}
	}
}

func TestRankFuelOpenAtAndWholeDay(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002")
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, berlin)
	yes, no := true, false
	day := []energy.OpeningTime{{Text: "täglich", Start: "06:00", End: "22:00"}}
	stations := []energy.FuelStation{
		{ID: "always", Hours: energy.Hours{WholeDay: &yes}},
		{ID: "day", Hours: energy.Hours{WholeDay: &no, Times: day}},
		{ID: "unknown"},
		{ID: "unreadable", Hours: energy.Hours{Times: []energy.OpeningTime{{Text: "nach Absprache", Start: "1", End: "2"}}}},
	}
	a := energy.RankFuel(stations, energy.FuelQuery{OpenAt: "23:30", Limit: 5}, now)
	if len(a.Stations) != 1 || a.Stations[0].ID != "always" || a.DroppedClosed != 1 || a.DroppedUnknown != 2 || a.OpenAt == nil ||
		!a.OpenAt.Equal(time.Date(2026, 3, 30, 23, 30, 0, 0, berlin)) {
		t.Errorf("open at: %+v", a)
	}
	a = energy.RankFuel(stations, energy.FuelQuery{OpenAt: "07:00", WholeDay: true, Limit: 5}, now)
	if len(a.Stations) != 1 || a.DroppedNotWholeDay != 3 || a.DroppedUnknown != 0 {
		t.Errorf("whole day: %+v", a)
	}
	a = energy.RankFuel(stations, energy.FuelQuery{OpenAt: "07:00", Limit: 5}, now)
	if len(a.Stations) != 2 || a.DroppedUnknown != 2 {
		t.Errorf("morning: %+v", a)
	}
	if a = energy.RankFuel(stations, energy.FuelQuery{OpenAt: "25:00", Limit: 5}, now); a.OpenAt != nil || len(a.Stations) != 4 {
		t.Errorf("an unresolvable time filters nothing: %+v", a)
	}
}
