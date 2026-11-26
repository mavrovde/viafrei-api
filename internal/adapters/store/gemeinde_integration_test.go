//go:build integration

package store_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// A position lies in the Gemeinde the nearest address names as its town,
// else in the nearest Gemeinde, which names its Kreis and Regierungsbezirk
// by key; another family's is never read.
func TestAPositionLiesInAGemeindeThatNamesItsKreisAndBezirk(t *testing.T) {
	at := geo.Point{Lat: 51.0004, Lon: 10.0004}
	st, _ := private(t,
		`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, admin_code, geom, closed_at)
		 values ('city', 'Lindental', 'lindental', 'bkg_gvisys', 'p-09999001', '09999001', st_setsrid(st_makepoint(10.0004, 51.0004), 4326)::geography, null),
		        ('city', 'Alttal', 'alttal', 'bkg_gvisys', 'p-09999002', '09999002', st_setsrid(st_makepoint(10.0004, 51.0004), 4326)::geography, now()),
		        ('district', 'Kreis Lindental', 'kreis lindental', 'bkg_gvisys', 'p-09999', '09999', vf_test.point(null), null),
		        ('admin', 'Bezirk Lindental', 'bezirk lindental', 'bkg_gvisys', 'p-099', '099', vf_test.point(null), null),
		        ('city', 'Obertal', 'obertal', 'bkg_gvisys', 'p-09999003', '09999003', st_setsrid(st_makepoint(10.0104, 51.0004), 4326)::geography, null)`,
		`insert into public.osm_addresses (osm_type, osm_id, street, house_number, postcode, place, street_norm, house_number_norm, place_norm, geom, source_id, extract_date, first_seen_at, last_seen_at, land)
		 values ('node', 999999001, 'Talweg', '1', '99999', 'Obertal, Gemeinde', 'talweg', '1', 'obertal', st_setsrid(st_makepoint(10.0004, 51.0005), 4326)::geography, 'osm', current_date, now(), now(), 'Bayern')`)
	l, err := st.Location(bounded(t), places.LocationQuery{At: at}, every(t))
	g := l.Gemeinde
	if err != nil || g == nil || g.Name != "Obertal" || g.Code != "09999003" || !g.Inside || g.Family != "bkg_gvisys" ||
		g.Kreis == nil || *g.Kreis != "Kreis Lindental" || g.Bezirk == nil || *g.Bezirk != "Bezirk Lindental" {
		t.Fatalf("gemeinde %+v (address %+v) %v", g, l.Address, err)
	}
	// The address's town named no Gemeinde within reach: the nearest, not
	// inside.
	far := geo.Point{Lat: 51.0004, Lon: 10.0006}
	st2, _ := private(t,
		`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, admin_code, geom)
		 values ('city', 'Lindental', 'lindental', 'bkg_gvisys', 'p-09999001', '09999001', st_setsrid(st_makepoint(10.0006, 51.0004), 4326)::geography)`,
		`insert into public.osm_addresses (osm_type, osm_id, street, house_number, postcode, place, street_norm, house_number_norm, place_norm, geom, source_id, extract_date, first_seen_at, last_seen_at, land)
		 values ('node', 999999002, 'Talweg', '2', '99999', 'Nirgendwo', 'talweg', '2', 'nirgendwo', st_setsrid(st_makepoint(10.0006, 51.0005), 4326)::geography, 'osm', current_date, now(), now(), 'Bayern')`)
	if l, err := st2.Location(bounded(t), places.LocationQuery{At: far}, every(t)); err != nil || l.Gemeinde == nil || l.Gemeinde.Name != "Lindental" || l.Gemeinde.Inside ||
		l.Gemeinde.Kreis != nil {
		t.Errorf("nearest: %+v %v", l.Gemeinde, err)
	}
	if l, err := st.Location(bounded(t), places.LocationQuery{At: at}, []string{"osm"}); err != nil || l.Gemeinde != nil {
		t.Errorf("another family's: %+v %v", l.Gemeinde, err)
	}
}
