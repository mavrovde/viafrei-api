//go:build integration

package store_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// placesAt is a point far from every fixture site; the plants lie north of
// it, at the metres their names say.
var placesAt = geo.Point{Lat: 70, Lon: -30}

func placeSite(feed, id, name, operator, street, house, postcode, city, version string, lat string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return "'" + s + "'"
	}
	return `insert into public.charging_sites (source_feed, site_id, name, operator, street, house_number, postcode, city, source_version, geom)
values ('` + feed + `', '` + id + `', ` + q(name) + `, ` + q(operator) + `, ` + q(street) + `, ` + q(house) + `, ` + q(postcode) + `, ` + q(city) + `, ` +
		q(version) + `, st_setsrid(st_makepoint(-30, ` + lat + `), 4326)::geography)`
}

func placePoint(feed, site, key, evse, connector, kw, price, taxIncluded, rate string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return "'" + s + "'"
	}
	return `insert into public.charging_points (source_feed, site_id, point_key, evse_id, connector_types, max_power_kw, price_per_kwh,
  price_tax_included, price_tax_rate, price_currency, geom)
select '` + feed + `', '` + site + `', '` + key + `', ` + q(evse) + `, array['` + connector + `'], ` + kw + `, ` + q(price) + `::numeric, ` +
		q(taxIncluded) + `::boolean, ` + q(rate) + `::numeric, case when ` + q(price) + ` is null then null else 'EUR' end, s.geom
  from public.charging_sites s where s.source_feed = '` + feed + `' and s.site_id = '` + site + `'`
}

func placeStatus(evse, status, age string) string {
	return `insert into public.charging_point_status (observed_at, evse_id, source_feed, raw, status)
values (now() - interval '` + age + `', '` + evse + `', 'afir_zeta', '{}', '` + status + `')`
}

// placesSeed: an operator's site with a free and an occupied point and a
// net price (100 m); three register rows of one operator's place (1000 to
// 1040 m) at the town's point; a site of a stale status and a gross price
// (2000 m); one wholly out of order (3000 m); one priced by its tax rate
// (4000 m); a register row filed under a town 30 km off (5000 m); and a
// register row at the town's point whose address lies at 7000 m.
var placesSeed = []string{
	`select vf_test.ensure_day('public.charging_point_status', now()), vf_test.ensure_day('public.charging_point_status', now() - interval '2 hours')`,
	`insert into public.charging_feed_register (feed_key, source_id, role, site_rank, twin_radius_m, stale_after_s, statement_max_age_s) values
	   ('afir_alpha', 'a', 'site', 1, null, null, null), ('afir_beta', 'b', 'site', 90, 50, null, null),
	   ('afir_zeta', 'z', 'status', null, null, 900, 3600), ('afir_alpha_stale', 's', 'status', null, null, 900, 3600)`,
	`update public.charging_feed_register set frozen_after_s = 7200, points_from = 'afir_alpha' where feed_key = 'afir_alpha_stale'`,
	`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, geom) values
	   ('city', 'Nordhafen', 'nordhafen', 'bkg_gvisys', 'g1', st_setsrid(st_makepoint(-30, 70.008993), 4326)::geography),
	   ('city', 'Fernstadt', 'fernstadt', 'bkg_gvisys', 'g2', st_setsrid(st_makepoint(-30, 70.269796), 4326)::geography)`,
	`insert into public.osm_addresses (osm_type, osm_id, street, house_number, street_norm, house_number_norm, postcode, place, place_norm, geom, extract_date)
	   values ('node', 7001, 'Kaiweg', '7', 'kaiweg', '7', '99999', 'Nordhafen', 'nordhafen', st_setsrid(st_makepoint(-30, 70.062952), 4326)::geography, '2026-11-01')`,
	placeSite("afir_alpha", "a1", "Q-Park Nord", "Q-Park", "Hafenstraße", "1", "99999", "Nordhafen", "", "70.000899"),
	placePoint("afir_alpha", "a1", "a1k1", "XX*QPK*E1", "iec62196T2COMBO", "150", "0.57", "false", ""),
	placePoint("afir_alpha", "a1", "a1k2", "XX*QPK*E2", "iec62196T2", "22", "", "", ""),
	placeStatus("XX*QPK*E1", "AVAILABLE", "1 minute"),
	placeStatus("XX*QPK*E2", "CHARGING", "1 minute"),
	placeSite("afir_beta", "r1", "bnetza_lsr_70.00899_-30", "Ladewerk GmbH", "Bergweg", "5", "99999", "Nordhafen", "2026-09-01", "70.008993"),
	placeSite("afir_beta", "r2", "bnetza_lsr_70.00917_-30", "Ladewerk GmbH", "Bergweg", "5", "99999", "Nordhafen", "2026-09-01", "70.009173"),
	placeSite("afir_beta", "r3", "bnetza_lsr_70.00935_-30", "Ladewerk GmbH", "Bergweg", "5", "99999", "Nordhafen", "2026-09-01", "70.009353"),
	placePoint("afir_beta", "r1", "r1k1", "", "iec62196T2COMBO", "50", "", "", ""),
	placePoint("afir_beta", "r2", "r2k1", "", "iec62196T2COMBO", "50", "", "", ""),
	placePoint("afir_beta", "r3", "r3k1", "", "iec62196T2COMBO", "50", "", "", ""),
	placeSite("afir_alpha", "a5", "Altmarkt", "", "", "", "", "", "", "70.017986"),
	placePoint("afir_alpha", "a5", "a5k1", "XX*ALT*E1", "iec62196T2", "11", "0.49", "true", ""),
	placeStatus("XX*ALT*E1", "AVAILABLE", "2 hours"),
	placeSite("afir_alpha", "a6", "Nullplatz", "", "", "", "", "", "", "70.026979"),
	placePoint("afir_alpha", "a6", "a6k1", "XX*NUL*E1", "iec62196T2", "11", "", "", ""),
	placeStatus("XX*NUL*E1", "FAULTED", "1 minute"),
	placeSite("afir_alpha", "a7", "Südkai", "", "", "", "", "", "", "70.035973"),
	placePoint("afir_alpha", "a7", "a7k1", "", "iec62196T2", "11", "0.40", "", "19"),
	placeSite("afir_beta", "r9", "bnetza_lsr_70.04497_-30", "", "Ringstraße", "2", "11111", "Fernstadt", "2026-10-01", "70.044966"),
	placePoint("afir_beta", "r9", "r9k1", "", "iec62196T2", "22", "", "", ""),
	placeSite("afir_beta", "r10", "bnetza_lsr_70.00989_-30", "", "Kaiweg", "7", "99999", "Nordhafen", "2026-09-01", "70.009892"),
	placePoint("afir_beta", "r10", "r10k1", "", "iec62196T2", "22", "", "", ""),
}

func placesService(st *store.Store) *features.Service {
	return features.New(features.Repositories{Register: st, Charging: st, ChargingPlaces: st}, features.Settings{Language: "de"}, time.Now)
}

var placesKey = access.Record{Plan: access.Plan{Name: "t", UseClass: "commercial", Tier: "standard", Sources: []string{"afir_alpha", "afir_beta", "afir_zeta"}}}

func TestChargingPlacesOnTheStore(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001", "FR-ENERGY-004")
	st, _ := private(t, placesSeed...)
	ctx := bounded(t)
	a, err := placesService(st).ChargingPlaces(ctx, placesKey, features.PlaceQuery{At: placesAt, RadiusM: 10000, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range a.Items {
		var ids []string
		for _, m := range p.Members {
			ids = append(ids, m.Record.SiteID)
		}
		lines = append(lines, strings.Join(ids, "+")+" "+p.Label)
	}
	want := []string{
		"a1 Q-Park Nord, Hafenstraße 1, 99999 Nordhafen",
		"r1+r2+r3 Bergweg 5, 99999 Nordhafen",
		"a5 Altmarkt",
		"a7 Südkai",
		"r9 Standort bei Nordhafen (im Register mit der Adresse 11111 Fernstadt gemeldet, die nicht am Standort liegt — vermutlich die Anschrift des Betreibers)",
		"r10 Kaiweg 7, 99999 Nordhafen",
		"a6 Nullplatz",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("places:\n%s", strings.Join(lines, "\n"))
	}
	byID := map[string]energy.Place{}
	for _, p := range a.Items {
		byID[p.Members[0].Record.SiteID] = p
	}
	// The operator's site: one free, one occupied, its net price.
	q := byID["a1"].Members[0]
	if tl := energy.TallyOf(q.States); tl.Available != 1 || tl.Occupied != 1 || q.StatusAsOf == nil || *q.Price != (energy.Price{Eur: 0.57}) {
		t.Errorf("a1: %+v %v %+v", tl, q.StatusAsOf, q.Price)
	}
	// Three register rows of one place: one line, no status, no price, the
	// register's release.
	for _, m := range byID["r1"].Members {
		if m.Price != nil || slices.ContainsFunc(m.States, func(v energy.Verdict) bool { return v != energy.VerdictUnknown }) {
			t.Errorf("a register row with a status or a price: %+v", m)
		}
	}
	if r := energy.RegisterReleaseOf([]energy.PlaceRecord{byID["r1"].Members[0].Record}, a.Register); r == nil || *r != "2026-09-01" {
		t.Errorf("r1's release %v", r)
	}
	if a.RegisterReleasedOn == nil || *a.RegisterReleasedOn != "2026-10-01" {
		t.Errorf("the answer's release %v", a.RegisterReleasedOn)
	}
	// Stale: the last state kept, as of its pull.
	if s := byID["a5"].Members[0]; s.States[0] != energy.VerdictStale || s.StaleStates.Available != 1 || s.StaleAsOf == nil || !s.Price.IncludesVat {
		t.Errorf("a5: %+v", s)
	}
	// Wholly out of order: last; zero free and zero occupied, a status.
	if s := byID["a6"].Members[0]; energy.TallyOf(s.States) != (energy.Tally{OutOfOrder: 1, Total: 1}) || s.StatusAsOf == nil {
		t.Errorf("a6: %+v", s)
	}
	if p := byID["a7"].Members[0].Price; p == nil || p.Eur < 0.4759 || p.Eur > 0.4761 || !p.IncludesVat {
		t.Errorf("a7's gross price %+v", p)
	}
	// Filed under a town 30 km off; placed at its address.
	if e := byID["r9"].Elsewhere; e == nil || e.LocatedNear == nil || *e.LocatedNear != "Nordhafen" {
		t.Errorf("r9 elsewhere %+v", e)
	}
	if r := byID["r10"].Members[0].Record; r.Reported == nil || r.DistanceM < 6999 || r.DistanceM > 7001 {
		t.Errorf("r10 not placed at its address: %+v", r)
	}

	// Three of the first five places publish a price.
	five, err := placesService(st).ChargingPlaces(ctx, placesKey, features.PlaceQuery{At: placesAt, RadiusM: 10000, Limit: 5})
	if err != nil || len(five.Items) != 5 || !five.HasMore {
		t.Fatalf("five: %d %v", len(five.Items), err)
	}
	priced := 0
	for _, p := range five.Items {
		if slices.ContainsFunc(p.Members, func(j energy.Judged) bool { return j.Price != nil }) {
			priced++
		}
	}
	if priced != 3 {
		t.Errorf("priced %d of 5", priced)
	}

	// Only available: the free site alone; the rest counted.
	free, err := placesService(st).ChargingPlaces(ctx, placesKey, features.PlaceQuery{At: placesAt, RadiusM: 10000, Limit: 5, OnlyAvailable: true})
	if err != nil || len(free.Items) != 1 || free.Items[0].Members[0].Record.SiteID != "a1" || *free.Dropped != (energy.Dropped{NotFree: 1, Stale: 1, NoStatus: 6}) {
		t.Errorf("only available: %+v %+v %v", free.Items, free.Dropped, err)
	}
	// A plug and a power: CCS from 100 kW.
	fast, err := placesService(st).ChargingPlaces(ctx, placesKey, features.PlaceQuery{At: placesAt, RadiusM: 10000, Limit: 5, MinKw: 100, Connector: "ccs2"})
	if err != nil || len(fast.Items) != 1 || fast.Items[0].Members[0].Record.SiteID != "a1" || len(fast.Items[0].Members[0].Record.Points) != 1 {
		t.Errorf("ccs from 100 kW: %+v %v", fast.Items, err)
	}

	// The site id is the key a charger watch takes back: the site by id.
	site, err := placesService(st).ChargingSite(ctx, placesKey, "afir_alpha:"+byID["a1"].Members[0].Record.SiteID, time.Hour)
	if err != nil || len(site.Items) != 1 || site.Items[0].ID != "afir_alpha:a1" {
		t.Errorf("the site by its id: %+v %v", site.Items, err)
	}

	// No site within the radius: none, and the circle's count, 0.
	none, err := placesService(st).ChargingPlaces(ctx, placesKey, features.PlaceQuery{At: geo.Point{Lat: 60, Lon: -40}, RadiusM: 1000, Limit: 5})
	if err != nil || len(none.Items) != 0 || none.InRadius == nil || *none.InRadius != 0 {
		t.Errorf("none: %+v %v", none, err)
	}
}

func TestChargingPlacesRegisterAndEmptyMasters(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	st, _ := private(t, placesSeed...)
	ctx := bounded(t)
	reg, err := st.ChargingRegister(ctx, []string{"afir_alpha_stale"})
	if err != nil || len(reg.Status) != 1 || reg.Status[0].FrozenAfter == nil || *reg.Status[0].FrozenAfter != 2*time.Hour ||
		reg.Status[0].PointsFrom == nil || *reg.Status[0].PointsFrom != "afir_alpha" {
		t.Fatalf("a frozen-after feed: %+v %v", reg, err)
	}
	// No site feed: no master, an empty array, not a null one; no record.
	recs, err := st.ChargingPlaceRecords(ctx, energy.PlaceRead{At: placesAt, RadiusM: 10000, Scan: 5}, reg, time.Now())
	if err != nil || len(recs) != 0 {
		t.Errorf("no masters: %+v %v", recs, err)
	}
}

func TestChargingPlacesReadsFailEach(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	_, pdb := private(t, placesSeed...)
	fst, f := faultyOn(t, pdb.ReaderURL)
	now := time.Now()
	reg := energy.SourceRegister{Sites: []energy.SiteSource{{Key: "afir_beta", Rank: 90}}, Status: []energy.StatusSource{{Key: "afir_zeta", StaleAfter: time.Hour}}}
	ten := 10
	reg.Sites[0].TwinRadiusM = &ten
	rec := energy.PlaceRecord{Source: "afir_beta", SiteID: "r10", Street: ptr("Kaiweg"), HouseNumber: ptr("7"), City: ptr("Nordhafen"), Postcode: ptr("99999")}
	failEach(t, fst, f, "ChargingRegister", func(ctx context.Context, s *store.Store) error {
		_, err := s.ChargingRegister(ctx, []string{"afir_beta"})
		return err
	})
	failEach(t, fst, f, "ChargingPlaceRecords", func(ctx context.Context, s *store.Store) error {
		_, err := s.ChargingPlaceRecords(ctx, energy.PlaceRead{At: placesAt, RadiusM: 10000, Scan: 5}, reg, now)
		return err
	})
	failEach(t, fst, f, "ChargingLiveness", func(ctx context.Context, s *store.Store) error {
		_, err := s.ChargingLiveness(ctx, reg, now)
		return err
	})
	failEach(t, fst, f, "ChargingAtTownPoint", func(ctx context.Context, s *store.Store) error {
		_, err := s.ChargingAtTownPoint(ctx, []energy.PlaceRecord{rec})
		return err
	})
	failEach(t, fst, f, "ChargingAddresses", func(ctx context.Context, s *store.Store) error { _, err := s.ChargingAddresses(ctx, rec); return err })
	failEach(t, fst, f, "ChargingRegisterElsewhere", func(ctx context.Context, s *store.Store) error {
		_, err := s.ChargingRegisterElsewhere(ctx, []energy.PlaceRecord{rec}, reg, now)
		return err
	})
}

func ptr(s string) *string { return &s }
