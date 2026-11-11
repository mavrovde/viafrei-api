package energy_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestMappings(t *testing.T) {
	req.Covers(t, "ENV-051")
	for raw, want := range map[string]energy.Access{
		"public": energy.AccessPublic, "restricted": energy.AccessRestricted, "unknown": energy.AccessUnknown,
		"x_unmapped": energy.AccessOther, "other": energy.AccessOther, "PUBLIC": energy.AccessOther,
	} {
		if got := energy.MapAccess(raw); got != want {
			t.Errorf("MapAccess(%q) = %q", raw, got)
		}
	}
	for raw, want := range map[string]energy.State{
		"available": energy.StateAvailable, "occupied": energy.StateOccupied, "reserved": energy.StateReserved,
		"out_of_order": energy.StateOutOfOrder, "unknown": energy.StateUnknown, "x_unmapped": energy.StateOther,
	} {
		if got := energy.MapState(raw); got != want {
			t.Errorf("MapState(%q) = %q", raw, got)
		}
	}
	for raw, want := range map[string]energy.Current{"ac": energy.CurrentAC, "dc": energy.CurrentDC, "x_unmapped": energy.CurrentOther} {
		if got := energy.MapCurrent(&raw); got == nil || *got != want {
			t.Errorf("MapCurrent(%q) = %v", raw, got)
		}
	}
	if energy.MapCurrent(nil) != nil {
		t.Error("an unpublished current is not nil")
	}
	if energy.Other != "other" || len(energy.Accesses) != 4 || len(energy.States) != 7 || len(energy.Currents) != 3 {
		t.Error("the enumerations changed")
	}
}

var now = time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)

func point(state energy.State, age time.Duration, kw *float64) energy.ChargePoint {
	p := energy.ChargePoint{MaxPowerKw: kw}
	if state != "" {
		p.Availability = &energy.Availability{State: state, ObservedAt: now.Add(-age), Family: "f2"}
	}
	return p
}

func kw(v float64) *float64 { return &v }

func intp(v int) *int { return &v }

func TestSummarise(t *testing.T) {
	req.Covers(t, "ENV-041", "FR-ENERGY-004")
	const stale = 10 * time.Minute
	cases := []struct {
		name   string
		points []energy.ChargePoint
		want   energy.Summary
	}{
		{"no point", nil, energy.Summary{}},
		{"no report", []energy.ChargePoint{point("", 0, nil)}, energy.Summary{ChargePoints: 1}},
		{"only stale reports", []energy.ChargePoint{point(energy.StateAvailable, stale+time.Second, kw(22))},
			energy.Summary{ChargePoints: 1, MaxPowerKw: kw(22)}},
		{"a report at the threshold is live", []energy.ChargePoint{point(energy.StateAvailable, stale, nil)},
			energy.Summary{ChargePoints: 1, AvailableNow: intp(1)}},
		{"live but none available", []energy.ChargePoint{point(energy.StateOccupied, 0, kw(11)), point(energy.StateOutOfOrder, time.Minute, kw(50))},
			energy.Summary{ChargePoints: 2, AvailableNow: intp(0), MaxPowerKw: kw(50)}},
		{"counted", []energy.ChargePoint{
			point(energy.StateAvailable, 0, kw(150)), point(energy.StateAvailable, time.Minute, kw(22)),
			point(energy.StateReserved, 0, nil), point(energy.StateAvailable, stale+time.Minute, kw(300)),
		}, energy.Summary{ChargePoints: 4, AvailableNow: intp(2), MaxPowerKw: kw(300)}},
	}
	for _, c := range cases {
		got := energy.Site{Points: c.points}.Summarise(now, func(string) time.Duration { return stale })
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, show(got), show(c.want))
		}
	}
}

func show(s energy.Summary) []any {
	out := []any{s.ChargePoints, nil, nil}
	if s.AvailableNow != nil {
		out[1] = *s.AvailableNow
	}
	if s.MaxPowerKw != nil {
		out[2] = *s.MaxPowerKw
	}
	return out
}

func TestFamilies(t *testing.T) {
	req.Covers(t, "SEC-032")
	s := energy.Site{Family: "f1", Points: []energy.ChargePoint{point("", 0, nil), point(energy.StateAvailable, 0, nil)}}
	if got := s.Families(); !reflect.DeepEqual(got, []string{"f1", "f2"}) {
		t.Errorf("Families = %v", got)
	}
	// A report that says nothing contributed nothing: its family is not credited.
	silent := energy.Site{Family: "f1", Points: []energy.ChargePoint{point(energy.StateUnknown, 0, nil), point(energy.StateOther, 0, nil)}}
	if got := silent.Families(); !reflect.DeepEqual(got, []string{"f1"}) {
		t.Errorf("Families = %v", got)
	}
}

func TestAccessFilterCodes(t *testing.T) {
	req.Covers(t, "FR-ENERGY-006")
	if got := energy.FilterPublic.Codes(); !reflect.DeepEqual(got, []energy.Access{energy.AccessPublic}) {
		t.Errorf("public %v", got)
	}
	if got := energy.FilterRestricted.Codes(); !reflect.DeepEqual(got, []energy.Access{energy.AccessRestricted}) {
		t.Errorf("restricted %v", got)
	}
	if got := energy.FilterAll.Codes(); got != nil {
		t.Errorf("all %v", got)
	}
}

func TestSummariseKeepsTheFirstOfEqualPowers(t *testing.T) {
	req.Covers(t, "ENV-041")
	s := energy.Site{Points: []energy.ChargePoint{point("", 0, kw(50)), point("", 0, kw(50))}}
	if got := s.Summarise(now, func(string) time.Duration { return time.Minute }).MaxPowerKw; got != s.Points[0].MaxPowerKw {
		t.Error("the highest power is not the first point's of equal powers")
	}
}

func TestSummariseJudgesEachReportByItsOwnFamily(t *testing.T) {
	req.Covers(t, "ENV-041", "FR-ENERGY-004")
	// Two reports of the same age: one family's threshold keeps it live, the
	// other's makes it stale. The site's own family has no say.
	quick, slow := point(energy.StateAvailable, 20*time.Minute, nil), point(energy.StateAvailable, 20*time.Minute, nil)
	quick.Availability.Family, slow.Availability.Family = "fast", "slow"
	thresholds := map[string]time.Duration{"site": time.Hour, "fast": 5 * time.Minute, "slow": 30 * time.Minute}
	var asked []string
	got := energy.Site{Family: "site", Points: []energy.ChargePoint{quick, slow}}.Summarise(now, func(f string) time.Duration {
		asked = append(asked, f)
		return thresholds[f]
	})
	if got.AvailableNow == nil || *got.AvailableNow != 1 || !reflect.DeepEqual(asked, []string{"fast", "slow"}) {
		t.Errorf("available %v, thresholds asked for %v", show(got)[1], asked)
	}
}

func TestAReportOlderThanTheShorterLimitIsShownStale(t *testing.T) {
	req.Covers(t, "ENV-041", "FR-ENERGY-004")
	// The status age limit and the family's threshold: the shorter one rules.
	if got := energy.StatusLimit(30*time.Minute, time.Hour); got != 30*time.Minute {
		t.Errorf("limit %v under a longer family threshold", got)
	}
	if got := energy.StatusLimit(30*time.Minute, 10*time.Minute); got != 10*time.Minute {
		t.Errorf("limit %v under a shorter family threshold", got)
	}
	const limit = 30 * time.Minute
	for _, c := range []struct {
		age  time.Duration
		want energy.State
	}{{0, energy.StateReserved}, {limit, energy.StateReserved}, {limit + time.Second, energy.StateStale}, {10 * time.Hour, energy.StateStale}} {
		a := point(energy.StateReserved, c.age, nil).Availability
		if got := a.Shown(now, limit); got != c.want || a.Live(now, limit) != (c.want != energy.StateStale) {
			t.Errorf("a report %v old: shown %q, want %q", c.age, got, c.want)
		}
	}
	if energy.MapState("stale") != energy.StateOther {
		t.Error("a stored code maps to stale; only the age makes a report stale")
	}
}

func TestAReportOfUnknownSaysNothing(t *testing.T) {
	req.Covers(t, "ENV-041", "ENV-051")
	for _, st := range energy.States {
		a := energy.Availability{State: st}
		if want := st != energy.StateUnknown && st != energy.StateOther; a.Says() != want {
			t.Errorf("%s says %v", st, a.Says())
		}
	}
	// A live report of unknown is not counted: nothing current is known.
	got := energy.Site{Points: []energy.ChargePoint{point(energy.StateUnknown, 0, nil), point(energy.StateOther, 0, nil)}}.
		Summarise(now, func(string) time.Duration { return time.Hour })
	if got.AvailableNow != nil {
		t.Errorf("available %v from reports that say nothing", *got.AvailableNow)
	}
}
