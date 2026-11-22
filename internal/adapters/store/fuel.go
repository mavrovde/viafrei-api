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
// (a station with no price carries none).
var fuelNear = `
with here as (select st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography g)
select s.station_id, s.name, s.brand, st_y(s.location), st_x(s.location),
       round(st_distance(s.location::geography, here.g))::int, s.open_state, s.source_ref, s.whole_day, s.opening_times,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}')
from contract.fuel_station s
cross join here
left join lateral (
  select array_agg(l.product_code order by l.product_code) products,
         array_agg(l.price::float8 order by l.product_code) prices,
         array_agg(l.observed_at order by l.product_code) observed
  from (select distinct on (o.product_code) o.product_code, o.price, o.observed_at
          from contract.fuel_price_observation o
         where o.station_id = s.station_id and o.price is not null
           and o.source_ref = any($4) and ` + permitted("o") + `
         order by o.product_code, o.observed_at desc) l) p on true
where s.withdrawn_at is null
  and s.source_ref = any($4)
  and ` + permitted("s") + `
  and st_dwithin(s.location::geography, here.g, $3)
order by st_distance(s.location::geography, here.g), s.station_id
limit $5`

// FuelNear reads the filling stations within q's radius (features.Fuel).
func (s *Store) FuelNear(ctx context.Context, q energy.FuelQuery, families []string) ([]energy.FuelStation, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.FuelStation, error) {
		rows, _ := tx.Query(ctx, fuelNear, q.At.Lon, q.At.Lat, q.RadiusM, families, energy.MaxFuelScan)
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
var fuelCoverage = `
with here as (select st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography g),
held as (
  select s.station_id, s.location::geography loc
  from contract.fuel_station s
  where s.withdrawn_at is null and s.source_ref = any($4) and ` + permitted("s") + `)
select
  (select count(*)::int from held, here where st_dwithin(held.loc, here.g, $3)),
  (select min(st_distance(held.loc, here.g)) from held, here
    where st_dwithin(held.loc, here.g, $6)
      and exists (select 1 from contract.fuel_price_observation o
                   where o.station_id = held.station_id and o.price is not null and ($5::text is null or o.product_code = $5::text)
                     and o.source_ref = any($4) and ` + permitted("o") + `)),
  case when $5::text is null then 0 else
  (select count(*)::int from held, here where st_dwithin(held.loc, here.g, $3)
     and not exists (select 1 from contract.fuel_price_observation o
                      where o.station_id = held.station_id and o.price is not null and o.product_code = $5::text
                        and o.observed_at > $8::timestamptz - make_interval(secs => $7::int)
                        and o.source_ref = any($4) and ` + permitted("o") + `)) end`

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
		err := tx.QueryRow(ctx, fuelCoverage, q.At.Lon, q.At.Lat, q.RadiusM, families, grade, energy.MaxFuelRadiusM,
			int(q.Fresh/time.Second), now).Scan(&c.StationsInRadius, &c.NearestPricedM, &c.WithoutCurrent)
		return c, err
	})
}

// fuelByID reads one filling station by its id ($1) with its address and,
// per grade, its newest price reported in the held window ($3 seconds) up to
// the instant $4; $2 the families. Withdrawn, it is not found.
var fuelByID = opaque(`
select s.station_id, s.name, s.brand, st_y(s.location), st_x(s.location), s.open_state, s.source_ref, s.whole_day, s.opening_times,
       s.street, s.house_number, s.postcode, s.city,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}')
from contract.fuel_station s
left join lateral (
  select array_agg(l.product_code order by l.product_code) products,
         array_agg(l.price::float8 order by l.product_code) prices,
         array_agg(l.observed_at order by l.product_code) observed
  from (select distinct on (o.product_code) o.product_code, o.price, o.observed_at
          from contract.fuel_price_observation o
         where o.station_id = s.station_id and o.price is not null
           and o.observed_at > $4::timestamptz - make_interval(secs => $3::int) and o.observed_at <= $4::timestamptz
           and o.source_ref = any($2) and `+permitted("o")+`
         order by o.product_code, o.observed_at desc) l) p on true
where s.station_id = $1::text
  and s.withdrawn_at is null
  and s.source_ref = any($2)
  and `+permitted("s"), "text", "text[]", "integer", "timestamp with time zone")

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
		err := tx.QueryRow(ctx, fuelByID, id, families, int(held.Seconds()), now).Scan(
			&v.ID, &v.Name, &v.Brand, &v.Location.Lat, &v.Location.Lon, &v.Open, &v.Family, &v.Hours.WholeDay, &v.Hours.Times,
			&v.Address.Street, &v.Address.HouseNumber, &v.Address.Postcode, &v.Address.City, &products, &prices, &observed)
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
