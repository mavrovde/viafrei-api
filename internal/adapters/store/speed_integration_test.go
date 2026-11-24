//go:build integration

package store_test

import (
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
	plant(t, `insert into vf_seed.place (place_id, place_kind, name, location, official_key, prominence, source_ref, origin_key, recorded_at, changed_at)
values ('99000001', 'municipality', 'Westdorf', st_setsrid(st_makepoint(-20.0, 75.0), 4326), '99000001', 1, 'bkg_gvisys', 'cut:w', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z'),
       ('99000002', 'municipality', 'Mitteldorf', st_setsrid(st_makepoint(-19.65, 75.0), 4326), '99000002', 1, 'bkg_gvisys', 'cut:m', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z'),
       ('99000003', 'municipality', 'Ostdorf', st_setsrid(st_makepoint(-19.3, 75.0), 4326), '99000003', 1, 'bkg_gvisys', 'cut:e', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z')`)
	plant(t, `insert into vf_seed.speed_camera (camera_id, camera_kind, enforces, location, max_speed_kmh, name, street, data_as_of, valid_from, valid_to, source_ref, recorded_at)
values ('se:990001', 'fixed', 'speed', st_setsrid(st_makepoint(-20.01, 75.0), 4326), 50, 'Westdorf', 'Hauptstraße', '2026-03-01', null, null, 'speed_alpha', '2026-03-28T00:00:00Z'),
       ('se:990002', 'mobile_announced', 'speed', st_setsrid(st_makepoint(-19.31, 75.0), 4326), 50, 'Ostdorf', 'Ring', '2026-03-01', '2026-03-28', '2026-04-30', 'speed_alpha', '2026-03-28T00:00:00Z'),
       ('osm:node990011', 'fixed', 'speed', st_setsrid(st_makepoint(-19.98, 75.0), 4326), 50, 'Blitzer West', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node990012', 'fixed', 'speed', st_setsrid(st_makepoint(-19.66, 75.0), 4326), 50, 'Blitzer Mitte', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z'),
       ('osm:node990013', 'fixed', 'speed', st_setsrid(st_makepoint(-19.29, 75.0), 4326), 50, 'Blitzer Ost', null, '2026-03-01', null, null, 'osm_speed_cameras', '2026-03-28T00:00:00Z')`)
	// the read contract takes no delete; the rows lie far from every
	// fixture area and stay, as the planted departures do
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
