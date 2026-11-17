//go:build integration

package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

func TestFuelStationIsReadByIDWithItsHeldPrices(t *testing.T) {
	st := seeded(t)
	var (
		id, fam string
		newest  time.Time
		grades  int
	)
	owner(t, `select s.station_id, s.source_ref, max(o.observed_at), count(distinct o.product_code)::int
from contract.fuel_station s join contract.fuel_price_observation o using (station_id)
where s.withdrawn_at is null and o.price is not null and s.display_class <> 'withhold' and o.display_class <> 'withhold'
group by 1, 2 order by 4 desc, 1 limit 1`, &id, &fam, &newest, &grades)
	now := newest.Add(time.Minute)
	ctx := access.WithAudience(bounded(t), access.AudienceOwn)
	got, err := st.FuelStation(ctx, id, []string{fam}, now, 72*time.Hour)
	if err != nil || got.ID != id || got.Family != fam {
		t.Fatalf("station %s: %+v %v", id, got, err)
	}
	if len(got.Prices) == 0 || len(got.Prices) > grades {
		t.Errorf("prices: %d of %d grades", len(got.Prices), grades)
	}
	for _, p := range got.Prices {
		if p.ObservedAt.After(now) || p.ObservedAt.Before(now.Add(-72*time.Hour)) {
			t.Errorf("a price outside the held window: %+v", p)
		}
	}
	// A window that ends before every report holds no price; the station is
	// still answered.
	early, err := st.FuelStation(ctx, id, []string{fam}, newest.Add(-1000*time.Hour), time.Hour)
	if err != nil || len(early.Prices) != 0 {
		t.Errorf("before every report: %+v %v", early.Prices, err)
	}
	for _, c := range []struct {
		name, id string
		fams     []string
	}{{"unknown id", "no-such-station", []string{fam}}, {"another family", id, []string{"park_alpha"}}} {
		if _, err := st.FuelStation(ctx, c.id, c.fams, now, time.Hour); !errors.Is(err, energy.ErrNotFound) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestChargingSiteIsReadByID(t *testing.T) {
	st := seeded(t)
	var (
		id, fam string
		points  int
	)
	owner(t, `select c.site_id, c.source_ref, count(p.point_id)::int
from contract.charging_site c join contract.charge_point p using (site_id)
where c.withdrawn_at is null and c.display_class <> 'withhold' and c.audience = 'public'
  and p.display_class <> 'withhold' and p.audience = 'public'
group by 1, 2 order by 3 desc, 1 limit 1`, &id, &fam, &points)
	sites, more, _, err := st.ChargingNear(bounded(t), energy.Query{SiteID: id, Access: energy.FilterAll, Limit: 1}, []string{fam}, time.Now())
	if err != nil || more || len(sites) != 1 || sites[0].ID != id || len(sites[0].Points) != points {
		t.Fatalf("site %s with %d points: %+v more=%v %v", id, points, sites, more, err)
	}
	none, _, _, err := st.ChargingNear(bounded(t), energy.Query{SiteID: "no-such-site", Access: energy.FilterAll, Limit: 1}, []string{fam}, time.Now())
	if err != nil || len(none) != 0 {
		t.Errorf("unknown id: %+v %v", none, err)
	}
}
