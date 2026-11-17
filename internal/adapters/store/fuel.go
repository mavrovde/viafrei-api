package store

import (
	"context"
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
