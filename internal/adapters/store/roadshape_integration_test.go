//go:build integration

package store_test

import (
	"encoding/json"
	"slices"
	"strings"
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
		// Every road's length and Länder, and one road's exits with their
		// Länder, equal the views' own, as JSON (parity leaves the length
		// out of its comparison).
		all, _, err := st.Motorways(ctx, roads.CatalogueQuery{Limit: 500}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var gotShapes, wantShapes string
		gotJSON, _ := json.Marshal(func() (o []any) {
			for _, m := range all {
				o = append(o, []any{m.Road, m.LengthKm, m.States})
			}
			return o
		}())
		gotShapes = string(gotJSON)
		owner(t, `select coalesce(json_agg(json_build_array(road_ref, length_km::float8, coalesce(states, '{}')) order by length(road_ref), road_ref), '[]')::text
from contract.road`, &wantShapes)
		if gotShapes != canonical(t, wantShapes) {
			t.Fatalf("%v: the list's shapes %s, the view's %s", extra, gotShapes, wantShapes)
		}
		one, _, err := st.Motorways(ctx, roads.CatalogueQuery{Road: ref, Limit: 1}, nil, time.Now())
		if err != nil || len(one) != 1 {
			t.Fatalf("%v: %s: %v %v", extra, ref, one, err)
		}
		var gotExits, wantExits string
		exits := map[string]*string{}
		for _, j := range one[0].Exits {
			exits[j.ID] = j.State
		}
		b, _ := json.Marshal(exits)
		gotExits = string(b)
		owner(t, `select coalesce(json_object_agg(junction_id, state_code), '{}')::text from contract.junction where road_ref = '`+strings.ReplaceAll(ref, "'", "''")+`'`, &wantExits)
		if gotExits != canonical(t, wantExits) {
			t.Fatalf("%v: %s's exits %s, the view's %s", extra, ref, gotExits, wantExits)
		}
		if _, _, _, err := st.ParkingNear(ctx, parking.Query{Road: ref, Limit: 5}, listed, listed, time.Now()); err != nil {
			t.Fatalf("%v: the parking along %s: %v", extra, ref, err)
		}
		st.Close()
		if extra == nil {
			// The shapes answer from the snapshot; a road not asked yet has
			// no junction snapshot to answer.
			if _, _, err := st.Motorways(ctx, roads.CatalogueQuery{Road: "not asked", Limit: 1}, nil, time.Now()); err == nil {
				t.Fatal("the exits of a road not asked read no junction Länder from a closed pool")
			}
			continue
		}
		if _, _, err := st.Motorways(ctx, roads.CatalogueQuery{Limit: 5}, nil, time.Now()); err == nil {
			t.Fatal("the list read no road shapes from a closed pool")
		}
		if _, _, err := st.Motorways(ctx, roads.CatalogueQuery{Road: ref, Limit: 1}, nil, time.Now()); err == nil {
			t.Fatal("the exits read no junction Länder from a closed pool")
		}
		if _, _, _, err := st.ParkingNear(ctx, parking.Query{Road: ref, Limit: 5}, listed, listed, time.Now()); err == nil {
			t.Fatal("the parking read no road shapes from a closed pool")
		}
	}
}

// canonical is the JSON text s as encoding/json writes it.
func canonical(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
