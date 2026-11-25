//go:build integration

package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// plantSpeedCut plants, far from every fixture area, three Gemeinden in a
// row 10 km apart (west, middle, east), an official fixed camera in the west
// one and a mobile check in the east one, and OpenStreetMap's cameras: one
// in each Gemeinde.
func plantSpeedCut(t *testing.T) {
	t.Helper()
	for _, r := range speedCutRows {
		plant(t, r)
	}
	// the read contract takes no delete; the rows lie far from every
	// fixture area and stay, as the planted departures do
}

// speedCutRows are plantSpeedCut's rows.
var speedCutRows = []string{
	`insert into vf_seed.place (place_id, place_kind, name, location, official_key, prominence, source_ref, origin_key, recorded_at, changed_at)
values ('99000001', 'municipality', 'Westdorf', st_setsrid(st_makepoint(-20.0, 75.0), 4326), '99000001', 1, 'bkg_gvisys', 'cut:w', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z'),
       ('99000002', 'municipality', 'Mitteldorf', st_setsrid(st_makepoint(-19.65, 75.0), 4326), '99000002', 1, 'bkg_gvisys', 'cut:m', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z'),
       ('99000003', 'municipality', 'Ostdorf', st_setsrid(st_makepoint(-19.3, 75.0), 4326), '99000003', 1, 'bkg_gvisys', 'cut:e', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z')`,
	`insert into vf_seed.speed_camera (camera_id, camera_kind, enforces, location, max_speed_kmh, name, street, data_as_of, valid_from, valid_to, source_ref, recorded_at)
values ('se:990001', 'fixed', 'speed', st_setsrid(st_makepoint(-20.01, 75.0), 4326), 50, 'Westdorf', 'Hauptstraße', '2026-03-01', null, null, 'speed_alpha', '2026-03-28T00:00:00Z'),
       ('se:990002', 'mobile_announced', 'speed', st_setsrid(st_makepoint(-19.31, 75.0), 4326), 50, 'Ostdorf', 'Ring', '2026-03-01', '2026-03-28', '2026-04-30', 'speed_alpha', '2026-03-28T00:00:00Z'),
       ('osm:node990011', 'fixed', 'speed', st_setsrid(st_makepoint(-19.98, 75.0), 4326), 50, 'Blitzer West', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node990012', 'fixed', 'speed', st_setsrid(st_makepoint(-19.66, 75.0), 4326), 50, 'Blitzer Mitte', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node990013', 'fixed', 'speed', st_setsrid(st_makepoint(-19.29, 75.0), 4326), 50, 'Blitzer Ost', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z')`,
}

func TestOSMCamerasAreCutByGemeindeAsThePlatformCutsThem(t *testing.T) {
	st := seeded(t)
	plantSpeedCut(t)
	fams := families(t, st, "roads")
	q := roads.SpeedQuery{At: geo.Point{Lat: 75.0, Lon: -19.65}, RadiusM: 30000, Today: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC), Limit: 10}
	got, err := st.SpeedCameras(bounded(t), q, fams)
	if err != nil {
		t.Fatal(err)
	}
	var osm []string
	for _, c := range got.OSM {
		osm = append(osm, c.ID)
	}
	// the west Gemeinde has an official fixed camera: its OpenStreetMap
	// camera is cut; a mobile check covers no Gemeinde
	if !slices.Equal(osm, []string{"osm:node990012", "osm:node990013"}) || got.OSMTotal != 2 || got.OfficialTotal != 2 {
		t.Errorf("kept %v (total %d), official %d", osm, got.OSMTotal, got.OfficialTotal)
	}
	// without the official family nothing is cut
	if got, err := st.SpeedCameras(bounded(t), q, slices.DeleteFunc(slices.Clone(fams), func(f string) bool { return f == "speed_alpha" })); err != nil || got.OSMTotal != 3 {
		t.Errorf("without the official list: %+v %v", got.OSM, err)
	}
}

// OpenStreetMap's cameras come by name (none first) and then by the
// numeric OSM id, as today's statement orders them: ids of different digit
// counts in numeric order (the text id would put node12230389900 before
// node1277236977), ids beyond 32 bits among them.
func TestOSMCamerasComeByNameThenNumericID(t *testing.T) {
	st := seeded(t)
	plant(t, `insert into vf_seed.place (place_id, place_kind, name, location, official_key, prominence, source_ref, origin_key, recorded_at, changed_at)
values ('99000004', 'municipality', 'Nordfeld', st_setsrid(st_makepoint(-19.65, 76.0), 4326), '99000004', 1, 'bkg_gvisys', 'order:n', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z')`)
	plant(t, `insert into vf_seed.speed_camera (camera_id, camera_kind, enforces, location, max_speed_kmh, name, street, data_as_of, valid_from, valid_to, source_ref, recorded_at)
values ('osm:node12230389900', 'fixed', 'speed', st_setsrid(st_makepoint(-19.651, 76.0), 4326), 50, 'Blitzer', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node1277236977', 'fixed', 'speed', st_setsrid(st_makepoint(-19.652, 76.0), 4326), 50, 'Blitzer', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node4294967296', 'fixed', 'speed', st_setsrid(st_makepoint(-19.653, 76.0), 4326), 50, null, null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node99', 'fixed', 'speed', st_setsrid(st_makepoint(-19.654, 76.0), 4326), 50, null, null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node5', 'fixed', 'speed', st_setsrid(st_makepoint(-19.655, 76.0), 4326), 50, null, null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z')`)
	q := roads.SpeedQuery{At: geo.Point{Lat: 76.0, Lon: -19.65}, RadiusM: 2000, Today: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC), Limit: 10}
	got, err := st.SpeedCameras(bounded(t), q, families(t, st, "roads"))
	if err != nil {
		t.Fatal(err)
	}
	var osm []string
	for _, c := range got.OSM {
		osm = append(osm, c.ID)
	}
	want := []string{"osm:node5", "osm:node99", "osm:node4294967296", "osm:node1277236977", "osm:node12230389900"}
	if !slices.Equal(osm, want) {
		t.Errorf("order %v, want %v", osm, want)
	}
}

// The Gemeinden the official lists cover are kept under the versions of the
// official families and the gazetteer, as today's speed card keeps them: an
// official camera written while the versions stay does not move the cut;
// once its family's version moves, the next read cuts by it. Before a
// versions read states the gazetteer's, every read reads them.
func TestTheOfficialCellsAreKeptUnderTheirFamiliesVersions(t *testing.T) {
	st, cp := private(t, speedCutRows...)
	fams := families(t, st, "roads")
	q := roads.SpeedQuery{At: geo.Point{Lat: 75.0, Lon: -19.65}, RadiusM: 30000, Today: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC), Limit: 10}
	exec := func(sql string) {
		t.Helper()
		conn, err := cp.Owner(bounded(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(context.Background()) }()
		if _, err := conn.Exec(bounded(t), sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	kept := func() []string {
		t.Helper()
		got, err := st.SpeedCameras(bounded(t), q, fams)
		if err != nil {
			t.Fatal(err)
		}
		var osm []string
		for _, c := range got.OSM {
			osm = append(osm, c.ID)
		}
		return osm
	}
	exec(`insert into control.feed_state (ledger, key, updated_at, last_changed_at)
	      values ('ingest', 'gazetteer', now(), now() - interval '1 day'), ('feed', 'speed_alpha', now(), now() - interval '1 day') on conflict do nothing`)
	if _, err := st.FreshnessVersions(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if got := kept(); !slices.Equal(got, []string{"osm:node990012", "osm:node990013"}) {
		t.Fatalf("kept %v", got)
	}
	// An official fixed camera in the middle Gemeinde, its family's version unchanged.
	exec(`create temp table c1 as select * from speed_enforcement s where s.source_feed = 'speed_alpha' and s.kind = 'fixed' limit 1;
	      update c1 set id = 990777, external_id = 'cells-990777', geom = st_setsrid(st_makepoint(-19.64, 75.0), 4326)::geography;
	      insert into speed_enforcement select * from c1`)
	if got := kept(); !slices.Equal(got, []string{"osm:node990012", "osm:node990013"}) {
		t.Fatalf("with the versions unchanged the kept cells answer: kept %v", got)
	}
	exec(`update control.feed_state set last_changed_at = now() + interval '1 minute' where key = 'speed_alpha'`)
	if _, err := st.FreshnessVersions(bounded(t)); err != nil {
		t.Fatal(err)
	}
	if got := kept(); !slices.Equal(got, []string{"osm:node990013"}) {
		t.Fatalf("after the family's version moved: kept %v, want the east camera alone", got)
	}
	// A failed cells read fails the answer.
	exec(`update control.feed_state set last_changed_at = now() + interval '2 minutes' where key = 'speed_alpha'`)
	if _, err := st.FreshnessVersions(bounded(t)); err != nil {
		t.Fatal(err)
	}
	exec(`revoke select on speed_enforcement from api_reader`)
	if _, err := st.SpeedCameras(bounded(t), q, fams); err == nil {
		t.Fatal("a cameras read whose official cells cannot be read succeeded")
	}
}
