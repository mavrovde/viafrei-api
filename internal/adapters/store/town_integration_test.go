//go:build integration

package store_test

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// As the platform's stop resolver reads "town, stop", a text no station's
// name holds is read as a town (a district by its name, or a Gemeinde in it)
// and a stop of that district; a platform filed under its station's DHID
// with no parent named is the station's, in the search (once, as the
// station) and in the station's family alike.
func TestAStationIsFoundAsAStopInTheTownTheTextNames(t *testing.T) {
	st, _ := private(t,
		`insert into public.ags_regions (ags, level, name, name_norm, parent_ags, source_id)
		 values ('09998', 'kreis', 'Rosenstadt', 'rosenstadt', '09', 'destatis_gv') on conflict do nothing`,
		`insert into public.gazetteer_places (kind, name, name_norm, admin_code, geom, source_id, external_id)
		 values ('city', 'Kleinrosen', 'kleinrosen', '09998123', vf_test.point(null), 'bkg_gvisys', 'test-kleinrosen')`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:09998:901', 'Dornenplatz', vf_test.point(null), 1) on conflict do nothing`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:09998:901:1:1', 'Dornenplatz', vf_test.point(null), 0) on conflict do nothing`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:09998:902:0:1', 'Distelweg', vf_test.point(null), 0), (1, 'de:09998:902:0:2', 'Distelweg', vf_test.point(null), 0)
		 on conflict do nothing`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:09998:903', 'Hauptbahnhof', vf_test.point(null), 1), (1, 'de:09998:904', 'Dornenweg', vf_test.point(null), 1)
		 on conflict do nothing`,
		// Towns written without their suffix: the Gemeinde whose name
		// starts with the word and a space.
		`insert into public.ags_regions (ags, level, name, name_norm, parent_ags, source_id)
		 values ('06412', 'kreis', 'Frankfurt am Main', 'frankfurt am main', '06', 'destatis_gv'),
		        ('15002', 'kreis', 'Halle (Saale)', 'halle saale', '15', 'destatis_gv') on conflict do nothing`,
		`insert into public.gazetteer_places (kind, name, name_norm, admin_code, geom, source_id, external_id)
		 values ('city', 'Frankfurt am Main', 'frankfurt am main', '06412000', vf_test.point(null), 'bkg_gvisys', 'test-frankfurt'),
		        ('city', 'Halle (Saale)', 'halle saale', '15002000', vf_test.point(null), 'bkg_gvisys', 'test-halle')`,
		`insert into public.stops (gtfs_feed_key, id, name, geom, location_type)
		 values (1, 'de:06412:99001', 'Konstablerwache', vf_test.point(null), 1), (1, 'de:15002:99001', 'Marktplatz', vf_test.point(null), 1)
		 on conflict do nothing`,
		// The town's main station is a DB station; its other stops are not.
		`insert into public.db_stations (station_no, name, content_hash, first_seen_at, last_seen_at, updated_at, ifopt, source_id, eva_no)
		 values ('999000903', 'Rosenstadt Hbf', 'test', now(), now(), now(), 'de:09998:903', 'db_stada', '9999903')`)
	const fam = "delfi_static" // the source of the timetable the stops are planted in
	ids := func(ss []transit.Station) []string {
		out := []string{}
		for _, s := range ss {
			out = append(out, s.ID)
		}
		return out
	}
	ss, _, err := st.Stations(bounded(t), transit.Query{Text: "Dornenplatz", Limit: 5}, []string{fam})
	if err != nil || !slices.Equal(ids(ss), []string{"1:de:09998:901"}) || ss[0].InTown {
		t.Errorf("the name alone: %v, %v", ids(ss), err)
	}
	for _, asked := range []string{"Rosenstadt Dornenplatz", "Dornenplatz, Rosenstadt", "Kleinrosen Dornenplatz"} {
		ss, _, err := st.Stations(bounded(t), transit.Query{Text: asked, Limit: 5}, []string{fam})
		if err != nil || !slices.Equal(ids(ss), []string{"1:de:09998:901"}) || !ss[0].InTown {
			t.Errorf("%q: %+v, %v", asked, ss, err)
		}
	}
	for asked, want := range map[string]string{"Frankfurt Konstablerwache": "1:de:06412:99001", "Halle Marktplatz": "1:de:15002:99001", "Kreis Rosenstadt, Dornenplatz": "1:de:09998:901"} {
		if ss, _, err := st.Stations(bounded(t), transit.Query{Text: asked, Limit: 5}, []string{fam}); err != nil || !slices.Equal(ids(ss), []string{want}) {
			t.Errorf("%q: %v, %v", asked, ids(ss), err)
		}
	}
	// The town alone names no stop.
	if ss, _, err := st.Stations(bounded(t), transit.Query{Text: "Kreis Rosenstadt", Limit: 5}, []string{fam}); err != nil || len(ss) != 0 {
		t.Errorf("the town alone: %v, %v", ids(ss), err)
	}
	// Two stations of the town hold the stop name: by name, then id.
	if ss, _, err := st.Stations(bounded(t), transit.Query{Text: "Rosenstadt Dornen", Limit: 5}, []string{fam}); err != nil || !slices.Equal(ids(ss), []string{"1:de:09998:901", "1:de:09998:904"}) {
		t.Errorf("two stations in the town: %v, %v", ids(ss), err)
	}
	// The town's main station, by the main-station words alone.
	for _, asked := range []string{"Rosenstadt Hbf", "Rosenstadt Hauptbahnhof"} {
		if ss, _, err := st.Stations(bounded(t), transit.Query{Text: asked, Limit: 5}, []string{fam}); err != nil || !slices.Equal(ids(ss), []string{"1:de:09998:903"}) {
			t.Errorf("%q: %v, %v", asked, ids(ss), err)
		}
	}
	// A railway search keeps the town's DB stations: its main station, and
	// none of the stops that are no DB station.
	if ss, _, err := st.Stations(bounded(t), transit.Query{Text: "Rosenstadt Hbf", Limit: 5, Railway: true}, []string{fam}); err != nil || !slices.Equal(ids(ss), []string{"1:de:09998:903"}) {
		t.Errorf("the town's railway station: %v, %v", ids(ss), err)
	}
	if ss, _, err := st.Stations(bounded(t), transit.Query{Text: "Rosenstadt Dornen", Limit: 5, Railway: true}, []string{fam}); err != nil || len(ss) != 0 {
		t.Errorf("no railway station in the town holds the name: %v, %v", ids(ss), err)
	}
	if ss, _, err := st.Stations(bounded(t), transit.Query{Text: "Rosenstadt Zzqxplatz", Limit: 5}, []string{fam}); err != nil || len(ss) != 0 {
		t.Errorf("no such stop in the town: %v, %v", ids(ss), err)
	}
	// A station whose DHID is no stop of its own: its first platform stands
	// for it, and its family is every platform under the DHID.
	ss, _, err = st.Stations(bounded(t), transit.Query{Text: "Distelweg", Limit: 5}, []string{fam})
	if err != nil || !slices.Equal(ids(ss), []string{"1:de:09998:902:0:1"}) {
		t.Errorf("a station of platforms only: %v, %v", ids(ss), err)
	}
	if sp, err := st.Station(bounded(t), "1:de:09998:902:0:1", []string{fam}); err != nil || !slices.Equal(sp.StopIDs, []string{"1:de:09998:902:0:1", "1:de:09998:902:0:2"}) {
		t.Errorf("its family: %v, %v", sp.StopIDs, err)
	}
	sp, err := st.Station(bounded(t), "1:de:09998:901", []string{fam})
	if err != nil || !slices.Equal(sp.StopIDs, []string{"1:de:09998:901", "1:de:09998:901:1:1"}) {
		t.Errorf("the family: %v, %v", sp.StopIDs, err)
	}
	sp, err = st.Station(bounded(t), "1:de:09998:901:1:1", []string{fam})
	if err != nil || !slices.Equal(sp.StopIDs, []string{"1:de:09998:901:1:1", "1:de:09998:901"}) {
		t.Errorf("the family of a platform: %v, %v", sp.StopIDs, err)
	}
}
