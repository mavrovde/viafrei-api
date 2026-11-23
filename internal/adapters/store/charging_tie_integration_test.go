//go:build integration

package store_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// plantSite is one charging site of family ref, origin key, at lat, lon:
// a row the fixture's own charging sites are planted the same way as.
func plantSite(ref, key string, lat, lon string) string {
	return `insert into vf_seed.charging_site (site_id, name, operator_name, location, source_ref, origin_key, recorded_at, changed_at, withdrawn_at)
values ('` + ref + `:` + key + `', 'Testladen ` + key + `', 'Test Strom', st_setsrid(st_makepoint(` + lon + `, ` + lat + `), 4326), '` + ref + `', '` + key + `', now(), now(), null)`
}

// The sites kept at the limit are those the site's nearest walk keeps
// (site 2.6.39): nearest by the geography distance operator, on the sphere,
// and at one distance by family, then origin key. The plants lie west of
// Germany, where the fixture has no site.
//
// Near: north of (50, 1), 1001.0 m on the sphere and 1001.3 m on the
// spheroid; east, 1000.0 m on the sphere and 1003.1 m on the spheroid. The
// walk keeps the east one; by the rounded spheroid distance it was the north
// one. At one coordinate: the families afir_alpha and afir_alpha_stale, whose
// joined ids sort the other way round from (family, key) under the
// database's collation when it skips punctuation, as en_US does.
func TestChargingNearKeepsTheSitesTheNearestWalkKeeps(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004")
	now := time.Now()
	st, _ := private(t,
		plantSite("afir_alpha", "tie-north", "50.0090022", "1"),
		plantSite("afir_alpha", "tie-east", "50", "1.0139909"),
		plantSite("afir_alpha_stale", "a", "50.5", "1"),
		plantSite("afir_alpha", "z", "50.5", "1"))
	fams := families(t, st, "energy")
	q := energy.Query{At: geo.Point{Lat: 50, Lon: 1}, RadiusM: 1500, Access: energy.FilterAll, Limit: 1}
	got, more, _, err := st.ChargingNear(bounded(t), q, fams, now)
	if err != nil || len(got) != 1 || got[0].ID != "afir_alpha:tie-east" || got[0].DistanceM != 1003 || !more {
		t.Fatalf("limit 1 at (50, 1): %+v, more %v, %v; want afir_alpha:tie-east at 1003 m and more", got, more, err)
	}
	q.Limit = 2
	got, more, _, err = st.ChargingNear(bounded(t), q, fams, now)
	if err != nil || len(got) != 2 || got[0].ID != "afir_alpha:tie-north" || got[1].ID != "afir_alpha:tie-east" || more {
		t.Fatalf("limit 2 at (50, 1): %+v, more %v, %v; want both, listed by the distance reported", got, more, err)
	}
	q = energy.Query{At: geo.Point{Lat: 50.5, Lon: 1}, RadiusM: 10, Access: energy.FilterAll, Limit: 1}
	got, more, _, err = st.ChargingNear(bounded(t), q, fams, now)
	if err != nil || len(got) != 1 || got[0].ID != "afir_alpha:z" || !more {
		t.Fatalf("limit 1 at one coordinate: %+v, more %v, %v; want afir_alpha:z and more", got, more, err)
	}
}
