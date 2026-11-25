//go:build integration

package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// roadStatusSeed is the motorway's sweep and live entries, a plan works in
// force with its source's last good run, a message closing the road, and
// two strategy sources: one checked now with a switched detour, one
// checked a day ago.
var roadStatusSeed = append(slices.Clone(motorwaySeed[:3]),
	`insert into public.road_events (feed_key, source_id, publication_id, version, payload, event_type, road_number, overall_start, overall_end, source_feed, first_seen_at, updated_at, last_seen_at)
	 values ('roadworks_bast', 's', 'b1', 1, '{"situationRecord": [{}]}', 'MaintenanceWorks', 'A99', now() - interval '1 day', now() + interval '1 day', 'roadworks_bast', now(), now(), now() - interval '1 hour'),
	        ('nms_events', 's', 'n1', 1, '{"situationRecord": [{}]}', 'roadClosed', 'A99', now() - interval '1 hour', now() + interval '1 hour', 'nms_events', now(), now(), now() - interval '5 minutes'),
	        ('routing_bab', 'r', 'g1', 1, '{"situationRecord": [{"cause": [{"causeDescription": [{"values": [{"value": [{"#text": "Unfall", "@lang": "de"}]}]}]}], "alternativeRoute": [{"roadNumber": {"#text": "A92"}}]}]}',
	         'reroutingManagement', 'A99', now() - interval '1 hour', now() + interval '1 hour', 'routing_bab', now(), now(), now())`,
	// Deleted entries, a works plan and a detour, the platform's live
	// events view drops, so a read that loses its filter counts them.
	`insert into public.road_events (feed_key, source_id, publication_id, version, payload, event_type, road_number, overall_start, overall_end, source_feed, first_seen_at, updated_at, last_seen_at, deleted_at)
	 values ('roadworks_bast', 's', 'b9', 1, '{"situationRecord": [{}]}', 'MaintenanceWorks', 'A99', now() - interval '1 day', now() + interval '1 day', 'roadworks_bast', now(), now(), now() - interval '1 hour', now() - interval '1 minute'),
	        ('routing_bab', 'r', 'g9', 1, '{"situationRecord": [{"alternativeRoute": [{"roadNumber": {"#text": "A93"}}]}]}',
	         'reroutingManagement', 'A99', now() - interval '1 hour', now() + interval '1 hour', 'routing_bab', now(), now(), now(), now() - interval '1 minute')`,
	`insert into public.feed_runs (feed_key, started_at, finished_at, status) values ('roadworks_bast', now() - interval '2 minutes', now() - interval '1 minute', 'ok')`,
	`insert into public.feed_catalog (feed_key, provider, display_name, licence_id, cadence_s) select k, 'p', k, (select licence_id from public.feed_catalog limit 1), 300 from unnest(array['routing_bab', 'routing_dvl_ni']) k on conflict (feed_key) do update set cadence_s = 300, closed_at = null`,
	`insert into control.feed_state (ledger, key, updated_at, last_success_at) values ('feed', 'routing_bab', now(), now()), ('feed', 'routing_dvl_ni', now(), now() - interval '1 day')
	 on conflict (ledger, key) do update set last_success_at = excluded.last_success_at`,
)

func TestRoadStatusReadsTheToolsStatements(t *testing.T) {
	st, pdb := private(t, roadStatusSeed...)
	now := time.Now()
	q, ok := roads.RoadStatusQueryOf("A99", roads.ClassMotorway, 10, now, time.Time{}, append(every(t), "autobahn", "roadworks_bast", "nms_events", "routing_bab", "routing_dvl_ni"))
	if !ok {
		t.Fatalf("query %+v", q)
	}
	r, err := st.RoadStatus(bounded(t), q)
	if err != nil || r.Tick.IsZero() {
		t.Fatalf("%+v %v", r, err)
	}
	if len(r.Live) != 2 || r.Live[0].Total != 2 || r.Live[1].Segments != 2 || len(r.Planned) != 2 || r.Checked["roadworks_bast"].IsZero() {
		t.Errorf("read live %+v planned %+v checked %v", r.Live, r.Planned, r.Checked)
	}
	if len(r.StrategiesStale) != 1 || r.StrategiesStale[0].Family != "routing_dvl_ni" || len(r.Strategies) != 1 {
		t.Errorf("strategies %+v stale %+v", r.Strategies, r.StrategiesStale)
	}
	a := roads.RoadStatusAnswerOf(q, r)
	if a.Counts.Total != 4 || a.Counts.Closed != 2 || a.Counts.Current != 1 || len(a.Strategies) != 1 || a.Strategies[0].Kind != "detour" || a.Strategies[0].Via == nil || *a.Strategies[0].Via != "A92" {
		t.Errorf("answer %+v %+v", a.Counts, a.Strategies)
	}
	// Every statement's fault fails the read.
	fst, f := faultyOn(t, pdb.ReaderURL)
	failEach(t, fst, f, "RoadStatus", func(ctx context.Context, s *store.Store) error { _, err := s.RoadStatus(ctx, q); return err })
	// No sweep, no listed row, no current strategy source; no strategies off a motorway.
	q.Road, q.Strategies = "A98", []string{"routing_dvl_ni"}
	if r, err = st.RoadStatus(bounded(t), q); err != nil || !r.Tick.IsZero() || r.Live != nil || len(r.Planned) != 0 || r.Strategies != nil || len(r.StrategiesStale) != 1 {
		t.Errorf("no sweep: %+v %v", r, err)
	}
	q.Strategies, q.Open = nil, []string{"roadworks_bast"}
	if r, err = st.RoadStatus(bounded(t), q); err != nil || r.StrategiesStale != nil {
		t.Errorf("no strategies: %+v %v", r, err)
	}
}
