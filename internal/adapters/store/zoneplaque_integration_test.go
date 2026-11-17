//go:build integration

package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The curated zone list ships with the schema: an entry is found by any of
// its Gemeinde keys, and a seeded entry is read beside the shipped ones.
func TestZoneEntriesReadTheCuratedListByGemeinde(t *testing.T) {
	req.Covers(t, "SEC-030")
	st := seeded(t)
	plant(t, `insert into contract.low_emission_zone_entry (entry_key, name, place_codes, plaque, covered, lifted_on)
values ('testdorf', 'Testdorf', array['99000101', '99000102'], null, false, '2025-01-31')`)
	got, err := st.ZoneEntries(bounded(t), []string{"09162000", "03241001", "99000102", "00000000"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]rules.ListEntry{}
	for _, e := range got {
		by[e.Key] = e
	}
	// Expected from the platform's curated list, not from the code under test.
	if m := by["muenchen"]; len(by) != 3 || m.Name != "München" || m.Plaque == nil || *m.Plaque != "green" || !m.Covered || m.LiftedOn != nil {
		t.Errorf("München: %+v of %d", m, len(by))
	}
	if h := by["hannover"]; h.Plaque != nil || h.LiftedOn == nil || !h.LiftedOn.Equal(time.Date(2024, 2, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Hannover, lifted: %+v", h)
	}
	if d := by["testdorf"]; d.Covered || !slices.Equal(d.PlaceCodes, []string{"99000101", "99000102"}) {
		t.Errorf("seeded: %+v", d)
	}
	if got, err := st.ZoneEntries(bounded(t), []string{"99999999"}); err != nil || len(got) != 0 {
		t.Errorf("no entry: %+v %v", got, err)
	}
}

func TestZoneEntryNamedReadsTheCuratedListByNameOrAlias(t *testing.T) {
	req.Covers(t, "FR-RULES-001")
	st := seeded(t)
	// Expected from the platform's curated list: München's aliases name it.
	for _, name := range []string{"muenchen", "munich", "monacodibaviera"} {
		if e, err := st.ZoneEntryNamed(bounded(t), name); err != nil || e == nil || e.Name != "München" || !e.Covered {
			t.Errorf("%s: %+v %v", name, e, err)
		}
	}
	if e, err := st.ZoneEntryNamed(bounded(t), "matfienland"); err != nil || e != nil {
		t.Errorf("no entry: %+v %v", e, err)
	}
	if zs, err := st.Zones(bounded(t), rules.ZoneQuery{City: "München"}, []string{"lez_alpha"}, time.Now()); err != nil || zs != nil {
		t.Errorf("no zone row is read for a city: %+v %v", zs, err)
	}
}

// The Gemeinde that finds a list entry is the nearest municipality of the
// families asked within the reach, none beyond it.
func TestGemeindeAtReadsTheNearestMunicipalityWithinReach(t *testing.T) {
	req.Covers(t, "SEC-030")
	st := seeded(t)
	plant(t, `insert into contract.place (place_id, place_kind, name, location, official_key, prominence, source_ref, origin_key, recorded_at, changed_at)
values ('99000101', 'municipality', 'Testdorf', st_setsrid(st_makepoint(-30.0, 72.0), 4326), '99000101', 1, 'bkg_gvisys', 'lez:t', '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z')`)
	near := geo.Point{Lat: 72.05, Lon: -30.0} // about 5.6 km north
	g, err := st.GemeindeAt(bounded(t), near, rules.GemeindeReachM, []string{"bkg_gvisys"})
	if err != nil || g == nil || g.Name != "Testdorf" || g.OfficialKey == nil || *g.OfficialKey != "99000101" || g.Family != "bkg_gvisys" {
		t.Errorf("near: %+v %v", g, err)
	}
	if g, err := st.GemeindeAt(bounded(t), near, 5000, []string{"bkg_gvisys"}); err != nil || g != nil {
		t.Errorf("beyond the reach: %+v %v", g, err)
	}
	if g, err := st.GemeindeAt(bounded(t), near, rules.GemeindeReachM, []string{"other"}); err != nil || g != nil {
		t.Errorf("another family: %+v %v", g, err)
	}
}
