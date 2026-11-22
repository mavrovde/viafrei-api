//go:build integration

package store_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The motorway places answer the place search as the platform's lists them
// (G5): a junction place of a roads family the key may read by its name, a
// motorway by its number at a point of it; with no roads family neither.
func TestPlacesListTheMotorwayPlacesOfTheRoadsFamilies(t *testing.T) {
	req.Covers(t, "FR-PLACE-001")
	st := seeded(t)
	var fams []string
	owner(t, `select array_agg(distinct source_ref) from contract.place where place_kind <> 'junction'`, &fams)
	var roadFam string
	owner(t, `select source_ref from contract.road where road_ref = 'A100'`, &roadFam)
	var junctionFam string
	owner(t, `select min(source_ref) from contract.junction where road_ref = 'A100'`, &junctionFam)
	roads := []string{fixtures.MotorwayPlaceRef, roadFam, junctionFam}
	core, _ := places.JunctionCore(fixtures.MotorwayPlaceName)

	ps, _, err := st.Places(bounded(t), places.Query{Text: core, Kind: places.KindJunction, Limit: 5}, fams, nil, roads)
	if err != nil || len(ps) != 1 || ps[0].Kind != places.KindJunction || !strings.HasPrefix(ps[0].Name, fixtures.MotorwayPlaceName) || ps[0].Family != fixtures.MotorwayPlaceRef {
		t.Errorf("junction %+v, %v", ps, err)
	}
	if ps, _, err := st.Places(bounded(t), places.Query{Text: core, Limit: 5}, fams, nil, nil); err != nil || len(ps) != 0 {
		t.Errorf("junction without a roads family %+v, %v", ps, err)
	}

	ps, _, err = st.Places(bounded(t), places.Query{Text: "Autobahn 100", Kind: places.KindMotorway, Limit: 5}, fams, nil, roads)
	if err != nil || len(ps) != 1 || ps[0].ID != "A100" || ps[0].Name != "A100" || ps[0].Kind != places.KindMotorway || ps[0].Family != roadFam || ps[0].Location.Lat == 0 {
		t.Errorf("motorway %+v, %v", ps, err)
	}
	if ps, _, err := st.Places(bounded(t), places.Query{Text: "A100", Kind: places.KindMotorway, Limit: 5}, fams, nil, nil); err != nil || len(ps) != 0 {
		t.Errorf("motorway without a roads family %+v, %v", ps, err)
	}
	if ps, _, err := st.Places(bounded(t), places.Query{Text: "A999", Kind: places.KindMotorway, Limit: 5}, fams, nil, roads); err != nil || len(ps) != 0 {
		t.Errorf("an unknown motorway %+v, %v", ps, err)
	}
	// A keyed place carries its own official key, a motorway none.
	var name, key string
	owner(t, `select name, official_key from contract.place where official_key ~ '^[0-9]{8}$' and withdrawn_at is null and location is not null
and display_class <> 'withhold' and audience = 'public' order by prominence desc nulls last, place_id limit 1`, &name, &key)
	ps, _, err = st.Places(bounded(t), places.Query{Text: name, Limit: 50}, fams, nil, roads)
	if err != nil || !slices.ContainsFunc(ps, func(p places.Place) bool { return p.OfficialKey != nil && *p.OfficialKey == key }) {
		t.Errorf("%s keyed %s: %+v, %v", name, key, ps, err)
	}
}
