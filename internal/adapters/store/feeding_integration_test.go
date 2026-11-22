//go:build integration

package store_test

import (
	"context"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestFeedingReadsTheFamiliesOfEachDataset: the families a dataset is fed
// by are those holding a row in its views, with db.feeding_snapshot_ttl 0s
// (read on every call) as with the snapshot the background keeps; asked
// for families that hold rows of nothing, it answers none.
func TestFeedingReadsTheFamiliesOfEachDataset(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	views := map[provenance.Dataset]string{
		provenance.DatasetFuel:         "select source_ref from contract.fuel_station",
		provenance.DatasetWarnings:     "select source_ref from contract.weather_warning",
		provenance.DatasetSharing:      "select source_ref from contract.sharing_station union select source_ref from contract.sharing_vehicle",
		provenance.DatasetRoadEvents:   "select source_ref from contract.traffic_event",
		provenance.DatasetSpeedCameras: "select source_ref from contract.speed_camera",
	}
	var every []string
	owner(t, `select array_agg(source_ref order by source_ref) from contract.source`, &every)
	for _, extra := range [][]string{{"VFAPI_DB_FEEDING_SNAPSHOT_TTL=0s"}, nil} {
		ctx := bounded(t)
		st, err := store.Open(ctx, readerConfig(t, extra...))
		if err != nil {
			t.Fatal(err)
		}
		for d, q := range views {
			var want []string
			owner(t, `select coalesce(array_agg(distinct f order by f), '{}') from (`+q+`) v(f)`, &want)
			got, err := st.Feeding(ctx, d, every)
			if err != nil || !slices.Equal(got, want) || len(want) == 0 {
				t.Errorf("%v %s: %v, the views' %v, %v", extra, d, got, want, err)
			}
			if got, err := st.Feeding(ctx, d, []string{"no_such_family"}); err != nil || len(got) != 0 {
				t.Errorf("%v %s: an unknown family holds %v, %v", extra, d, got, err)
			}
		}
		if extra == nil {
			// The background read answers from the snapshot until ctx ends.
			kctx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { st.KeepFeeding(kctx); close(done) }()
			if got, err := st.Feeding(ctx, provenance.DatasetFuel, every); err != nil || len(got) == 0 {
				t.Errorf("with the background read: %v, %v", got, err)
			}
			cancel()
			<-done
		}
		st.Close()
		if extra != nil {
			// Read on every call: a closed pool fails it.
			if _, err := st.Feeding(ctx, provenance.DatasetFuel, every); err == nil {
				t.Error("a closed pool answered the families of a dataset")
			}
		}
	}
}
