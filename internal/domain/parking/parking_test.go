package parking_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestMappings(t *testing.T) {
	req.Covers(t, "ENV-051")
	for _, k := range parking.Kinds[:6] {
		if got := parking.MapKind(string(k)); got != k {
			t.Errorf("MapKind(%q) = %q", k, got)
		}
	}
	for _, raw := range []string{"x_unmapped", "other", "", "Garage"} {
		if got := parking.MapKind(raw); got != parking.KindOther {
			t.Errorf("MapKind(%q) = %q", raw, got)
		}
		if got := parking.MapState(raw); got != parking.StateOther {
			t.Errorf("MapState(%q) = %q", raw, got)
		}
	}
	for _, s := range parking.States[:5] {
		if got := parking.MapState(string(s)); got != s {
			t.Errorf("MapState(%q) = %q", s, got)
		}
	}
	if parking.Other != "other" || len(parking.Kinds) != 7 || len(parking.States) != 6 {
		t.Error("the enumerations changed")
	}
}

func TestWithheld(t *testing.T) {
	req.Covers(t, "FR-PARK-003", "SEC-030")
	free := 3
	s := parking.Site{ID: "p", Occupancy: &parking.Occupancy{Free: &free, State: parking.StateFree, ObservedAt: time.Now()}}
	w := s.Withheld()
	if w.Occupancy != nil || !w.TermsRequired || w.ID != "p" {
		t.Errorf("Withheld = %+v", w)
	}
	if s.Occupancy == nil || s.TermsRequired {
		t.Error("Withheld changed the original")
	}
}
