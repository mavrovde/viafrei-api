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
	if energy.Other != "other" || len(energy.Accesses) != 4 || len(energy.States) != 6 || len(energy.Currents) != 3 {
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
		got := energy.Site{Points: c.points}.Summarise(now, stale)
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
