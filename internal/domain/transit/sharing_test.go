package transit_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// A station's count stands while its system's status is at most 15 minutes
// old; a bike is available when the count stands, the station rents (or
// does not say) and a vehicle is counted.
func TestSharingStationCurrentAndAvailable(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	fresh, stale := now.Add(-transit.SharingFreshS*time.Second), now.Add(-transit.SharingFreshS*time.Second-time.Second)
	one, none := 1, 0
	yes, no := true, false
	if !transit.SharingFresh(&fresh, now) || transit.SharingFresh(&stale, now) || transit.SharingFresh(nil, now) {
		t.Fatal("SharingFresh")
	}
	for _, c := range []struct {
		s                  transit.SharingStation
		current, available bool
	}{
		{transit.SharingStation{StatusSeenAt: &fresh, VehiclesAvailable: &one}, true, true},
		{transit.SharingStation{StatusSeenAt: &fresh, VehiclesAvailable: &one, IsRenting: &yes}, true, true},
		{transit.SharingStation{StatusSeenAt: &fresh, VehiclesAvailable: &one, IsRenting: &no}, true, false},
		{transit.SharingStation{StatusSeenAt: &fresh, VehiclesAvailable: &none}, true, false},
		{transit.SharingStation{StatusSeenAt: &stale, VehiclesAvailable: &one}, false, false},
		{transit.SharingStation{StatusSeenAt: &fresh}, false, false},
	} {
		if c.s.Current(now) != c.current || c.s.Available(now) != c.available {
			t.Errorf("%+v: current %v available %v", c.s, c.s.Current(now), c.s.Available(now))
		}
	}
}

// Electric and electrically assisted vehicles are electric; others and an
// unstated propulsion are not.
func TestSharingVehicleElectric(t *testing.T) {
	for p, want := range map[string]bool{"electric": true, "electric_assist": true, "human": false} {
		if got := (transit.SharingVehicle{Propulsion: &p}).Electric(); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
	if (transit.SharingVehicle{}).Electric() {
		t.Error("unstated")
	}
}
