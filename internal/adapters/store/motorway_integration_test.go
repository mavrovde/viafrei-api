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

// motorwaySeed is a motorway of the road operator's three sweeps (a jam
// growing over them), a works published as two sections, an announced
// closure, two measured sites (one per lane), a plan works in force and five
// weeks of the jam's history.
var motorwaySeed = []string{
	`select vf_test.ensure_day('public.autobahn_events', now()), vf_test.ensure_day('public.autobahn_events', now() - interval '25 minutes')`,
	`insert into public.autobahn_roads (road) values ('A99') on conflict do nothing`,
	`insert into public.autobahn_events (observed_at, road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, geom, extent, raw, raw_hash, source)
	 select now() - make_interval(mins => m), 'A99', 'warning', 'j1.de1', 'A99 | AS Haar - AS Ottobrunn', 'München -> Salzburg', 'STATIONARY_TRAFFIC', 1200 + 600 * (20 - m) / 10,
	        'SRID=4326;POINT(11.6 48.1)'::geography, '48.1,11.6,' || (48.1 + 0.01 * (30 - m)) || ',11.6',
	        jsonb_build_object('description', jsonb_build_array('Stau', 'zwischen AS Haar und AS Ottobrunn', '', 'Ereignismeldung:', '- Unfall', '- Durchschnittsgeschwindigkeit 10 km/h'),
	                           'routeRecommendation', jsonb_build_array('U1 ab AS Haar'), 'isBlocked', 'false',
	                           'startTimestamp', to_char((now() - interval '1 hour') at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')),
	        decode(md5('j1' || m), 'hex'), 'autobahn'
	   from unnest(array[20, 10, 1]) m
	 union all
	 select now() - interval '1 minute', 'A99', k, x, t, 'München -> Salzburg', null, null, 'SRID=4326;POINT(11.7 48.2)'::geography, '48.2,11.7,48.21,11.7',
	        jsonb_build_object('description', d, 'isBlocked', b, 'startTimestamp', to_char(s at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')),
	        decode(md5(x), 'hex'), 'autobahn'
	   from (values ('roadworks', 'r1.de1', 'A99 | Baustelle', jsonb_build_array('Beginn: 01.11.26 um 08:00 Uhr', 'Ende: 30.12.26 um 18:00 Uhr', '(Ende der Gesamtmaßnahme: 31.12.27)'), 'false', now() - interval '3 days'),
	                ('roadworks', 'r1.de2', 'A99 | Baustelle', jsonb_build_array('Beginn: 01.11.26 um 08:00 Uhr', 'Ende: 30.12.26 um 18:00 Uhr', '(Ende der Gesamtmaßnahme: 31.12.27)'), 'false', now() - interval '3 days'),
	                ('closure', 'c1', 'A99 | Vollsperrung', jsonb_build_array('Vollsperrung'), 'true', now() + interval '2 days')) v(k, x, t, d, b, s)`,
	`insert into public.autobahn_jam_hours (hour_start, road, jam_titles, jam_subtitles)
	 select ((date_trunc('hour', now() at time zone 'Europe/Berlin') - make_interval(days => 7 * g)) at time zone 'Europe/Berlin') + make_interval(hours => h), 'A99',
	        case when h = 0 then array['A99 | AS Haar - AS Ottobrunn'] else array[]::text[] end, case when h = 0 then array['München -> Salzburg'] else array[]::text[] end
	   from generate_series(1, 6) g, generate_series(-1, 1) h`,
	`insert into public.los_sites (site_id, carriageway, geom, road, first_seen_at, last_seen_at, source_feed, source_publisher)
	 values ('s1', 'hfb', 'SRID=4326;POINT(11.6 48.1)', 'A99', now(), now(), 'los_nrw_sites', 'p'), ('mq.s2', 'hfb', 'SRID=4326;POINT(11.7 48.2)', 'A99', now(), now(), 'los_nrw_sites', 'p')`,
	`insert into public.los_state (site_id, status, measured_at, status_since, last_seen_at, source_feed, source_publisher)
	 values ('s1', 'congested', now() - interval '2 minutes', now(), now(), 'los_nrw', 'p')`,
	`insert into public.los_lane_sites (lane_id, site_id, lane, first_seen_at, last_seen_at, source_feed, source_publisher)
	 values ('fs.s2_1', 'mq.s2', 1, now(), now(), 'los_nrw_lane_sites', 'p')`,
	`insert into public.los_lane_state (lane_id, site_id, status, measured_at, status_since, last_seen_at, source_feed, source_publisher)
	 values ('fs.s2_1', 'mq.s2', 'impossible', now() - interval '1 minute', now(), now(), 'los_nrw_lanes', 'p')`,
	`insert into public.tmc_locations (cid, tabcd, lcd, lcl_version, class, tcd, stcd, name, road_number, geom)
	 values (58, 1, 990001, '1.0', 'P', 1, 1, 'Haar', 'A99', 'SRID=4326;POINT(11.61 48.1)'), (58, 1, 990002, '1.0', 'P', 1, 1, 'Ottobrunn', 'A99', 'SRID=4326;POINT(11.71 48.2)')`,
	`insert into public.road_events (feed_key, source_id, publication_id, version, payload, event_type, road_number, overall_start, overall_end, source_feed, first_seen_at, updated_at)
	 values ('roadworks_akd', 's', 'p1', 1, '{"situationRecord": [{"cause": [{"causeDescription": [{"values": [{"value": [{"#text": "Mäharbeiten"}]}]}]}]}]}', 'MaintenanceWorks', 'A99',
	         now() - interval '1 hour', now() + interval '1 hour', 'roadworks_akd', now(), now())`,
}

func TestMotorwayReadsTheToolsStatements(t *testing.T) {
	st, pdb := private(t, motorwaySeed...)
	now := time.Now()
	q, ok := roads.MotorwayQueryOf("A99", 10, now, append(every(t), "autobahn", "roadworks_akd", "los_nrw", "los_nrw_sites", "los_nrw_lanes", "los_nrw_lane_sites"))
	if !ok || !q.Lanes {
		t.Fatalf("query %+v %v", q, ok)
	}
	r, err := st.Motorway(bounded(t), q)
	if err != nil || r.Tick.IsZero() {
		t.Fatalf("%+v %v", r, err)
	}
	k := roads.CountsOf(r.Counts)
	if k.Jams != 1 || k.Warnings != 1 || k.Roadworks != 1 || k.AnnouncedClosures != 1 || k.AnnouncedFrom == nil {
		t.Errorf("counts %+v from %+v", k, r.Counts)
	}
	if len(r.Events) != 2 || !r.Events[0].IsJam() || r.Events[1].Segments != 2 || len(r.Lamp) != 2 {
		t.Fatalf("events %+v", r.Events)
	}
	if m := r.Measured; m == nil || m.Sites != 2 || m.Slow != 1 || m.Standstill != 1 || m.LaneSites != 1 || !slices.Equal(m.SlowNear, []string{"Ottobrunn", "Haar"}) {
		t.Errorf("measured %+v", r.Measured)
	}
	if len(r.Plans) != 1 || *r.Plans[0].Cause != "Mäharbeiten" {
		t.Errorf("plans %+v", r.Plans)
	}
	if len(r.JamTicks) != 3 || !r.HistoryReady || len(r.JamUsual) != 1 || r.JamUsual[0].WeeksObserved != 6 || r.JamUsual[0].WeeksWithJam != 6 {
		t.Errorf("jam %+v %v %+v", r.JamTicks, r.HistoryReady, r.JamUsual)
	}
	a := roads.MotorwayAnswerOf(r, 3, now)
	j := a.Entries[0].Jam
	if j == nil || j.Trend == nil || j.Trend.Direction != "growing" || j.Usual == nil || j.Usual.Verdict != "usual" || j.End == nil || *j.End.Place != "AS Haar" ||
		!slices.Equal(j.Causes, []string{"Unfall"}) || !slices.Equal(j.Detours, []string{"U1 ab AS Haar"}) {
		t.Errorf("jam detail %+v", j)
	}
	if p := a.Entries[1].Phase; p.Start == nil || p.End == nil || p.MeasureEnd == nil {
		t.Errorf("phase %+v", p)
	}
	// Without the measured state, the lanes, or a plan the key may read;
	// and a road with no sweep.
	q.Measured, q.Lanes, q.Plans, q.Limit = true, false, nil, 1
	if r, err = st.Motorway(bounded(t), q); err != nil || r.Measured == nil || r.Measured.Sites != 1 || r.Plans != nil || len(r.Events) != 1 {
		t.Errorf("no lanes: %+v %v", r, err)
	}
	q.Measured = false
	if r, err = st.Motorway(bounded(t), q); err != nil || r.Measured != nil {
		t.Errorf("no measured: %+v %v", r.Measured, err)
	}
	// Every statement's fault fails the read.
	fst, f := faultyOn(t, pdb.ReaderURL)
	all := q
	all.Measured, all.Lanes, all.Plans, all.Limit = true, true, []string{"roadworks_akd"}, 10
	failEach(t, fst, f, "Motorway", func(ctx context.Context, s *store.Store) error { _, err := s.Motorway(ctx, all); return err })
	q.Road = "A98"
	if r, err = st.Motorway(bounded(t), q); err != nil || !r.Tick.IsZero() {
		t.Errorf("no sweep: %+v %v", r, err)
	}
}

// A motorway whose jam history is younger than the minimum is not asked its
// usual; nor is a road without a listed jam asked its sweeps.
func TestMotorwayYoungHistory(t *testing.T) {
	st, _ := private(t, append(slices.Clone(motorwaySeed[:3]), `delete from public.autobahn_events where kind = 'warning'`)...)
	now := time.Now()
	q, _ := roads.MotorwayQueryOf("A99", 10, now, []string{"autobahn"})
	r, err := st.Motorway(bounded(t), q)
	if err != nil || len(r.Events) != 1 || r.JamTicks != nil {
		t.Fatalf("%+v %v", r, err)
	}
	st, _ = private(t, motorwaySeed[:3]...)
	if r, err = st.Motorway(bounded(t), q); err != nil || r.HistoryReady || r.JamUsual != nil || len(r.JamTicks) != 3 {
		t.Errorf("%+v %v", r, err)
	}
	// The oldest hour is kept: a second read asks no history.
	if r, err = st.Motorway(bounded(t), q); err != nil || r.HistoryReady {
		t.Errorf("kept: %+v %v", r, err)
	}
}
