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
// (a station with no price carries none). A family is read when its source
// rule gives it to the key (ruledFamilies): Tankerkönig's prices only to an
// own key.
var fuelNear = named("api_fuel_near", `
with here as (select st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography g),
fam as (`+ruledFamilies("(select $4::text[])")+`)
select f.id, f.name, f.brand, st_y(f.geom::geometry), st_x(f.geom::geometry),
       round(st_distance(f.geom, here.g))::int, coalesce(f.state, 'unknown'), coalesce(f.source_feed, f.source), f.whole_day, f.opening_times,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}')
from fuel_stations f
cross join here
cross join fam
left join lateral (
  select array_agg(l.product order by l.product) products,
         array_agg(l.price::float8 order by l.product) prices,
         array_agg(l.observed_at order by l.product) observed
  from (select distinct on (o.product) o.product, o.price, o.observed_at
          from fuel_prices o
         where o.station_id = f.id and o.price is not null and o.source = any(fam.f)
         order by o.product, o.observed_at desc) l) p on true
where f.deleted_at is null and f.geom is not null
  and coalesce(f.source_feed, f.source) = any(fam.f)
  and st_dwithin(f.geom, here.g, (select $3::float8))
order by st_distance(f.geom, here.g), f.id
limit (select $5::bigint)`, "float8", "float8", "float8", "text[]", "bigint")

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
			)
			err := r.Scan(&v.ID, &v.Name, &v.Brand, &v.Location.Lat, &v.Location.Lon, &v.DistanceM, &v.Open, &v.Family,
				&v.Hours.WholeDay, &v.Hours.Times, &products, &prices, &observed)
			for i := range products {
				v.Prices = append(v.Prices, energy.FuelPrice{Product: products[i], Price: prices[i], ObservedAt: observed[i]})
			}
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
// one station's observations, as the price read's: the observations are a
// time series, and a check not tied to the station would scan it whole.
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
      and exists (select 1 from fuel_prices o
                   where o.station_id = held.station_id and o.price is not null
                     and ((select $5::text) is null or o.product = (select $5::text)) and o.source = any((select fam.f from fam)::text[]))),
  case when (select $5::text) is null then 0 else
  (select count(*)::int from held, here where st_dwithin(held.loc, here.g, (select $3::float8))
     and not exists (select 1 from fuel_prices o
                      where o.station_id = held.station_id and o.price is not null and o.product = (select $5::text)
                        and o.observed_at > (select $8::timestamptz) - make_interval(secs => (select $7::int))
                        and o.source = any((select fam.f from fam)::text[]))) end`,
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
// per grade, its newest price reported in the held window ($3 seconds) up to
// the instant $4; $2 the families. Withdrawn, it is not found.
var fuelByID = named("api_fuel_by_id", `
with fam as (`+ruledFamilies("(select $2::text[])")+`)
select f.id, f.name, f.brand, st_y(f.geom::geometry), st_x(f.geom::geometry), coalesce(f.state, 'unknown'), coalesce(f.source_feed, f.source),
       f.whole_day, f.opening_times, f.street, f.house_no, f.postcode, f.city,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}')
from fuel_stations f
cross join fam
left join lateral (
  select array_agg(l.product order by l.product) products,
         array_agg(l.price::float8 order by l.product) prices,
         array_agg(l.observed_at order by l.product) observed
  from (select distinct on (o.product) o.product, o.price, o.observed_at
          from fuel_prices o
         where o.station_id = f.id and o.price is not null
           and o.observed_at > (select $4::timestamptz) - make_interval(secs => (select $3::int)) and o.observed_at <= (select $4::timestamptz)
           and o.source = any(fam.f)
         order by o.product, o.observed_at desc) l) p on true
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
		)
		rows, _ := run(ctx, tx, fuelByID, id, families, int(held.Seconds()), now)
		_, err := pgx.CollectOneRow(rows, func(r pgx.CollectableRow) (struct{}, error) {
			return struct{}{}, r.Scan(
				&v.ID, &v.Name, &v.Brand, &v.Location.Lat, &v.Location.Lon, &v.Open, &v.Family, &v.Hours.WholeDay, &v.Hours.Times,
				&v.Address.Street, &v.Address.HouseNumber, &v.Address.Postcode, &v.Address.City, &products, &prices, &observed)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// No such station is an answer, not a failure of storage.
			return energy.FuelStationPage{}, nil
		}
		for i := range products {
			v.Prices = append(v.Prices, energy.FuelPrice{Product: products[i], Price: prices[i], ObservedAt: observed[i]})
		}
		return v, err
	})
	if err == nil && out.ID == "" {
		err = energy.ErrNotFound
	}
	return out, err
}
