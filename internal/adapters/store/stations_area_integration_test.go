//go:build integration

package store_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// area keeps a search to the stations whose district code starts with the
// key: a Kreis its own, a Land every Kreis of it, a station with no district
// none.
func TestStationsKeptToAnArea(t *testing.T) {
	st := seeded(t)
	fams := families(t, st, "transit")
	var kreis string
	owner(t, `select region_code from contract.stop_place where withdrawn_at is null and location is not null and region_code is not null
order by region_code limit 1`, &kreis)
	count := func(prefix string) int {
		t.Helper()
		var n int
		owner(t, fmt.Sprintf(`select count(*) from contract.stop_place where withdrawn_at is null and location is not null
and source_ref in (%s) and region_code like '%s%%'`, quoted(fams), prefix), &n)
		return n
	}
	for _, area := range []string{kreis, kreis[:2]} {
		ss, _, err := st.Stations(bounded(t), transit.Query{Area: area, Limit: 1000}, fams)
		if err != nil {
			t.Fatal(err)
		}
		// Expected from the rows, not from the code under test.
		if want := count(area); len(ss) != want || want == 0 {
			t.Errorf("area %s: %d stations, want %d", area, len(ss), want)
		}
		for _, s := range ss {
			if s.RegionCode == nil || !strings.HasPrefix(*s.RegionCode, area) {
				t.Errorf("area %s: %s lies in %v", area, s.ID, s.RegionCode)
			}
		}
	}
	all, _, err := st.Stations(bounded(t), transit.Query{Limit: 1000}, fams)
	if err != nil || len(all) <= count(kreis) {
		t.Errorf("no area: %d stations %v", len(all), err)
	}
}
