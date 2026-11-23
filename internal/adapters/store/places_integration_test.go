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
	// A place with an English alias other than its name states it, as the
	// platform's nameEn; the others none.
	var id, en string
	owner(t, `select p.place_id, p.name, a.label from contract.place p join contract.place_alias a on a.place_id = p.place_id
where a.lang = 'en' and a.label <> p.name and p.withdrawn_at is null and p.location is not null
  and p.display_class <> 'withhold' and p.audience = 'public' and a.display_class <> 'withhold' and a.audience = 'public'
order by p.place_id limit 1`, &id, &name, &en)
	ps, _, err = st.Places(bounded(t), places.Query{Text: name, Limit: 50}, fams, nil, roads)
	i := slices.IndexFunc(ps, func(p places.Place) bool { return p.ID == id })
	if err != nil || i < 0 || ps[i].NameEn == nil || *ps[i].NameEn != en {
		t.Errorf("%s (en %s): %+v, %v", name, en, ps, err)
	}
	for _, p := range ps {
		if p.NameEn != nil && *p.NameEn == p.Name {
			t.Errorf("%s states its own name as English", p.ID)
		}
	}
	// A Gemeinde in a Kreis names it from the district rows; a key that may
	// not read them names none.
	// The fixture's districts are regions, so the test plants the open BKG
	// district row of a fixture Gemeinde, and a closed one of the same key.
	const kreis = "Testkreis Nord"
	owner(t, `select p.place_id, p.name from contract.place p
where p.place_kind = 'municipality' and p.official_key ~ '^[0-9]{5}[0-9]{2}[1-9]$' and p.withdrawn_at is null and p.location is not null
  and p.display_class <> 'withhold' and p.audience = 'public'
order by p.place_id limit 1`, &id, &name)
	change(t, `insert into vf_seed.place (place_id, place_kind, name, location, region_code, official_key, prominence, source_ref, origin_key,
    recorded_at, changed_at, withdrawn_at, source_refs, display_class, audience)
  select k.id, 'district', k.name, p.location, p.region_code, left(p.official_key, 5), 0, $2, 'o-' || k.id,
    p.recorded_at, p.changed_at, k.gone, array[$2::text], 'open', 'public'
  from contract.place p, (values ('990000001', $3::text, null::timestamptz), ('990000002', 'Altkreis', now())) k(id, name, gone)
  where p.place_id = $1`, id, places.KreisFamily, kreis)
	t.Cleanup(func() { change(t, `delete from contract.place where place_id in ('990000001', '990000002')`) })
	ps, _, err = st.Places(bounded(t), places.Query{Text: name, Limit: 50}, fams, nil, roads)
	i = slices.IndexFunc(ps, func(p places.Place) bool { return p.ID == id })
	want := places.KreisOf("municipality", ps[max(i, 0)].OfficialKey, map[string]string{(*ps[max(i, 0)].OfficialKey)[:5]: kreis})
	if err != nil || i < 0 || ps[i].Kreis == nil || *ps[i].Kreis != *want || ps[i].KreisFamily != places.KreisFamily {
		t.Errorf("%s in %s: %+v, %v", name, kreis, ps, err)
	}
	others := slices.DeleteFunc(slices.Clone(fams), func(f string) bool { return f == places.KreisFamily })
	ps, _, err = st.Places(bounded(t), places.Query{Text: name, Limit: 50}, others, nil, roads)
	if i := slices.IndexFunc(ps, func(p places.Place) bool { return p.ID == id }); err != nil || i >= 0 && (ps[i].Kreis != nil || ps[i].KreisFamily != "") {
		t.Errorf("without %s: %+v, %v", places.KreisFamily, ps, err)
	}
}
