//go:build integration

package store_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// placePartsSeed: a town Quellstadt an der Aue (a Gemeinde of 07998), the
// kreisfreie Stadt Großquell (07999000, with its district row and the
// district's region), and two city districts named Quellstadt, each ranked
// below every municipality (prominence -1, whatever its population): one
// part of Großquell, one part of an unrecorded municipality.
var placePartsSeed = []string{
	`insert into public.ags_regions (ags, level, name, name_norm, parent_ags, source_id)
	 values ('07999', 'kreis', 'Großquell', 'grossquell', '07', 'destatis_gv') on conflict do nothing`,
	`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, admin_code, population, rank, geom) values
	   ('city', 'Quellstadt an der Aue', 'quellstadt an der aue', 'bkg_gvisys', 'q1', '07998000', 5000, 5000, st_setsrid(st_makepoint(8.1, 49.3), 4326)),
	   ('city', 'Großquell', 'grossquell', 'bkg_gvisys', 'q2', '07999000', 90000, 90000, st_setsrid(st_makepoint(8.5, 49.5), 4326)),
	   ('district', 'Großquell', 'grossquell', 'bkg_gvisys', 'q3', '07999', 90000, 90000, st_setsrid(st_makepoint(8.5, 49.5), 4326))`,
	`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, population, rank, part_of, part_type, geom) values
	   ('city', 'Quellstadt', 'quellstadt', 'geonames', 'g1', 12000, 12000, '07999000', 'stadtteil', st_setsrid(st_makepoint(8.51, 49.51), 4326)),
	   ('city', 'Quellstadt', 'quellstadt', 'geonames', 'g2', 0, 0, '07997000', 'ortsteil', st_setsrid(st_makepoint(8.9, 49.9), 4326))`,
}

// TestPlacesNameCityDistrictsAsPartsOfTheirMunicipality: a city district is
// a locality with the municipality it is part of, that municipality's Kreis
// and its population; a municipality whose name starts with the text comes
// before a district spelt as the text (#307).
func TestPlacesNameCityDistrictsAsPartsOfTheirMunicipality(t *testing.T) {
	st, _ := private(t, placePartsSeed...)
	ps, _, err := st.Places(bounded(t), places.Query{Text: "Quellstadt", Limit: 5}, []string{"bkg_gvisys", "geonames"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 {
		t.Fatalf("%d places: %+v", len(ps), ps)
	}
	if ps[0].Name != "Quellstadt an der Aue" || ps[0].Kind != "municipality" || ps[0].Population == nil || *ps[0].Population != 5000 || ps[0].PartOf != nil {
		t.Errorf("first: %+v", ps[0])
	}
	// The two districts tie in rank; each is found by the municipality it is
	// part of.
	var d, o places.Place
	for _, p := range ps[1:] {
		switch {
		case p.PartOfKey != nil && *p.PartOfKey == "07999000":
			d = p
		case p.PartOfKey != nil && *p.PartOfKey == "07997000":
			o = p
		}
	}
	if d.RegionCode == nil || *d.RegionCode != "07999" {
		t.Errorf("the district of Großquell's districtCode: %v, want 07999", d.RegionCode)
	}
	if d.Name != "Quellstadt" || d.Kind != "locality" || d.PartOf == nil || *d.PartOf != "Großquell" || d.PartOfKey == nil || *d.PartOfKey != "07999000" ||
		d.Kreis == nil || *d.Kreis != "kreisfreie Stadt Großquell" || d.Population == nil || *d.Population != 12000 || d.AGS() != nil {
		t.Errorf("the district of Großquell: %+v", d)
	}
	// Without the family that names it, the municipality is not named.
	if ps, _, err := st.Places(bounded(t), places.Query{Text: "Quellstadt", Kind: "locality", Limit: 5}, []string{"geonames"}, nil, nil); err != nil || len(ps) != 2 || ps[0].PartOf != nil {
		t.Errorf("districts without the naming family: %+v, %v", ps, err)
	}
	if o.Kind != "locality" || o.PartOf != nil || o.Population != nil {
		t.Errorf("the district of an unrecorded municipality: %+v", o)
	}
}
