//go:build integration

package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/national"
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

func TestNationalReadsEveryDomainsPart(t *testing.T) {
	st := seeded(t)
	ctx := access.WithAudience(bounded(t), access.AudienceOwn)
	var fams []string
	owner(t, `select array_agg(distinct source_ref order by source_ref) from contract.source`, &fams)
	var newest time.Time
	owner(t, `select max(observed_at) from contract.fuel_price_observation`, &newest)
	at := newest.Add(time.Minute)
	seen := map[string]bool{}
	for _, d := range national.Domains {
		rows, err := st.National(ctx, d, fams, at, 72*time.Hour)
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		for _, r := range rows {
			seen[r.Section] = true
			if r.N < 0 || r.Family == "" {
				t.Errorf("%s: %+v", d, r)
			}
			if r.Section == national.SectionFuel && (r.Low == nil || r.Mid == nil || *r.Low > *r.Mid) {
				t.Errorf("fuel: %+v", r)
			}
		}
	}
	for _, s := range []string{national.SectionFuel, national.SectionCharging, national.SectionParking} {
		if !seen[s] {
			t.Errorf("the fixture fills %s, the read gave none (%v)", s, seen)
		}
	}
	if rows, err := st.National(ctx, "places", fams, at, time.Hour); err != nil || rows != nil {
		t.Errorf("a domain with no part: %v %v", rows, err)
	}
	if rows, err := st.National(ctx, "roads", []string{"no-such-family"}, at, time.Hour); err != nil || len(rows) != 0 {
		t.Errorf("no family: %v %v", rows, err)
	}
}

func TestAffectedStationsAreRankedByWhatAffectsThem(t *testing.T) {
	st := seeded(t)
	var fams []string
	owner(t, `select array_agg(distinct source_ref order by source_ref) from contract.source`, &fams)
	// The departures of the stations the fixture's lifts name, read station by
	// station (the view is built for that): the hour from the first one.
	var at time.Time
	owner(t, `select coalesce(min(d.planned_departure_at), now()) - interval '1 minute'
from (select distinct stop_place_id from contract.station_facility where facility_status = 'inactive') f
cross join lateral (select planned_departure_at from contract.departure d where d.stop_place_id = f.stop_place_id order by 1 limit 1) d`, &at)
	ctx := access.WithAudience(bounded(t), access.AudienceOwn)
	got, err := st.AffectedStations(ctx, fams, at, 50)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range got {
		if a.Cancelled+a.Late+a.Alerts+a.LiftsOut == 0 || len(a.Families) == 0 || a.Families[0] != a.Station.Family {
			t.Errorf("listed with nothing, or uncredited: %+v", a)
		}
		if i > 0 && got[i-1].Cancelled < a.Cancelled {
			t.Errorf("not ranked by cancellations: %+v before %+v", got[i-1], a)
		}
	}
	if len(got) == 0 {
		t.Error("the fixture holds out-of-service lifts and cancellations; none was listed")
	}
	none, err := st.AffectedStations(ctx, []string{"no-such-family"}, at, 5)
	if err != nil || len(none) != 0 {
		t.Errorf("no family: %v %v", none, err)
	}
}

func TestPlacesCoverageCountsKindsLandsAndAddresses(t *testing.T) {
	st := seeded(t)
	var fams []string
	owner(t, `select array_agg(distinct source_ref order by source_ref) from contract.source`, &fams)
	var places, lands int
	owner(t, `select (select count(*) from contract.place where withdrawn_at is null and display_class <> 'withhold' and audience = 'public')::int,
       (select count(*) from contract.place where place_kind = 'state' and official_key is not null and withdrawn_at is null and display_class <> 'withhold' and audience = 'public')::int`, &places, &lands)
	c, err := st.PlacesCoverage(bounded(t), fams)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, k := range c.Kinds {
		sum += k.Rows
		if k.EnglishNames > k.Rows || k.Family == "" {
			t.Errorf("kind %+v", k)
		}
	}
	if sum != places || len(c.Lands) != lands {
		t.Errorf("places %d of %d, lands %d of %d", sum, places, len(c.Lands), lands)
	}
	if len(c.Addresses) == 0 {
		t.Error("the fixture holds addresses; none was covered")
	}
	for i, a := range c.Addresses {
		if a.Family == "" || (i > 0 && (c.Addresses[i-1].Land == nil || (a.Land != nil && *c.Addresses[i-1].Land >= *a.Land))) {
			t.Errorf("addresses not one per Land, by name, the unplaced last: %+v", c.Addresses)
		}
	}
	none, err := st.PlacesCoverage(bounded(t), []string{"no-such-family"})
	if err != nil || len(none.Kinds)+len(none.Lands)+len(none.Addresses) != 0 {
		t.Errorf("no family: %+v %v", none, err)
	}
}
