package features_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

type baysFake struct {
	out     parking.Bays
	err     error
	gotQ    parking.BayQuery
	gotFams []string
}

func (b *baysFake) Bays(_ context.Context, q parking.BayQuery, fams []string) (parking.Bays, error) {
	b.gotQ, b.gotFams = q, fams
	return b.out, b.err
}

func TestParkingBaysCreditsTheListedFamilies(t *testing.T) {
	f, b := &fake{}, &baysFake{}
	s := features.New(features.Repositories{Register: f, Bays: b}, features.Settings{Language: "en", PartDeadline: 50 * time.Millisecond}, func() time.Time { return now })
	k := key("commercial", []access.Scope{"parking"}, "p1", "p2")
	ctx := context.Background()
	// none listed: none credited
	a, err := s.ParkingBays(ctx, k, parking.BayQuery{Kind: "disabled", Limit: 5})
	if err != nil || len(a.Meta.Families) != 0 || !b.gotQ.Now.Equal(now) || len(b.gotFams) == 0 {
		t.Fatalf("empty: %+v %v %+v", a, err, b)
	}
	b.out = parking.Bays{Items: []parking.Bay{{ID: "x", Family: "p1"}, {ID: "y", Family: "p1"}}, Counts: parking.BayCounts{Total: 2, Sensors: 2}}
	if a, err = s.ParkingBays(ctx, k, parking.BayQuery{Kind: "disabled", Limit: 5}); err != nil || len(a.Items) != 2 || len(a.Meta.Families) != 1 {
		t.Fatalf("listed: %+v %v", a, err)
	}
	b.err = errStore
	if _, err = s.ParkingBays(ctx, k, parking.BayQuery{}); !errors.Is(err, errStore) {
		t.Fatalf("store: %v", err)
	}
}

func TestBayCountsAndSensor(t *testing.T) {
	two := 2
	if (parking.Bay{Spaces: &two}).Sensor() || !(parking.Bay{}).Sensor() {
		t.Error("sensor")
	}
	if (parking.BayCounts{Sensors: 5, Free: 1, Occupied: 2}).Unknown() != 2 {
		t.Error("unknown")
	}
}
