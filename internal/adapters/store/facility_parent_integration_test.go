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
