//go:build integration

package store_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// tripUpdatesOwnTheirLicence makes the private database's register as the
// platform's: the Trip Updates source owns the register row of its licence
// ('delfi_rt_tu'), so the register publishes it as 'tripupdates' alone.
var tripUpdatesOwnTheirLicence = []string{
	`delete from public.feed_catalog where feed_key = 'delfi_rt_tu'`,
	`update public.feed_catalog set licence_id = 'delfi_rt_tu' where feed_key = 'tripupdates'`,
	`insert into public.source_register (source_id, attribution_text, source_url, licence_label, share_alike, share_alike_database,
	                                    use_classes, consumer_answers_only, no_redistribution, register_version)
	 values ('delfi_rt_tu', 'DELFI', 'https://example.org/delfi_rt_tu', 'CC BY-SA 4.0', true, false,
	         array['consumer', 'non_commercial', 'commercial'], false, false, current_date)
	 on conflict (source_id) do nothing`,
}

// As on the platform, the Trip Updates source owns the register row of its
// licence, so the register publishes their figures under the source's key, not
// the licence's id: a board reads a call's Trip Updates figure under that
// family, and every family a call carries is one the read was given (the
// licence gate's post-check refused every such board, 500, when the figure
// was stamped with the licence's id).
func TestABoardCarriesOnlyTheFamiliesItWasGiven(t *testing.T) {
	const station = "1:de:09999:77"
	st, cp := private(t, append([]string{
		`insert into vf_seed.departure (stop_place_id, stop_point_id, journey_id, call_order, line_name, transport_mode, planned_departure_at,
		   boarding, realtime_source, cancelled, delay_s, source_ref)
		 select '1:de:09999:77', '1:de:09999:77:1', '1:tu-board:' || to_char(now() at time zone 'Europe/Berlin', 'YYYYMMDD'), 1, 'RB 7', 'rail',
		        date_trunc('minute', now()) + interval '20 minutes', true, 'delfi_rt_tu', false, 300, 'delfi_static'`,
		// The trip calls at a stop after the station: the call departs.
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:09999:77:1:next', 'Weiter', vf_test.point(null), 0) on conflict do nothing`,
	}, tripUpdatesOwnTheirLicence...)...)
	conn, err := cp.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(bounded(t)) }()
	var at time.Time
	if err := conn.QueryRow(bounded(t), `select planned_departure_at from vf_seed.departure
		where stop_place_id = $1 and boarding and realtime_source = 'delfi_rt_tu'`, station).Scan(&at); err != nil {
		t.Fatalf("no call with a Trip Updates figure was planted: %v", err)
	}
	reg, err := st.Register(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	var fams []string
	for _, r := range reg {
		fams = append(fams, r.Family)
	}
	if slices.Contains(fams, "delfi_rt_tu") || !slices.Contains(fams, "tripupdates") {
		t.Fatalf("the register publishes the Trip Updates under the source's key: %v", fams)
	}
	ds, err := st.Departures(bounded(t), transit.DepartureQuery{StationID: station, From: at, Window: time.Minute, Limit: 5}, fams)
	if err != nil || len(ds) == 0 {
		t.Fatalf("%s: %+v %v", station, ds, err)
	}
	tu := false
	for _, d := range ds {
		for _, f := range d.Families {
			if !slices.Contains(fams, f) {
				t.Errorf("%s: a call carries %q, a family the read was not given", d.JourneyID, f)
			}
			tu = tu || f == "tripupdates"
		}
	}
	if !tu {
		t.Errorf("%s: no call credits the Trip Updates: %+v", station, ds)
	}
	// A key not given the Trip Updates gets no call that carries them.
	ds, err = st.Departures(bounded(t), transit.DepartureQuery{StationID: station, From: at, Window: time.Minute, Limit: 5},
		slices.DeleteFunc(slices.Clone(fams), func(f string) bool { return f == "tripupdates" }))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if slices.Contains(d.Families, "tripupdates") {
			t.Errorf("%s: a call carries the Trip Updates the read was not given", d.JourneyID)
		}
	}
}

// TestTheLampsReferenceMovesWithWhatAJunctionsStateIsDerivedFrom: a
// junction's state is the Land of the nearest municipality within 20 km,
// so a moved junction, a municipality of another Land and a municipality
// closed each move the lamp's reference, and nothing else moves with it.
func TestTheLampsReferenceMovesWithWhatAJunctionsStateIsDerivedFrom(t *testing.T) {
	const junction = `(select t.ctid from public.tmc_locations t where t.cid = 58 and t.tabcd = 1 and t.tcd = 1
		and t.road_number ~ '^A[1-9][0-9]{0,2}$' and t.geom is not null and t.name is not null order by t.lcd limit 1)`
	const city = `(select g.id from public.gazetteer_places g where g.closed_at is null and g.source_id = 'bkg_gvisys'
		and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$' and g.geom is not null order by g.id limit 1)`
	st, cp := private(t)
	conn, err := cp.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(bounded(t)) }()
	var n int
	if err := conn.QueryRow(bounded(t), `select (select count(*) from public.tmc_locations t where t.ctid = `+junction+`) +
		(select count(*) from public.gazetteer_places g where g.id = `+city+`)`).Scan(&n); err != nil || n != 2 {
		dbAsk(t, "the seed holds no motorway junction or no BKG municipality: no junction's state is derived")
	}
	read := func() provenance.Versions {
		t.Helper()
		vs, err := st.FreshnessVersions(bounded(t))
		if err != nil || vs.LampReference == "" {
			t.Fatalf("%+v %v", vs, err)
		}
		return vs
	}
	seen := map[string]bool{read().LampReference: true}
	for _, change := range []string{
		`update public.tmc_locations set geom = st_project(geom, 500, 0) where ctid = ` + junction,
		`update public.gazetteer_places set admin_code = case when left(admin_code, 2) = '09' then '08' else '09' end || substr(admin_code, 3) where id = ` + city,
		`update public.gazetteer_places set geom = st_project(geom, 500, 0) where id = ` + city,
		`update public.gazetteer_places set closed_at = now() where id = ` + city,
	} {
		before := read()
		if _, err := conn.Exec(bounded(t), change); err != nil {
			t.Fatalf("%s: %v", change, err)
		}
		moved := read()
		if seen[moved.LampReference] {
			t.Errorf("%s did not move the lamp's reference", change)
		}
		seen[moved.LampReference] = true
		if moved.Register != before.Register || moved.RulesReference != before.RulesReference || !maps.Equal(moved.Families, before.Families) {
			t.Errorf("%s moved more than the lamp's reference", change)
		}
	}
}

// The trip delays of an area are read under the Trip Updates' family as the
// register publishes it, like a board's figures: a key given the published
// family is answered with them (they were stamped with the licence's id, a
// family no key holds where the source owns its licence's row, and never
// served). The area's punctuality waits for the database (punctualityFamily).
func TestAnAreasTripDelaysAreReadUnderThePublishedFamily(t *testing.T) {
	const area = "09162"
	st, _ := private(t, append([]string{
		// A Trip Updates call seeded as the board test's: the seed then
		// holds the Trip Updates source the register publishes.
		`insert into vf_seed.departure (stop_place_id, stop_point_id, journey_id, call_order, line_name, transport_mode, planned_departure_at,
		   boarding, realtime_source, cancelled, delay_s, source_ref)
		 select '1:de:09999:78', '1:de:09999:78:1', '1:tu-area:' || to_char(now() at time zone 'Europe/Berlin', 'YYYYMMDD'), 1, 'RB 7', 'rail',
		        date_trunc('minute', now()) + interval '20 minutes', true, 'delfi_rt_tu', false, 300, 'delfi_static'`,
		`insert into public.transit_delays (observed_at, feed_ts, trip_id, stop_id, stop_sequence, delay_s, schedule_relationship, source)
		 select now() - interval '2 minutes', now() - interval '2 minutes', v.trip, 'de:09162:1', 1, v.delay, 'SCHEDULED', 'delfi_rt_tu'
		 from (values ('tu-area-1', 900), ('tu-area-2', 60)) v(trip, delay)`,
	}, tripUpdatesOwnTheirLicence...)...)
	reg, err := st.Register(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	var fams []string
	for _, r := range reg {
		fams = append(fams, r.Family)
	}
	if slices.Contains(fams, "delfi_rt_tu") || !slices.Contains(fams, "tripupdates") {
		t.Fatalf("the register publishes the Trip Updates under the source's key: %v", fams)
	}
	d, err := st.Disruptions(bounded(t), transit.DisruptionQuery{Area: area, Limit: 5, Now: time.Now(), LateOverMin: 5}, fams)
	if err != nil {
		t.Fatal(err)
	}
	if d.Trips == nil || d.Trips.Observed != 2 || d.Trips.Late != 1 || d.Trips.Family != "tripupdates" {
		t.Errorf("trips %+v, want 2 observed, 1 late, of tripupdates", d.Trips)
	}
}
