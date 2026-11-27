//go:build integration

package store_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// poiOrderAt is a point no other fixture row is near.
var poiOrderAt = geo.Point{Lat: 73.5, Lon: -18.5}

// poiOrderSeed: three pharmacies around the point, the one spelt exactly as
// asked the farthest, one beginning with the text in the middle and the
// nearest of another name that holds it.
var poiOrderSeed = []string{
	`insert into public.osm_pois (osm_type, osm_id, name, name_norm, category, category_value, geom, source_id, extract_date, place, place_norm) values
	   ('node', 9201, 'Apotheke', 'apotheke', 'amenity', 'pharmacy', st_setsrid(st_makepoint(-18.5, 73.509), 4326)::geography, 'osm', '2026-10-27', 'Ort', 'ort'),
	   ('node', 9202, 'Apotheke am Markt', 'apotheke am markt', 'amenity', 'pharmacy', st_setsrid(st_makepoint(-18.5, 73.505), 4326)::geography, 'osm', '2026-10-27', 'Ort', 'ort'),
	   ('node', 9203, 'Löwen-Apotheke', 'lowen apotheke', 'amenity', 'pharmacy', st_setsrid(st_makepoint(-18.5, 73.5005), 4326)::geography, 'osm', '2026-10-27', 'Ort', 'ort')`,
}

// TestPoisAroundAPositionAreNearestFirst pins the order: around a position
// the nearest first, whatever the name (#309); in a town, the name spelt as
// asked first, then one that begins with it.
func TestPoisAroundAPositionAreNearestFirst(t *testing.T) {
	st, _ := private(t, poiOrderSeed...)
	ids := func(q places.PoiQuery) []string {
		t.Helper()
		got, err := st.Pois(bounded(t), q, []string{"osm"})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range got {
			out = append(out, p.ID)
		}
		return out
	}
	near := ids(places.PoiQuery{Name: "Apotheke", At: &poiOrderAt, RadiusM: 2000, Category: "amenity"})
	if want := []string{"osm:node9203", "osm:node9202", "osm:node9201"}; !equalIDs(near, want) {
		t.Errorf("around the point: %v, want %v", near, want)
	}
	nearAny := ids(places.PoiQuery{Name: "Apotheke", At: &poiOrderAt, RadiusM: 2000})
	if want := []string{"osm:node9203", "osm:node9202", "osm:node9201"}; !equalIDs(nearAny, want) {
		t.Errorf("around the point, no category: %v, want %v", nearAny, want)
	}
	town := ids(places.PoiQuery{Name: "Apotheke", In: "Ort"})
	if want := []string{"osm:node9201", "osm:node9202", "osm:node9203"}; !equalIDs(town, want) {
		t.Errorf("in the town: %v, want %v", town, want)
	}
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
