//go:build integration

package store_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The two place reads the synthetic world asks with no row to find: an
// address whose place the question names only by its first word, and a
// stop whose search name ends with the asked name. Each is read in a copy
// that holds such rows, beside a closed twin the read must drop, so the
// parity replay compares rows.
func TestThePlaceReadsTheWorldFindsNothingFor(t *testing.T) {
	st, _ := private(t,
		`insert into public.osm_addresses (osm_type, osm_id, street, house_number, street_norm, house_number_norm, postcode, place, place_norm, geom, extract_date, closed_at)
		 values ('node', 9980001, 'Seeweg', '3', 'seeweg', '3', '99998', 'Weserhaven Nord', 'weserhaven nord', st_setsrid(st_makepoint(-25, 72), 4326)::geography, '2026-11-01', null),
		        ('node', 9980002, 'Seeweg', '3', 'seeweg', '3', '99998', 'Weserhaven Süd', 'weserhaven sud', st_setsrid(st_makepoint(-25, 72.01), 4326)::geography, '2026-11-01', now())`,
		`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, geom, closed_at) values
		   ('stop', 'Weserhaven Seesternplatz', 'weserhaven seesternplatz', 'delfi_zhv', 'de:99998:1', st_setsrid(st_makepoint(-25, 72), 4326)::geography, null),
		   ('stop', 'Altdorf Seesternplatz', 'altdorf seesternplatz', 'delfi_zhv', 'de:99998:2', st_setsrid(st_makepoint(-25, 72.01), 4326)::geography, now())`)
	reads := st.ResolveReads([]string{"osm", "delfi_zhv"})
	ctx := bounded(t)
	hits, err := reads.AddressRows(ctx, places.AddrByPlacePrefix, []any{"seeweg", "seeweg", "weserhaven", "3", 5})
	if err != nil || len(hits) != 1 || *hits[0].Place != "Weserhaven Nord" {
		t.Errorf("by the place's first word: %+v %v", hits, err)
	}
	stops, err := reads.ShownStops(ctx, "seesternplatz", 5)
	if err != nil || len(stops) != 1 || stops[0].Name != "Weserhaven Seesternplatz" {
		t.Errorf("by the name's end: %+v %v", stops, err)
	}
}
