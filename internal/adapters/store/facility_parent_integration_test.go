//go:build integration

package store_test

import (
	"testing"
	"time"
)

// DB files a station's facilities under the DHID its platforms name as their
// parent, which need not be the DHID the timetable's station carries (S+U
// Berlin Hauptbahnhof): the station's facilities are those of its stop
// family's DHIDs and of their parents'.
func TestAStationAnswersTheFacilitiesFiledUnderItsPlatformsParent(t *testing.T) {
	st, _ := private(t,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:09998:910', 'Lindenhof', vf_test.point(null), 0), (1, 'de:09998:911', 'Lindenhof', vf_test.point(null), 1)
		 on conflict do nothing`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type, parent_id)
		 values (1, 'de:09998:910:1:1', 'Lindenhof', vf_test.point(null), 0, 'de:09998:911') on conflict do nothing`,
		`insert into public.db_stations (station_no, name, content_hash, first_seen_at, last_seen_at, updated_at, ifopt, source_id)
		 values ('999000911', 'Lindenhof', 'test', now(), now(), now(), 'de:09998:911', 'db_stada')`,
		`insert into public.db_facilities (equipment_no, station_no, kind, state, state_since, first_seen_at, last_seen_at, source_feed, source_publisher, description)
		 values ('9990001', '999000911', 'elevator', 'active', now(), now(), now(), 'db_fasta', 'DB', 'zu Gleis 1')`)
	fs, err := st.Facilities(bounded(t), "1:de:09998:910", []string{"db_fasta"}, time.Now())
	if err != nil || len(fs) != 1 || fs[0].ID != "fasta:9990001" {
		t.Errorf("the station's facilities: %+v, %v", fs, err)
	}
	if fs, err := st.Facilities(bounded(t), "1:de:09998:912", []string{"db_fasta"}, time.Now()); err != nil || len(fs) != 0 {
		t.Errorf("another station: %+v, %v", fs, err)
	}
}

// A state is current while its feed restates it, whenever it began: a lift
// in service for a week and restated a minute ago is in service (Hamburg
// Hbf read 49 of 49 unknown, judged on when each state began). A unit DB's
// FaSta and OpenStation both list (one equipment number) is FaSta's alone
// where the key may read FaSta; OpenStation adds the units FaSta does not
// list, and answers alone for a key that may not read FaSta (#222).
func TestAFacilityStateIsCurrentWhileRestatedAndAUnitIsListedOnce(t *testing.T) {
	st, _ := private(t,
		// FaSta's cadence, as prod states it: 300 s, so stale after 900 s.
		`insert into public.feed_catalog (feed_key, provider, display_name, licence_id, cadence_s)
		 values ('db_fasta', 'db', 'Deutsche Bahn AG', 'db_fasta', 300)
		 on conflict (feed_key) do update set cadence_s = 300`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:09998:920', 'Lindenhof Ost', vf_test.point(null), 1) on conflict do nothing`,
		`insert into public.db_stations (station_no, name, content_hash, first_seen_at, last_seen_at, updated_at, ifopt, source_id)
		 values ('999000920', 'Lindenhof Ost', 'test', now(), now(), now(), 'de:09998:920', 'db_stada')`,
		`insert into public.db_facilities (equipment_no, station_no, kind, state, state_since, first_seen_at, last_seen_at, source_feed, source_publisher, description)
		 values ('9990020', '999000920', 'elevator', 'active', now() - interval '7 days', now() - interval '7 days', now() - interval '1 minute', 'db_fasta', 'DB', 'zu Gleis 1'),
		        ('9990021', '999000920', 'elevator', 'active', now() - interval '7 days', now() - interval '7 days', now() - interval '1 day', 'db_fasta', 'DB', 'zu Gleis 2')`,
		`insert into public.station_equipment (equipment_id, kind, equipment_no, stop_place_id, description, first_seen_at, last_seen_at, source_feed, source_publisher)
		 values ('diid:9990020', 'elevator', '9990020', 'de:09998:920', 'zu Gleis 1', now(), now(), 'openstation_netex', 'DB'),
		        ('diid:9990022', 'escalator', '9990022', 'de:09998:920', 'zur Halle', now(), now(), 'openstation_netex', 'DB')`,
		`insert into public.station_equipment_state (equipment_id, status, state, status_since, last_seen_at, known_equipment, source_feed, source_publisher)
		 values ('diid:9990020', 'available', 'inactive', now() - interval '3 days', now() - interval '1 minute', true, 'openstation_siri_fm', 'DB'),
		        ('diid:9990022', 'available', 'active', now() - interval '3 days', now() - interval '1 minute', true, 'openstation_siri_fm', 'DB')`)
	all := []string{"db_fasta", "openstation_siri_fm", "openstation_netex"}
	fs, err := st.Facilities(bounded(t), "1:de:09998:920", all, time.Now())
	got := map[string]string{}
	for _, f := range fs {
		got[f.ID] = f.State + " " + f.Family
	}
	want := map[string]string{"fasta:9990020": "active db_fasta", "fasta:9990021": "unknown db_fasta", "equipment:diid:9990022": "active openstation_siri_fm"}
	if err != nil || len(got) != len(want) || len(fs) != len(want) {
		t.Fatalf("the station's facilities: %v, %v", got, err)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
	fs, err = st.Facilities(bounded(t), "1:de:09998:920", []string{"openstation_siri_fm", "openstation_netex"}, time.Now())
	if err != nil || len(fs) != 2 || fs[0].ID != "equipment:diid:9990020" || fs[0].State != "inactive" {
		t.Errorf("without FaSta, OpenStation answers alone: %+v, %v", fs, err)
	}
}
