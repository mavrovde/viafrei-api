//go:build integration

package store_test

import (
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
