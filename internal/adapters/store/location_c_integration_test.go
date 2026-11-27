//go:build integration

package store_test

import (
	"fmt"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// Under a district, the Land is the Land of the district's key, never a
// Regierungsbezirk whose key begins as the Land's does (the gazetteer files
// both as admin; a Bavarian district once read Schwaben for Bayern): a
// Regierungsbezirk planted with its Land's key and one digit more, and a
// district planted on a municipality's point and key.
func TestLocationUnderADistrictReadsItsLandNotItsRegierungsbezirk(t *testing.T) {
	st, cp := private(t,
		plantCopy("contract.place", "admin_code",
			`w.kind = 'admin' and w.closed_at is null and w.admin_code = (select left(m.admin_code, 2) from public.gazetteer_places m
			  where m.id::text in (select place_id from contract.place where place_kind = 'municipality' and region_code is not null)
			  order by m.id limit 1)`,
			`jsonb_build_object('name', 'Regierungsbezirk', 'admin_code', w.admin_code || '7', 'external_id', w.external_id || '-rb')`),
		plantCopy("contract.place", "admin_code",
			`w.id::text in (select place_id from contract.place where place_kind = 'municipality' and region_code is not null)`,
			`jsonb_build_object('kind', 'district', 'external_id', w.external_id || '-district', 'closed_at', null)`))
	var (
		at   geo.Point
		land string
	)
	ownerOf(t, cp, `select st_y(location), st_x(location), left(official_key, 2) from contract.place
		where place_kind = 'district' and region_code is not null limit 1`, &at.Lat, &at.Lon, &land)
	var bezirk int
	ownerOf(t, cp, `select count(*) from contract.place where place_kind = 'state' and official_key = '`+land+`7'`, &bezirk)
	if bezirk == 0 {
		t.Fatal("no Regierungsbezirk planted under the district's Land")
	}
	l, err := st.Location(bounded(t), places.LocationQuery{At: at}, every(t))
	if err != nil || l.State == nil || l.State.OfficialKey == nil || *l.State.OfficialKey != land || l.State.Name == "Regierungsbezirk" {
		t.Fatalf("%+v %v", l.State, err)
	}
}

// At a fixture place of interest of a need's kind, the need reads it, at
// no distance; every asked need answers, a need the platform does not know
// with no places.
func TestNeedPlacesReadsTheNeedsKinds(t *testing.T) {
	var (
		at   geo.Point
		kind string
	)
	if !fixtureRow(t, `select st_y(geom::geometry), st_x(geom::geometry), category || '=' || category_value from public.osm_pois
		where closed_at is null and name is not null order by id limit 1`, &at.Lat, &at.Lon, &kind) {
		t.Fatal("the fixtures hold no place of interest")
	}
	need := ""
	for n, kinds := range places.NeedKinds {
		if slices.Contains(kinds, kind) {
			need = n
		}
	}
	if need == "" {
		t.Fatalf("the fixture's place of interest %s answers no need", kind)
	}
	got, err := seeded(t).NeedPlaces(bounded(t), places.NearbyQuery{At: at, Needs: append(slices.Clone(places.Needs), "no_such_need")}, every(t))
	if err != nil || len(got) != len(places.Needs)+1 || len(got["no_such_need"]) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if ps := got[need]; len(ps) == 0 || ps[0].DistanceM != 0 || ps[0].Kind != kind {
		t.Fatalf("%s: %+v", need, ps)
	}
}

// A published situation of the fixtures reads by its id.
func TestSituationReadsByID(t *testing.T) {
	var id string
	if !fixtureRow(t, `select event_id from contract.traffic_event where withdrawn_at is null and display_class <> 'withhold'
		and audience = 'public' order by event_id limit 1`, &id) {
		t.Fatal("the fixtures hold no published situation")
	}
	got, err := seeded(t).Situation(bounded(t), id, every(t), time.Unix(0, 0))
	if err != nil || got.ID != id {
		t.Fatalf("%+v %v", got, err)
	}
}

// landArea plants a Land area held under name: a box of 0.4 degrees of
// latitude and longitude around (lon, lat), of the family osm.
func landArea(name string, id int, lon, lat float64) string {
	return fmt.Sprintf(`insert into public.osm_boundary_de (land, osm_id, osm_version, part, geom, source_id, extract_date)
	 values ('%s', %d, 1, 1, st_makeenvelope(%f, %f, %f, %f, 4326), 'osm', current_date)`, name, id, lon-0.2, lat-0.2, lon+0.2, lat+0.2)
}

// areaNames are the names Germany's Land areas are held under.
var areaNames = []string{"schleswig-holstein", "hamburg", "niedersachsen", "bremen", "nordrhein-westfalen", "hessen", "rheinland-pfalz",
	"baden-wuerttemberg", "bayern", "saarland", "berlin", "brandenburg", "mecklenburg-vorpommern", "sachsen", "sachsen-anhalt", "thueringen"}

// With the Land areas held, a position's Land and whether it may lie
// abroad are read from them (#299): inside an area it is in Germany, its
// Land the area's; outside every area it may lie abroad, its Land the one
// whose area is nearest (not whose point is), stated at the area's
// distance, none beyond the reach; a point on an area's edge or corner is
// inside. That every name an area may be held under is a Land's is
// TestEveryLandAreaNameIsALands.
func TestTheLandAndAbroadAreReadFromTheLandAreas(t *testing.T) {
	var key string
	if !fixtureRow(t, `select admin_code from public.gazetteer_places where kind = 'admin' and length(admin_code) = 2 and closed_at is null
		order by admin_code limit 1`, &key) {
		t.Fatal("the fixtures hold no Land")
	}
	name, other := "", ""
	for _, n := range areaNames {
		switch k, _ := places.LandOfArea(n); {
		case k == key:
			name = n
		case other == "":
			other = n
		}
	}
	// The near Land's box (74.3 to 74.7 north) and another's farther north.
	st, _ := private(t, landArea(name, 990101, -19.5, 74.5), landArea(other, 990102, -19.5, 75.7))
	fams := every(t)
	at := func(lat float64) places.Location {
		t.Helper()
		l, err := st.Location(bounded(t), places.LocationQuery{At: geo.Point{Lat: lat, Lon: -19.5}}, fams)
		if err != nil {
			t.Fatalf("%v: %v", lat, err)
		}
		return l
	}
	in := at(74.5)
	if in.MayLieAbroad || in.AbroadFamily != "" || in.State == nil || in.State.OfficialKey == nil || *in.State.OfficialKey != key ||
		*in.State.DistanceM != 0 || in.StateArea != "osm" {
		t.Errorf("inside: %+v %+v", in, in.State)
	}
	// On the near box's corner and on its west edge: covered, not contained.
	for _, p := range []geo.Point{{Lat: 74.7, Lon: -19.7}, {Lat: 74.5, Lon: -19.7}} {
		l, err := st.Location(bounded(t), places.LocationQuery{At: p}, fams)
		if err != nil || l.MayLieAbroad || l.AbroadFamily != "" || l.State == nil || l.State.OfficialKey == nil || *l.State.OfficialKey != key ||
			l.State.DistanceM == nil || *l.State.DistanceM != 0 {
			t.Errorf("on the edge at %+v: %+v %+v %v", p, l, l.State, err)
		}
	}
	// 0.1 degree north of the near box's edge, about 11 km; the other's is 0.7.
	out := at(74.8)
	if !out.MayLieAbroad || out.AbroadFamily != "osm" || out.State == nil || *out.State.OfficialKey != key ||
		*out.State.DistanceM < 11000 || *out.State.DistanceM > 11300 || out.StateArea != "osm" {
		t.Errorf("outside: %+v %+v", out, out.State)
	}
	// Beyond the reach of either area: abroad, and no Land.
	if far := at(78.5); !far.MayLieAbroad || far.State != nil || far.StateArea != "" {
		t.Errorf("far: %+v", far)
	}
}

// Every name the Land areas' table admits (its check, read from the
// schema, not the rows) is a Land's, and each Land's name is admitted.
func TestEveryLandAreaNameIsALands(t *testing.T) {
	var check string
	if !fixtureRow(t, `select pg_get_constraintdef(c.oid) from pg_constraint c
		where c.conrelid = 'public.osm_boundary_de'::regclass and c.conname = 'osm_boundary_de_land_known'`, &check) {
		t.Fatal("the Land areas' table has no check of its names")
	}
	admitted := regexp.MustCompile(`'([a-z-]+)'::text`).FindAllStringSubmatch(check, -1)
	keys := map[string]bool{}
	for _, m := range admitted {
		k, ok := places.LandOfArea(m[1])
		if !ok {
			t.Errorf("the check admits %q, no Land's name", m[1])
		}
		keys[k] = true
	}
	if len(admitted) != len(areaNames) || len(keys) != 16 {
		t.Errorf("the check admits %d names of %d Länder: %s", len(admitted), len(keys), check)
	}
}
