package transit_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func TestTrainDepartureReadsAsTheBoardStatesIt(t *testing.T) {
	at := time.Date(2026, 11, 17, 15, 42, 0, 0, time.UTC)
	late := at.Add(5*time.Minute + 31*time.Second)
	s := func(v string) *string { return &v }
	d := transit.TrainDeparture{PlannedAt: at, ChangedAt: &late, Platform: s("5"), ChangedPlatform: s("7")}
	if !d.ExpectedAt().Equal(late) || d.DelayMin() != 6 || !d.PlatformChanged() {
		t.Errorf("changed: %v %d %v", d.ExpectedAt(), d.DelayMin(), d.PlatformChanged())
	}
	same := transit.TrainDeparture{PlannedAt: at, Platform: s("5"), ChangedPlatform: s("5")}
	if !same.ExpectedAt().Equal(at) || same.DelayMin() != 0 || same.PlatformChanged() {
		t.Errorf("same: %+v", same)
	}
	if !(transit.TrainDeparture{ChangedPlatform: s("2")}).PlatformChanged() || (transit.TrainDeparture{Platform: s("2")}).PlatformChanged() {
		t.Error("platform changed without a planned one")
	}
}
