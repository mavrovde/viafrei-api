package transit_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestStateAlertsFoldAsTheLandPageFolds(t *testing.T) {
	end := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	later := end.Add(time.Hour)
	rows := []transit.StateAlertRow{
		// the secondary family's row comes first; the message keeps the primary's headline and end
		{ID: "sx:1", Header: "Störung (SX)", Until: &later, Lines: []string{"S 10", "S 2"}, MessageKey: "sa:1", CarriedBy: []string{"sa", "sx"}, Lands: []string{"09"}},
		{ID: "sa:1", Header: "Störung", Until: &end, Lines: []string{"S 2", "S 1"}, MessageKey: "sa:1", CarriedBy: []string{"sa", "sx"}, Lands: []string{"08"}},
		{ID: "sa:2", Header: "Bauarbeiten", Lines: []string{"RE 1"}, MessageKey: "sa:2", CarriedBy: []string{"sa"}, Lands: []string{"09"}},
		{ID: "sa:3", Header: "Aufzug", Lines: []string{"U 3"}, MessageKey: "sa:3", CarriedBy: []string{"sa"}, Lands: []string{"09"}},
		{ID: "sa:4", Header: "Anderswo", Lines: []string{"S 1", "S 2", "S 3", "S 4"}, MessageKey: "sa:4", CarriedBy: []string{"sa"}, Lands: []string{"05"}},
		// the primary row first: a later row of the message keeps it
		{ID: "sa:5", Header: "Ölspur", MessageKey: "sa:5", CarriedBy: []string{"sa"}, Lands: []string{"09"}},
		{ID: "sx:5", Header: "Ölspur (SX)", MessageKey: "sa:5", CarriedBy: []string{"sa"}, Lands: []string{"09"}},
	}
	got := transit.StateAlertsOf(rows, "09")
	want := []transit.StateAlert{
		{Header: "Störung", Lines: []string{"S 1", "S 2", "S 10"}, Until: &end, CarriedBy: []string{"sa", "sx"}},
		{Header: "Aufzug", Lines: []string{"U 3"}, CarriedBy: []string{"sa"}},
		{Header: "Bauarbeiten", Lines: []string{"RE 1"}, CarriedBy: []string{"sa"}},
		{Header: "Ölspur", Lines: []string{}, CarriedBy: []string{"sa"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("folded\n%+v\nwant\n%+v", got, want)
	}
	if got := transit.StateAlertsOf(rows, "01"); got == nil || len(got) != 0 {
		t.Errorf("no alert in the Land: %+v", got)
	}
}
