//go:build integration

package store_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The junctions near a position are OpenStreetMap's nodes, rest areas left
// out and same-named ones folded, each on the road the LCL places it on;
// with no node, the gazetteer's junction points; with neither, none.
func TestJunctionsNearArePlacedOnTheirMotorway(t *testing.T) {
	pt := func(lon, lat string) string {
		return "st_setsrid(st_makepoint(" + lon + ", " + lat + "), 4326)::geography"
	}
	st, _ := private(t,
		`delete from public.osm_pois where category_value = 'motorway_junction'`,
		`delete from public.gazetteer_places where kind = 'junction'`,
		`insert into public.osm_pois (osm_type, osm_id, name, name_norm, category, category_value, geom, source_id, extract_date, first_seen_at, last_seen_at)
		 values ('node', 990001, 'Kreuz Nürnberg-Hafen', 'x', 'highway', 'motorway_junction', `+pt("11.0400", "49.4000")+`, 'osm', current_date, now(), now()),
		        ('node', 990002, 'Kreuz Nürnberg-Hafen', 'x', 'highway', 'motorway_junction', `+pt("11.0420", "49.4010")+`, 'osm', current_date, now(), now()),
		        ('node', 990003, 'Rastplatz Hammerbach', 'x', 'highway', 'motorway_junction', `+pt("11.0410", "49.4000")+`, 'osm', current_date, now(), now()),
		        ('node', 990004, 'Joachimsplatz', 'x', 'highway', 'motorway_junction', `+pt("11.0800", "49.4000")+`, 'osm', current_date, now(), now())`,
		`insert into public.tmc_locations (cid, tabcd, lcd, lcl_version, class, tcd, stcd, road_number, name, geom)
		 values (58, 1, 990001, '22.0', 'P', 1, 3, 'A73', 'Nürnberg-Hafen', vf_test.point(st_setsrid(st_makepoint(11.0405, 49.4002), 4326))),
		        (58, 1, 990002, '22.0', 'P', 1, 3, 'A6', 'Nürnberg-Süd', vf_test.point(st_setsrid(st_makepoint(11.0401, 49.4001), 4326)))`,
		`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, road, geom)
		 values ('junction', 'Feucht (A9)', 'feucht a9', 'autobahn', 'j-990001', 'A9', `+pt("12.0000", "49.4000")+`)`)
	fams := every(t)
	js, err := st.Junctions(bounded(t), places.NearbyQuery{At: geo.Point{Lat: 49.4, Lon: 11.04}, RadiusM: 5000, Limit: 3}, fams)
	if err != nil || js.From != places.JunctionsFromOSM || len(js.Rows) != 2 || js.Rows[0].Name != "Kreuz Nürnberg-Hafen" ||
		js.Rows[0].Road == nil || *js.Rows[0].Road != "A73" || js.Rows[0].RoadBy != "bast_lcl" || js.Rows[0].Family != "osm" ||
		js.Rows[1].Name != "Joachimsplatz" || js.Rows[1].Road != nil || js.Rows[1].RoadBy != "" {
		t.Fatalf("osm: %+v %v", js, err)
	}
	js, err = st.Junctions(bounded(t), places.NearbyQuery{At: geo.Point{Lat: 49.4, Lon: 12.0}, RadiusM: 5000, Limit: 3}, fams)
	if err != nil || js.From != places.JunctionsFromGazetteer || len(js.Rows) != 1 || js.Rows[0].Road == nil || *js.Rows[0].Road != "A9" || js.Rows[0].Family != "autobahn" {
		t.Errorf("gazetteer: %+v %v", js, err)
	}
	js, err = st.Junctions(bounded(t), places.NearbyQuery{At: geo.Point{Lat: 54.9, Lon: 5.0}, RadiusM: 5000, Limit: 3}, fams)
	if err != nil || js.From != places.JunctionsFromOSM || len(js.Rows) != 0 {
		t.Errorf("none: %+v %v", js, err)
	}
}
