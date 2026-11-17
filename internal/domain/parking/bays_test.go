package parking_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// A bay a sensor reports has no static spaces; the bays with no current
// reading are the sensor bays neither free nor occupied.
func TestBaySensorAndUnknownCount(t *testing.T) {
	spaces := 4
	if !(parking.Bay{}).Sensor() || (parking.Bay{Spaces: &spaces}).Sensor() {
		t.Fatal("Sensor")
	}
	if got := (parking.BayCounts{Sensors: 10, Free: 3, Occupied: 5}).Unknown(); got != 2 {
		t.Fatalf("Unknown: %d", got)
	}
}
