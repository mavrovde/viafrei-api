package energy_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestOperatorKey(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004")
	for name, want := range map[string]string{
		"EnBW mobility+ AG":       "enbw",
		"ENBW":                    "enbw",
		"Stadtwerke München GmbH": "münchen",
		"bp pulse":                "bp",
		"E.ON Drive":              "eon",
		"Tesla Germany GmbH":      "tesla",
	} {
		if got, ok := energy.OperatorKey(name); !ok || got != want {
			t.Errorf("%q: %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"GmbH & Co. KG", "", "ab"} {
		if got, ok := energy.OperatorKey(name); ok {
			t.Errorf("%q names no brand, got %q", name, got)
		}
	}
	if sql := energy.OperatorKeySQL("c.operator_name"); !strings.Contains(sql, "'gmbhcokg'") || !strings.Contains(sql, "'bp', 'gp'") {
		t.Errorf("sql %s", sql)
	}
}

func TestFreeNow(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004", "FR-ENERGY-005")
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	limit := func(string) time.Duration { return 30 * time.Minute }
	point := func(s energy.State, age time.Duration) energy.ChargePoint {
		return energy.ChargePoint{Availability: &energy.Availability{State: s, ObservedAt: now.Add(-age), Family: "f"}}
	}
	for _, c := range []struct {
		points []energy.ChargePoint
		want   energy.FreeNow
	}{
		{[]energy.ChargePoint{point(energy.StateOccupied, 0), point(energy.StateAvailable, time.Minute)}, energy.Free},
		{[]energy.ChargePoint{point(energy.StateOutOfOrder, 0), point(energy.StateAvailable, time.Hour)}, energy.NotFree},
		{[]energy.ChargePoint{point(energy.StateOccupied, time.Minute)}, energy.NotFree},
		{[]energy.ChargePoint{point(energy.StateAvailable, time.Hour), point(energy.StateUnknown, 0)}, energy.Stale},
		{[]energy.ChargePoint{{}, point(energy.StateOther, 0)}, energy.NoStatus},
		{nil, energy.NoStatus},
	} {
		if got := (energy.Site{Points: c.points}).FreeNow(now, limit); got != c.want {
			t.Errorf("%+v: %s, want %s", c.points, got, c.want)
		}
	}
}
