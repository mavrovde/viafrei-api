package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// fuelNear reads the filling stations within a radius, nearest first: $1,
// $2 the position's longitude and latitude; $3 the radius in metres; $4 the
// families; $5 the most stations. Each carries its newest price per grade
// (a station with no price carries none), from fuel_price_latest as today's
// MCP fuel read takes it: one row per station and grade, its observed time
// restated by every poll that confirms the price unchanged, where the ticks
// (fuel_prices) gain a row only when the price changes; a retracted price
// (deleted_at) is not read. A family is read when its source
// rule gives it to the key (ruledFamilies): Tankerkönig's prices only to an
// own key.
var fuelNear = named("api_fuel_near", `
with here as (select st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography g),
fam as (`+ruledFamilies("(select $4::text[])")+`)
select f.id, f.name, f.brand, st_y(f.geom::geometry), st_x(f.geom::geometry),
       round(st_distance(f.geom, here.g))::int, coalesce(f.source_feed, f.source), `+fuelHours+`,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}'), coalesce(p.open, '{}')
from fuel_stations f
cross join here
cross join fam
left join lateral (
  select array_agg(l.product order by l.product) products,
         array_agg(l.price::float8 order by l.product) prices,
         array_agg(l.observed_at order by l.product) observed,
         array_agg(l.is_open order by l.product) open
  from fuel_price_latest l
  where l.station_id = f.id and l.deleted_at is null and l.price is not null and l.source_feed = any(fam.f)) p on true
where f.deleted_at is null and f.geom is not null
  and coalesce(f.source_feed, f.source) = any(fam.f)
  and st_dwithin(f.geom, here.g, (select $3::float8))
order by st_distance(f.geom, here.g), f.id
limit (select $5::bigint)`, "float8", "float8", "float8", "text[]", "bigint")

// fuelHours are a station's published hours with the time they were last
// read from the provider, as today's fuel reads take them: hours never read
// (no detail_refreshed_at) are none, so no 24 h claim is made without its
// date.
const fuelHours = `case when f.detail_refreshed_at is not null then f.whole_day end,
       case when f.detail_refreshed_at is not null then f.opening_times end, f.detail_refreshed_at`

// fuelPrices are a station's newest prices per grade, each with the open
// state the station reported with it.
func fuelPrices(products []string, prices []float64, observed []time.Time, open []*bool) []energy.FuelPrice {
	var out []energy.FuelPrice
	for i := range products {
		out = append(out, energy.FuelPrice{Product: products[i], Price: prices[i], ObservedAt: observed[i], Open: open[i]})
	}
	return out
}

// FuelNear reads the filling stations within q's radius (features.Fuel).
func (s *Store) FuelNear(ctx context.Context, q energy.FuelQuery, families []string) ([]energy.FuelStation, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.FuelStation, error) {
		rows, _ := run(ctx, tx, fuelNear, q.At.Lon, q.At.Lat, float64(q.RadiusM), families, energy.MaxFuelScan)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (energy.FuelStation, error) {
			var (
				v        energy.FuelStation
				products []string
				prices   []float64
				observed []time.Time
				open     []*bool
			)
			err := r.Scan(&v.ID, &v.Name, &v.Brand, &v.Location.Lat, &v.Location.Lon, &v.DistanceM, &v.Family,
				&v.Hours.WholeDay, &v.Hours.Times, &v.Hours.AsOf, &products, &prices, &observed, &open)
			v.Prices = fuelPrices(products, prices, observed, open)
			v.Open, v.OpenObservedAt = energy.Reported(v.Prices)
			return v, err
		})
	})
}

// fuelCoverage reads what the families $4 hold around the position $1, $2
// (longitude, latitude): the stations within $3 metres; the metres to the
// nearest one with a price for the grade $5 (null: any grade) within $6
// metres; and, for a grade, the stations within $3 with no price for it
// reported in the $7 seconds before the instant $8. As the platform's
// coverage read, by no filter but the grade. Each price check is a probe of
// one station's newest prices (fuel_price_latest's key), as the price
// read's.
//
//nolint:misspell // materialized is the SQL keyword
var fuelCoverage = named("api_fuel_coverage", `
with here as (select st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography g),
fam as (`+ruledFamilies("(select $4::text[])")+`),
held as not materialized (
  select f.id station_id, f.geom loc
  from fuel_stations f, fam
  where f.deleted_at is null and f.geom is not null and coalesce(f.source_feed, f.source) = any(fam.f))
select
  (select count(*)::int from held, here where st_dwithin(held.loc, here.g, (select $3::float8))),
  (select min(st_distance(held.loc, here.g)) from held, here
    where st_dwithin(held.loc, here.g, (select $6::float8))
      and exists (select 1 from fuel_price_latest o
                   where o.station_id = held.station_id and o.deleted_at is null and o.price is not null
                     and ((select $5::text) is null or o.product = (select $5::text)) and o.source_feed = any((select fam.f from fam)::text[]))),
  case when (select $5::text) is null then 0 else
  (select count(*)::int from held, here where st_dwithin(held.loc, here.g, (select $3::float8))
     and not exists (select 1 from fuel_price_latest o
                      where o.station_id = held.station_id and o.deleted_at is null and o.price is not null and o.product = (select $5::text)
                        and o.observed_at > (select $8::timestamptz) - make_interval(secs => (select $7::int))
                        and o.source_feed = any((select fam.f from fam)::text[]))) end`,
	"float8", "float8", "float8", "text[]", "text", "float8", "int", "timestamptz")

// FuelCoverage reads what the families hold around q's position
// (features.Fuel).
func (s *Store) FuelCoverage(ctx context.Context, q energy.FuelQuery, families []string, now time.Time) (energy.FuelCoverage, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (energy.FuelCoverage, error) {
		var (
			c     energy.FuelCoverage
			grade *string
		)
		if q.Fuel != "" {
			grade = &q.Fuel
		}
		rows, _ := run(ctx, tx, fuelCoverage, q.At.Lon, q.At.Lat, float64(q.RadiusM), families, grade, float64(energy.MaxFuelRadiusM),
			int(q.Fresh/time.Second), now)
		_, err := pgx.CollectOneRow(rows, func(r pgx.CollectableRow) (struct{}, error) {
			return struct{}{}, r.Scan(&c.StationsInRadius, &c.NearestPricedM, &c.WithoutCurrent)
		})
		return c, err
	})
}

// fuelByID reads one filling station by its id ($1) with its address and,
// per grade, its newest price (fuel_price_latest, as the price read's)
// reported in the held window ($3 seconds) up to the instant $4; $2 the
// families. Withdrawn, it is not found.
var fuelByID = named("api_fuel_by_id", `
with fam as (`+ruledFamilies("(select $2::text[])")+`)
select f.id, f.name, f.brand, st_y(f.geom::geometry), st_x(f.geom::geometry), coalesce(f.source_feed, f.source),
       `+fuelHours+`, f.street, f.house_no, f.postcode, f.city,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}'), coalesce(p.open, '{}')
from fuel_stations f
cross join fam
left join lateral (
  select array_agg(l.product order by l.product) products,
         array_agg(l.price::float8 order by l.product) prices,
         array_agg(l.observed_at order by l.product) observed,
         array_agg(l.is_open order by l.product) open
  from fuel_price_latest l
  where l.station_id = f.id and l.deleted_at is null and l.price is not null
    and l.observed_at > (select $4::timestamptz) - make_interval(secs => (select $3::int)) and l.observed_at <= (select $4::timestamptz)
    and l.source_feed = any(fam.f)) p on true
where f.id = (select $1::text)
  and f.deleted_at is null and f.geom is not null
  and coalesce(f.source_feed, f.source) = any(fam.f)`, "text", "text[]", "int", "timestamptz")

// FuelStation reads one filling station by its id (features.Fuel), or
// energy.ErrNotFound.
func (s *Store) FuelStation(ctx context.Context, id string, families []string, now time.Time, held time.Duration) (energy.FuelStationPage, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (energy.FuelStationPage, error) {
		var (
			v        energy.FuelStationPage
			products []string
			prices   []float64
			observed []time.Time
			open     []*bool
		)
		rows, _ := run(ctx, tx, fuelByID, id, families, int(held.Seconds()), now)
		_, err := pgx.CollectOneRow(rows, func(r pgx.CollectableRow) (struct{}, error) {
			return struct{}{}, r.Scan(
				&v.ID, &v.Name, &v.Brand, &v.Location.Lat, &v.Location.Lon, &v.Family, &v.Hours.WholeDay, &v.Hours.Times, &v.Hours.AsOf,
				&v.Address.Street, &v.Address.HouseNumber, &v.Address.Postcode, &v.Address.City, &products, &prices, &observed, &open)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// No such station is an answer, not a failure of storage.
			return energy.FuelStationPage{}, nil
		}
		v.Prices = fuelPrices(products, prices, observed, open)
		v.Open, v.OpenObservedAt = energy.Reported(v.Prices)
		return v, err
	})
	if err == nil && out.ID == "" {
		err = energy.ErrNotFound
	}
	return out, err
}

// osmFuelUnpriced reads the OpenStreetMap filling stations of the families
// $4 within $3 metres of ($1, $2) that no station the prices are held for
// stands within 300 m of, nearest first, $5 at most, as today's
// OSM_FUEL_STATIONS_NEAR and its twin filter read them: the forty nearest
// fuel amenities in Germany's boundary whose name is not a gas dealer's
// ($6 the brand, $7 the name filter, as the station search spells them),
// then those without a held twin. No id, no state, no grade, no price.
//
//nolint:misspell // materialized is the SQL keyword
var osmFuelUnpriced = named("api_osm_fuel_unpriced", `
with cand as materialized (
  select p.name, p.brand, p.street, p.house_number, p.postcode, p.place, p.geom, p.source_id,
         st_distance(p.geom, `+atPosition+`) d
  from osm_pois p
  where p.closed_at is null and p.category = 'amenity' and p.category_value = 'fuel'
    and exists (select 1 from osm_boundary_de de_b where st_covers(de_b.geom, p.geom::geometry))
    and p.name !~* '(propan|fl(ü|ue)ssiggas(handel|vertrieb)|gashandel|gasflaschen)'
    and st_dwithin(p.geom, `+atPosition+`, (select $3::float8))
    and p.source_id = any((select $4::text[])::text[])
    and ((select $6::text) is null or coalesce(p.brand, '') ilike '%' || (select $6::text) || '%' or p.name ilike '%' || (select $6::text) || '%')
    and ((select $7::text) is null or p.name ilike '%' || (select $7::text) || '%' or coalesce(p.brand, '') ilike '%' || (select $7::text) || '%')
  order by p.geom <-> `+atPosition+`
  limit 40)
select c.name, c.brand, c.street, c.house_number, c.postcode, c.place, st_y(c.geom::geometry), st_x(c.geom::geometry), round(c.d)::int, c.source_id
from cand c
where not exists (select 1 from fuel_stations s where s.deleted_at is null and s.geom is not null and st_dwithin(s.geom, c.geom, 300))
order by c.d
limit (select $5::bigint)`, "float8", "float8", "float8", "text[]", "bigint", "text", "text")

// OSMFuelNear reads the OpenStreetMap filling stations without a price
// within q's radius (features.FuelStations).
func (s *Store) OSMFuelNear(ctx context.Context, q energy.FuelQuery, families []string, limit int) ([]energy.OSMFuelStation, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.OSMFuelStation, error) {
		rows, _ := run(ctx, tx, osmFuelUnpriced, q.At.Lon, q.At.Lat, float64(q.RadiusM), families, limit, nonEmpty(q.Brand), nonEmpty(q.Name))
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (energy.OSMFuelStation, error) {
			var v energy.OSMFuelStation
			err := r.Scan(&v.Name, &v.Brand, &v.Address.Street, &v.Address.HouseNumber, &v.Address.Postcode, &v.Address.City,
				&v.Location.Lat, &v.Location.Lon, &v.DistanceM, &v.Family)
			return v, err
		})
	})
}

// nonEmpty is s, or nil when it is empty.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
