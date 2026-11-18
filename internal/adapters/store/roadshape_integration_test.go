//go:build integration

package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestRoadSnapshotTTL: the motorway list and the parking along a motorway
// answer a road's length, Länder and line from the road snapshot as the
// road view states them, with db.road_snapshot_ttl 0s (read on every
// request, so a closed pool fails them) as with the default.
func TestRoadSnapshotTTL(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	ctx := bounded(t)
	var (
		ref, state string
		km         *float64
	)
	owner(t, `select road_ref, coalesce(states[1], ''), length_km::float8 from contract.road
order by states is null, length_km is null, road_ref limit 1`, &ref, &state, &km)
	listed := families(t, seeded(t), "parking")
	for _, extra := range [][]string{{"VFAPI_DB_ROAD_SNAPSHOT_TTL=0s"}, nil} {
		st, err := store.Open(ctx, readerConfig(t, extra...))
		if err != nil {
			t.Fatal(err)
		}
		ms, _, err := st.Motorways(ctx, roads.CatalogueQuery{State: state, Limit: 500}, nil, time.Now())
		i := slices.IndexFunc(ms, func(m roads.Motorway) bool { return m.Road == ref })
		if err != nil || i < 0 || (km == nil) != (ms[i].LengthKm == nil) || (km != nil && *ms[i].LengthKm != *km) ||
			ms[i].States == nil || (state != "" && !slices.Contains(ms[i].States, state)) {
			t.Fatalf("%v: %s in %s: %+v %v", extra, ref, state, ms, err)
		}
		if _, _, _, err := st.ParkingNear(ctx, parking.Query{Road: ref, Limit: 5}, listed, listed, time.Now()); err != nil {
			t.Fatalf("%v: the parking along %s: %v", extra, ref, err)
		}
		st.Close()
		if extra == nil {
			continue
		}
		if _, _, err := st.Motorways(ctx, roads.CatalogueQuery{Limit: 5}, nil, time.Now()); err == nil {
			t.Fatal("the list read no road shapes from a closed pool")
		}
		if _, _, _, err := st.ParkingNear(ctx, parking.Query{Road: ref, Limit: 5}, listed, listed, time.Now()); err == nil {
			t.Fatal("the parking read no road shapes from a closed pool")
		}
	}
}
