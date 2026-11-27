//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
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
		if extra == nil {
			// The background read: the junction Länder answer from it.
			if err := st.WarmRoads(ctx); err != nil {
				t.Fatal(err)
			}
		}
		// Every Land's roads equal the view's: its Länder or a junction's.
		var lands []string
		owner(t, `select coalesce(array_agg(distinct s order by s), '{}') from (
  select unnest(states) s from contract.road union select state_code from contract.junction where state_code is not null) l`, &lands)
		for _, land := range lands {
			// A leaf of its own each: the live junction test is one Land's
			// read of every junction (TST-012).
			t.Run(fmt.Sprintf("%v %s", extra, land), func(t *testing.T) {
				ms, _, err := st.Motorways(bounded(t), roads.CatalogueQuery{State: land, Limit: 500}, nil, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				var got, want []string
				for _, m := range ms {
					got = append(got, m.Road)
				}
				owner(t, `select coalesce(array_agg(road_ref order by length(road_ref), road_ref), '{}') from contract.road r
where r.display_class <> 'withhold' and r.audience = 'public' and ('`+land+`' = any(r.states)
  or exists (select 1 from contract.junction j where j.road_ref = r.road_ref and j.state_code = '`+land+`'))`, &want)
				if !slices.Equal(got, want) {
					t.Fatalf("the roads in %s: %v, the view's %v", land, got, want)
				}
			})
		}
		// The rest of this configuration's reads are bounded from here.
		ctx = bounded(t)
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

// TestColdRoadListServesLiveDuringTheJunctionRead: on a pod whose snapshot
// is empty, the background read holds the road shapes once its shape read
// is done; the junction read, road by road, is no flight a request waits
// for. While it is held part-way, a road's exits and a Land's list answer
// live, inside their own read, as the views state them.
func TestColdRoadListServesLiveDuringTheJunctionRead(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	var ref, state string
	owner(t, `select j.road_ref, j.state_code from contract.junction j where j.state_code is not null
order by j.road_ref, j.junction_id limit 1`, &ref, &state)
	st := openStore(t, bounded(t))
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	st.HoldJunctionRead(func() {
		once.Do(func() { close(held) })
		<-release
	})
	warmed := make(chan error, 1)
	go func() { warmed <- st.WarmRoads(context.Background()) }()
	<-held
	// A request that waited for the junction read would wait for release;
	// its deadline is well short of the test's bound.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	one, _, err := st.Motorways(store.WithTest(ctx, t), roads.CatalogueQuery{Road: ref, Limit: 1}, nil, time.Now())
	if err != nil || len(one) != 1 {
		t.Fatalf("%s during the junction read: %v %v", ref, one, err)
	}
	exits := map[string]*string{}
	for _, j := range one[0].Exits {
		exits[j.ID] = j.State
	}
	b, _ := json.Marshal(exits)
	var wantExits string
	owner(t, `select coalesce(json_object_agg(junction_id, state_code), '{}')::text from contract.junction where road_ref = '`+strings.ReplaceAll(ref, "'", "''")+`'`, &wantExits)
	if string(b) != canonical(t, wantExits) {
		t.Fatalf("%s's exits during the junction read %s, the view's %s", ref, b, wantExits)
	}
	ms, _, err := st.Motorways(ctx, roads.CatalogueQuery{State: state, Limit: 500}, nil, time.Now())
	if err != nil || !slices.ContainsFunc(ms, func(m roads.Motorway) bool { return m.Road == ref }) {
		t.Fatalf("the roads in %s during the junction read: %v %v", state, ms, err)
	}
	close(release)
	if err := <-warmed; err != nil {
		t.Fatalf("the junction read after release: %v", err)
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

// The motorway catalogue counts the families of the situations in force on
// any road of the register, whatever the query keeps (a Land, one road, no
// road at all), as the frozen families statement read them on its own.
func TestMotorwaysCountTheFamiliesOfEveryRoad(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	ctx := bounded(t)
	now := time.Now()
	var fams []string
	var ref string
	owner(t, `select coalesce(array_agg(distinct source_ref order by source_ref), '{}') from contract.traffic_event`, &fams)
	owner(t, `select road_ref from contract.road order by road_ref limit 1`, &ref)
	conn, err := pgx.Connect(ctx, db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var want []string
	if err := conn.QueryRow(ctx, store.LegacyMotorwayFamilies(), fams, now).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("no family is counted on the fixture: the case proves nothing")
	}
	st := seeded(t)
	for _, q := range []roads.CatalogueQuery{{Limit: 500}, {Limit: 1}, {Road: ref, Limit: 1}, {Road: "A0", Limit: 1}, {State: "not a Land", Limit: 5}} {
		ms, counted, err := st.Motorways(ctx, q, fams, now)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		if !slices.Equal(counted, want) {
			t.Errorf("%+v: %d roads, families counted %q, want %q", q, len(ms), counted, want)
		}
	}
	// A situation in force on a road the register does not hold, of a
	// family no register road carries (its other situations are withdrawn),
	// is counted by neither statement.
	var lone string
	owner(t, `select source_ref from contract.traffic_event where event_id = '`+fixtures.PlantedEvent("ev-u-kind")+`'`, &lone)
	planted, pdb := private(t, `insert into vf_seed.traffic_event (event_id, event_kind, road_ref, road_class, location, valid_from, headline, source_ref,
    origin_key, published_at, recorded_at, changed_at)
  select '`+fixtures.PlantedEvent("ev-off-register")+`', 'jam', 'A9999', 'motorway', e.location, now() - interval '1 hour', 'AbnormalTraffic', e.source_ref,
    'ev-off-register', now(), now(), now()
  from contract.traffic_event e
  where e.event_id = '`+fixtures.PlantedEvent("ev-u-kind")+`'`,
		`update public.road_events set closed_at = now() - interval '2 hours' where feed_key = '`+lone+`' and road_number is distinct from 'A9999'`)
	pc, err := pgx.Connect(ctx, pdb.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close(context.WithoutCancel(ctx)) }()
	off := append(slices.Clone(fams), lone)
	var inForce int
	if err := pc.QueryRow(ctx, `select count(*) from contract.traffic_event where source_ref = '`+lone+`'
	   and road_ref = 'A9999' and withdrawn_at is null and valid_from <= $1 and (valid_to is null or valid_to > $1)`, now).Scan(&inForce); err != nil || inForce != 1 {
		t.Fatalf("the planted situation is not in force: %d, %v", inForce, err)
	}
	if err := pc.QueryRow(ctx, store.LegacyMotorwayFamilies(), off, now).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if _, counted, err := planted.Motorways(ctx, roads.CatalogueQuery{Limit: 500}, off, now); err != nil || slices.Contains(counted, lone) || !slices.Equal(counted, want) {
		t.Fatalf("off the register: families counted %q, want %q (%v)", counted, want, err)
	}
	// No family asked: none is counted, and the roads are still listed.
	ms, counted, err := st.Motorways(ctx, roads.CatalogueQuery{Limit: 500}, nil, now)
	if err != nil || len(counted) != 0 || len(ms) == 0 {
		t.Fatalf("no family: %d roads, %q, %v", len(ms), counted, err)
	}
}
