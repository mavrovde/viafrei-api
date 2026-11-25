//go:build integration

package store_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
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

// A state is current while its source restates it, whenever it began: a lift
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
		// The operator's explanation of a state is kept verbatim.
		`update public.db_facilities set state_explanation = 'under maintenance' where equipment_no = '9990021'`,
		`insert into public.station_equipment (equipment_id, kind, equipment_no, stop_place_id, description, first_seen_at, last_seen_at, source_feed, source_publisher)
		 values ('diid:9990020', 'elevator', '9990020', 'de:09998:920', 'zu Gleis 1', now(), now(), 'openstation_netex', 'DB'),
		        ('diid:9990022', 'escalator', '9990022', 'de:09998:920', 'zur Halle', now(), now(), 'openstation_netex', 'DB'),
		        ('diid:9990023', 'escalator', null, 'de:09998:920', 'zum Ausgang', now(), now(), 'openstation_netex', 'DB')`,
		`insert into public.station_equipment_state (equipment_id, status, state, status_since, last_seen_at, known_equipment, source_feed, source_publisher)
		 values ('diid:9990020', 'available', 'inactive', now() - interval '3 days', now() - interval '1 minute', true, 'openstation_siri_fm', 'DB'),
		        ('diid:9990022', 'available', 'active', now() - interval '3 days', now() - interval '1 minute', true, 'openstation_siri_fm', 'DB'),
		        ('diid:9990023', 'available', 'active', now() - interval '3 days', now() - interval '1 minute', true, 'openstation_siri_fm', 'DB')`)
	all := []string{"db_fasta", "openstation_siri_fm", "openstation_netex"}
	fs, err := st.Facilities(bounded(t), "1:de:09998:920", all, time.Now())
	got := map[string]string{}
	for _, f := range fs {
		// The unit's DB equipment number, as its source lists it; none for
		// the unit OpenStation gives no number.
		no := "-"
		if f.EquipmentNo != nil {
			no = *f.EquipmentNo
		}
		got[f.ID] = f.State + " " + f.Family + " " + no
		// a unit whose state is the status family's names its listing's
		// family (the inventory) beside it, as today's credit line does
		if f.Listing != "" {
			got[f.ID] += " listed " + f.Listing
		}
		if f.StateExplanation != nil {
			got[f.ID] += " " + *f.StateExplanation
		}
	}
	want := map[string]string{"fasta:9990020": "active db_fasta 9990020", "fasta:9990021": "unknown db_fasta 9990021 under maintenance",
		"equipment:diid:9990022": "active openstation_siri_fm 9990022 listed openstation_netex", "equipment:diid:9990023": "active openstation_siri_fm - listed openstation_netex"}
	if err != nil || len(got) != len(want) || len(fs) != len(want) {
		t.Fatalf("the station's facilities: %v, %v", got, err)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
	fs, err = st.Facilities(bounded(t), "1:de:09998:920", []string{"openstation_siri_fm", "openstation_netex"}, time.Now())
	if err != nil || len(fs) != 3 || fs[0].ID != "equipment:diid:9990020" || fs[0].State != "inactive" {
		t.Errorf("without FaSta, OpenStation answers alone: %+v, %v", fs, err)
	}
	// a key that may not read the inventory is not credited with it
	fs, err = st.Facilities(bounded(t), "1:de:09998:920", []string{"openstation_siri_fm"}, time.Now())
	for _, f := range fs {
		if f.Listing != "" {
			t.Errorf("%s: credited %q to a key that may not read it", f.ID, f.Listing)
		}
	}
	if err != nil {
		t.Error(err)
	}
}

// A railway search by name keeps the DB stations: two stop places share
// "Frankfurt (Main) Hauptbahnhof", a bus stop with no EVA number first by
// id, so the first station of a name search was the bus stop and the train
// board asked of it was empty (prod side-by-side, #222). With Railway the
// station of the EVA number answers, as the platform's train departures
// check resolves a name against the DB station register; the town and stop
// read keeps its DB stations too.
func TestARailwaySearchByNameKeepsTheDBStations(t *testing.T) {
	st, _ := private(t,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, '000099980001', 'Lindenhof Hauptbahnhof', vf_test.point(null), 0),
		        (1, 'de:09998:930', 'Lindenhof Hauptbahnhof', vf_test.point(null), 1) on conflict do nothing`,
		`insert into public.db_stations (station_no, name, content_hash, first_seen_at, last_seen_at, updated_at, ifopt, source_id, eva_no)
		 values ('999000930', 'Lindenhof Hbf', 'test', now(), now(), now(), 'de:09998:930', 'db_stada', '9999930')`)
	fams := families(t, st, "transit")
	for _, text := range []string{"Lindenhof Hbf", "Lindenhof(Hbf)"} {
		all, _, err := st.Stations(bounded(t), transit.Query{Text: text, Limit: 1}, fams)
		if err != nil || len(all) != 1 || all[0].ID != "1:000099980001" {
			t.Errorf("%s, every stop: %+v, %v", text, all, err)
		}
		rail, _, err := st.Stations(bounded(t), transit.Query{Text: text, Limit: 1, Railway: true}, fams)
		if err != nil || len(rail) != 1 || rail[0].ID != "1:de:09998:930" || rail[0].EVA == nil || *rail[0].EVA != "9999930" {
			t.Errorf("%s, railway: %+v, %v", text, rail, err)
		}
	}
}
