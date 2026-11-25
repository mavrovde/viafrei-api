//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// aroundAt is a point no fixture row is near.
var aroundAt = geo.Point{Lat: 74.5, Lon: -19.5}

// aroundSeed: a café and a toilet near the site, a second café farther
// off, three car parks (one private, one for bicycles by its name), a
// street's bays (two of them inside 300 m) and a coach place of a feed
// checked now, and a bay rule of a feed not checked for a month.
var aroundSeed = []string{
	`insert into public.osm_pois (osm_type, osm_id, name, name_norm, category, category_value, geom, source_id, extract_date, access) values
	   ('node', 9101, 'Eck', 'eck', 'amenity', 'cafe', st_setsrid(st_makepoint(-19.5, 74.5001), 4326)::geography, 'osm', '2026-10-27', null),
	   ('node', 9102, 'Fern', 'fern', 'amenity', 'cafe', st_setsrid(st_makepoint(-19.5, 74.5003), 4326)::geography, 'osm', '2026-10-27', null),
	   ('node', 9103, 'WC', 'wc', 'amenity', 'toilets', st_setsrid(st_makepoint(-19.5, 74.5002), 4326)::geography, 'osm', '2026-10-27', null),
	   ('way', 9104, 'P+R Nord', 'p+r nord', 'amenity', 'parking', st_setsrid(st_makepoint(-19.5, 74.501), 4326)::geography, 'osm', '2026-10-27', 'yes'),
	   ('way', 9105, 'Hof', 'hof', 'amenity', 'parking', st_setsrid(st_makepoint(-19.5, 74.5005), 4326)::geography, 'osm', '2026-10-26', 'private; customers'),
	   ('way', 9106, 'Fahrradparken', 'fahrradparken', 'amenity', 'parking', st_setsrid(st_makepoint(-19.5, 74.5004), 4326)::geography, 'osm', '2026-10-26', null),
	   ('way', 9107, 'Markt', 'markt', 'amenity', 'parking', st_setsrid(st_makepoint(-19.5, 74.502), 4326)::geography, 'osm', '2026-10-26', '')`,
	`insert into public.feed_catalog (feed_key, provider, display_name, licence_id, cadence_s) select k, 'p', k, (select licence_id from public.feed_catalog limit 1), 3600
	   from unnest(array['ctx_now', 'ctx_old']) k on conflict (feed_key) do update set cadence_s = 3600, closed_at = null`,
	`insert into control.feed_state (ledger, key, updated_at, last_success_at) values ('feed', 'ctx_now', now(), now()), ('feed', 'ctx_old', now(), now() - interval '30 days')
	   on conflict (ledger, key) do update set last_success_at = excluded.last_success_at`,
	`insert into public.road_context_features (source_feed, source_publisher, feature_id, kind, variant, name, props, geom, anchor, content_hash, first_seen_at, last_seen_at) values
	   ('ctx_now', 'p', 'b1', 'street_parking', 'ticket', 'Hafenstraße', '{"fee": "yes"}',
	    st_setsrid(st_geomfromtext('MULTIPOINT((-19.5 74.5001), (-19.5 74.5002), (-19.5 74.51))'), 4326), st_setsrid(st_makepoint(-19.5, 74.5001), 4326), 'h1', now(), now()),
	   ('ctx_now', 'p', 'c1', 'coach_parking', 'parking', 'Busplatz', '{"valid_from": "2020-01-01"}',
	    st_setsrid(st_makepoint(-19.5, 74.505), 4326), st_setsrid(st_makepoint(-19.5, 74.505), 4326), 'h2', now(), now()),
	   ('ctx_now', 'p', 'c2', 'coach_parking', 'parking', 'Später', '{"valid_from": "2999-01-01"}',
	    st_setsrid(st_makepoint(-19.5, 74.505), 4326), st_setsrid(st_makepoint(-19.5, 74.505), 4326), 'h3', now(), now()),
	   ('ctx_old', 'p', 'b2', 'street_parking', 'disc', 'Alt', '{}',
	    st_setsrid(st_makepoint(-19.5, 74.5001), 4326), st_setsrid(st_makepoint(-19.5, 74.5001), 4326), 'h4', now(), now())`,
}

func TestParkingAroundReadsTheToolsStatements(t *testing.T) {
	st, pdb := private(t, aroundSeed...)
	q := parking.AroundQuery{At: aroundAt, Now: time.Now(), Sites: []geo.Point{aroundAt}, Radii: []float64{500}, OSMFamilies: []string{parking.OSMFamily},
		CarParks: true, CarParkRadiusM: 1000, CarParkFetch: 8, Context: true, ContextFamilies: []string{"ctx_now", "ctx_old"}}
	r, err := st.ParkingAround(bounded(t), q)
	if err != nil {
		t.Fatal(err)
	}
	// One per amenity, the nearest.
	if len(r.Nearby) != 2 || r.Nearby[0].Amenity != "cafe" || *r.Nearby[0].Name != "Eck" || r.Nearby[0].Site != 1 || r.Nearby[1].Amenity != "toilet" ||
		r.Nearby[0].ExtractDate == nil || *r.Nearby[0].ExtractDate != "2026-10-27" {
		t.Errorf("nearby %+v", r.Nearby)
	}
	// The private and the bicycle car parks are left out.
	if len(r.CarParks) != 2 || r.CarParks[0].ID != "way/9104" || r.CarParks[1].ID != "way/9107" || r.CarParks[0].DistanceM < 100 {
		t.Errorf("car parks %+v", r.CarParks)
	}
	// The bays count the parts inside 300 m; the coach place whose period
	// has not begun and the feed not checked are left out.
	if len(r.Context) != 2 || r.Context[0].Kind != "coach_parking" || r.Context[0].ID != "c1" || r.Context[1].ID != "b1" || r.Context[1].PartsIn != 2 ||
		r.Context[1].Total != 2 || r.Context[1].Variants["ticket"] != 2 || r.Context[1].Facts["fee"] != "yes" || r.Context[1].Flagged != nil {
		t.Errorf("context %+v", r.Context)
	}
	// A park and ride pattern keeps only the P+R.
	p := parking.OSMParkAndRidePattern
	q.Pattern, q.Sites, q.Context = &p, nil, false
	if r, err = st.ParkingAround(bounded(t), q); err != nil || len(r.CarParks) != 1 || r.Nearby != nil || r.Context != nil {
		t.Errorf("park and ride: %+v %v", r, err)
	}
	// A key without OpenStreetMap or the context families reads nothing.
	q.OSMFamilies, q.Context, q.ContextFamilies = nil, true, nil
	if r, err = st.ParkingAround(bounded(t), q); err != nil || r.CarParks != nil || r.Context != nil {
		t.Errorf("nothing permitted: %+v %v", r, err)
	}
	q = parking.AroundQuery{At: aroundAt, Now: time.Now(), Sites: []geo.Point{aroundAt}, Radii: []float64{500}, OSMFamilies: []string{parking.OSMFamily},
		CarParks: true, CarParkRadiusM: 1000, CarParkFetch: 8, Context: true, ContextFamilies: []string{"ctx_now"}}
	fst, f := faultyOn(t, pdb.ReaderURL)
	failEach(t, fst, f, "ParkingAround", func(ctx context.Context, s *store.Store) error { _, err := s.ParkingAround(ctx, q); return err })
}
