package transit_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestStationAlertsAsToday(t *testing.T) {
	end := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	rows := []transit.StationAlertRow{
		{ID: "sx:1", MessageKey: "sa:1", Header: "secondary", Lines: []string{"RE10", "RE2"}, CarriedBy: []string{"sx"}},
		{ID: "sa:1", MessageKey: "sa:1", Header: "primary", Until: &end, Lines: []string{"RE2", "RE10"}, CarriedBy: []string{"sa"}},
		{ID: "sa:2", MessageKey: "sa:2", Header: "at stop", StopIDs: []string{"de:1:1"}, CarriedBy: []string{"sa"}},
		{ID: "sa:3", MessageKey: "sa:3", Header: "elsewhere", StopIDs: []string{"de:9:9"}, Lines: []string{"X"}},
		{ID: "sa:4", MessageKey: "sa:2", Header: "twin", Lines: []string{"RE2"}, CarriedBy: []string{"sa", "sx"}},
	}
	got := transit.StationAlerts(rows, []string{"de:1:1"}, []string{"RE10", "RE2"})
	if len(got) != 2 || got[0].Header != "at stop" || !got[0].AtStation || !slices.Equal(got[0].Lines, []string{"RE2"}) ||
		!slices.Equal(got[0].CarriedBy, []string{"sa", "sx"}) {
		t.Fatalf("%+v", got)
	}
	if m := got[1]; m.Header != "primary" || m.Until != &end || m.AtStation || !slices.Equal(m.Lines, []string{"RE2", "RE10"}) || !slices.Equal(m.CarriedBy, []string{"sa", "sx"}) {
		t.Errorf("%+v", m)
	}
}
