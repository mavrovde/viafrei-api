//go:build integration

package store_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
)

// A price a poll confirmed unchanged is restated in fuel_price_latest, and
// the ticks gain no row: the station's prices are read from it, as today's
// fuel read takes them, so a confirmed price is as current as its newest
// confirmation (current, under today's strict comparison, at 29 minutes,
// and not at 30 or 31). A retracted price (deleted_at) is not read.
func TestFuelPricesAreTheNewestConfirmation(t *testing.T) {
	var (
		id, fam, product string
		lat, lon         float64
		tick             time.Time
	)
	owner(t, `select l.station_id, l.source_feed, l.product, st_y(f.geom::geometry), st_x(f.geom::geometry), l.observed_at
from fuel_price_latest l join fuel_stations f on f.id = l.station_id
where l.deleted_at is null and l.price is not null and f.deleted_at is null and f.geom is not null
order by l.observed_at desc, 1, 3 limit 1`, &id, &fam, &product, &lat, &lon, &tick)
	restated := tick.Add(6 * time.Hour).Truncate(time.Second)
	st, _ := private(t,
		`update fuel_price_latest set observed_at = '`+restated.Format(time.RFC3339)+`' where station_id = '`+id+`' and product = '`+product+`'`,
		`update fuel_price_latest set deleted_at = now() where station_id = '`+id+`' and product <> '`+product+`'`)
	ctx := access.WithAudience(bounded(t), access.AudienceOwn)
	near, err := st.FuelNear(ctx, energy.FuelQuery{At: geo.Point{Lat: lat, Lon: lon}, RadiusM: 50, Limit: 10}, []string{fam})
	if err != nil {
		t.Fatal(err)
	}
	var got *energy.FuelStation
	for i := range near {
		if near[i].ID == id {
			got = &near[i]
		}
	}
	if got == nil || len(got.Prices) != 1 || got.Prices[0].Product != product || !got.Prices[0].ObservedAt.Equal(restated) {
		t.Fatalf("station %s: %+v", id, got)
	}
	p := got.Prices[0]
	for _, c := range []struct {
		age     time.Duration
		current bool
	}{{29 * time.Minute, true}, {30 * time.Minute, false}, {31 * time.Minute, false}} {
		if p.Current(restated.Add(c.age), 30*time.Minute) != c.current {
			t.Errorf("%v after the confirmation: current %v", c.age, !c.current)
		}
	}
	page, err := st.FuelStation(ctx, id, []string{fam}, restated.Add(29*time.Minute), 30*time.Minute)
	if err != nil || len(page.Prices) != 1 || !page.Prices[0].ObservedAt.Equal(restated) {
		t.Fatalf("by id, 29 min after the confirmation: %+v %v", page.Prices, err)
	}
	// Past the held window the price is not held; the station is.
	if late, err := st.FuelStation(ctx, id, []string{fam}, restated.Add(31*time.Minute), 30*time.Minute); err != nil || len(late.Prices) != 0 {
		t.Errorf("by id, 31 min after: %+v %v", late.Prices, err)
	}
}

// Every kind the platform's facility tables allow (their CHECK: rest_area,
// car_park, park_and_ride, truck, other) maps to its contract kind; a car
// park (a Parkhaus) is a garage, and only a stored other is other.
func TestParkingKindsMapEveryStoredKind(t *testing.T) {
	want := map[string]parking.Kind{"rest_area": parking.KindRestArea, "car_park": parking.KindGarage,
		"park_and_ride": parking.KindParkAndRide, "truck": parking.KindTruckParking, "other": parking.KindOther}
	st, cp := private(t, `with k(n, kind) as (values (1, 'rest_area'), (2, 'car_park'), (3, 'park_and_ride'), (4, 'truck'), (5, 'other')),
f as (select id, row_number() over (order by id) n from parking_facilities where deleted_at is null and closed_at is null and geom is not null)
update parking_facilities p set kind = k.kind from f join k using (n) where p.id = f.id`)
	type planted struct {
		id, kind string
		at       geo.Point
	}
	var rows []planted
	{
		conn, err := cp.Owner(bounded(t))
		if err != nil {
			t.Fatal(err)
		}
		r, err := conn.Query(bounded(t), `select 'p:' || id, kind, st_y(geom::geometry), st_x(geom::geometry) from parking_facilities
where deleted_at is null and closed_at is null and geom is not null order by id limit 5`)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var p planted
			if err := r.Scan(&p.id, &p.kind, &p.at.Lat, &p.at.Lon); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, p)
		}
		_ = conn.Close(context.Background())
	}
	if len(rows) != len(want) {
		t.Fatalf("planted %d kinds", len(rows))
	}
	listed := families(t, st, "parking")
	for _, p := range rows {
		sites, _, _, err := st.ParkingNear(bounded(t), parking.Query{At: p.at, RadiusM: 1, Limit: 10}, listed, listed, fixtures.ReferenceInstant)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(sites, func(s parking.Site) bool { return s.ID == p.id })
		if i < 0 {
			t.Errorf("planted %s (%s) not read", p.id, p.kind)
		} else if sites[i].Kind != want[p.kind] {
			t.Errorf("stored %s reads as %s, want %s", p.kind, sites[i].Kind, want[p.kind])
		}
	}
}

// A warning says where its level came from, as stored: published (the
// DWD's number), derived_from_severity (mapped from its CAP severity
// word), or nothing when not stored.
func TestWarningLevelSourceIsAsStored(t *testing.T) {
	type ask struct {
		cell string
		at   time.Time
	}
	conn, err := db.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(bounded(t), `select distinct warncell_id, valid_from from weather_warnings where warncell_id is not null order by 2, 1 limit 40`)
	if err != nil {
		t.Fatal(err)
	}
	var asks []ask
	for rows.Next() {
		var a ask
		if err := rows.Scan(&a.cell, &a.at); err != nil {
			t.Fatal(err)
		}
		asks = append(asks, a)
	}
	_ = conn.Close(context.Background())
	// Three warnings the cell read answers, each with the ask that reads it.
	read := func(st *store.Store, a ask) []weather.Warning {
		t.Helper()
		got, err := st.CellWarnings(bounded(t), weather.CellQuery{Cell: a.cell}, families(t, st, "weather"), a.at)
		if err != nil {
			t.Fatal(err)
		}
		return got.Warnings
	}
	seed := seeded(t)
	picked := map[string]ask{}
	for _, a := range asks {
		for _, w := range read(seed, a) {
			if _, ok := picked[w.ID]; !ok && len(picked) < 3 {
				picked[w.ID] = a
			}
		}
	}
	if len(picked) != 3 {
		t.Fatalf("the cell read answers %d warnings", len(picked))
	}
	ids := slices.Sorted(maps.Keys(picked))
	values := []string{"published", "derived_from_severity", ""}
	var plants []string
	for i, id := range ids {
		v := "null"
		if values[i] != "" {
			v = "'" + values[i] + "'"
		}
		plants = append(plants, fmt.Sprintf(`update weather_warnings set level_source = %s where 'dwd:' || id = '%s'`, v, id))
	}
	st, _ := private(t, plants...)
	for i, id := range ids {
		var got *weather.Warning
		for _, w := range read(st, picked[id]) {
			if w.ID == id {
				got = &w
			}
		}
		switch {
		case got == nil:
			t.Errorf("%s not read", id)
		case values[i] == "" && got.LevelSource != nil, values[i] != "" && (got.LevelSource == nil || *got.LevelSource != values[i]):
			t.Errorf("%s: level source %v, want %q", id, got.LevelSource, values[i])
		}
	}
}
