//go:build integration

package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// sameLamp fails unless the lamp page of ref states what the road's own
// conditions read states for its lamp, over the whole road in both
// directions (#141).
func sameLamp(t *testing.T, st *store.Store, ref string, lamp roads.Page, fams roads.Families, now time.Time) {
	t.Helper()
	p, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: ref, Direction: roads.DirectionBoth,
		Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 1}, fams, now)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]any{
		"messages": {p.Messages, lamp.Messages}, "warnings": {p.Warnings, lamp.Warnings}, "sources": {p.Sources, lamp.Sources},
		"confirmations": {p.Confirmations, lamp.Confirmations}, "states": {p.States, lamp.States}, "coverage": {p.Coverage, lamp.Coverage},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("%s %s: lamp read %+v, conditions read %+v", ref, name, pair[1], pair[0])
		}
	}
	if len(lamp.Items) != 0 || lamp.NextChange != nil {
		t.Errorf("%s: a lamp page lists %d situations, next change %v", ref, len(lamp.Items), lamp.NextChange)
	}
}

// TestRoadLampsReadWhatEachRoadsConditionsRead: the catalogue's one read of
// every listed road's lamp states, road by road, what each road's own
// conditions read states for its lamp, with and without weather families,
// for a road the register does not hold too.
func TestRoadLampsReadWhatEachRoadsConditionsRead(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	st := seeded(t)
	now, _ := busiest(t)
	conn, err := db.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(bounded(t), "select road_ref from contract.road where road_ref <> 'A101' order by 1 limit 4")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	_ = conn.Close(context.Background())
	if err != nil || len(refs) < 4 {
		t.Fatalf("%d roads: %v", len(refs), err)
	}
	// A101, whose situations the fixture plants, a road the register does
	// not hold, and a family covering the messages of a state an A101
	// junction lies in, so a lamp carries coverage.
	refs = append(refs, "A101", "A0")
	var state, family string
	owner(t, `select j.state_code, s.source_ref from contract.junction j, contract.source s
where j.road_ref = 'A101' and j.state_code is not null and not exists (select 1 from contract.message_coverage c where c.state_code = j.state_code and c.source_ref = s.source_ref)
order by j.road_ref, j.junction_id, s.source_ref limit 1`, &state, &family)
	change(t, `insert into vf_seed.message_coverage (state_code, source_ref, coverage_role, current_only) values ('`+state+`', '`+family+`', 'own', true)`)
	t.Cleanup(func() {
		change(t, `delete from contract.message_coverage where state_code = '`+state+`' and source_ref = '`+family+`'`)
	})
	for _, fams := range []roads.Families{
		{Roads: families(t, st, "roads"), Weather: families(t, st, "weather")},
		onRoads(families(t, st, "roads")),
	} {
		lamps, err := st.RoadLamps(bounded(t), refs, fams, now)
		if err != nil || len(lamps) != len(refs) {
			t.Fatalf("%d lamps of %d roads: %v", len(lamps), len(refs), err)
		}
		messages, covered := 0, 0
		for _, ref := range refs {
			sameLamp(t, st, ref, lamps[ref], fams, now)
			messages += len(lamps[ref].Messages)
			covered += len(lamps[ref].Coverage)
		}
		if messages == 0 || covered == 0 {
			t.Errorf("%d messages, %d states covered: the test compared nothing", messages, covered)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.RoadLamps(ctx, refs, onRoads([]string{"x"}), now); err == nil {
		t.Error("RoadLamps on an ended context succeeded")
	}
}
