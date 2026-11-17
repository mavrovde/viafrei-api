package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/national"
)

// The national picture, one statement per domain: $1 the families, $2 the
// instant; the energy statement's $3 is the fuel price window in seconds.
// Each row is section, key, name, family, n, m, low, mid.
var nationalSQL = map[string]string{
	"roads": `
select 'situations', e.event_kind, null::text, e.source_ref, count(*)::int, null::int, null::float8, null::float8
from contract.traffic_event e
where ` + nationalInForce + `
group by 2, 4
union all
select 'states', e.state_code, null, e.source_ref, count(*)::int, null, null, null
from contract.traffic_event e
where ` + nationalInForce + ` and e.state_code is not null
group by 2, 4
order by 1, 2, 4`,
	"energy": `
with latest as (
  select distinct on (o.station_id, o.product_code) o.product_code, o.price, o.source_ref
  from contract.fuel_price_observation o
  join contract.fuel_station s on s.station_id = o.station_id and s.withdrawn_at is null and s.source_ref = any($1) and ` + permitted("s") + `
  where o.price is not null and o.source_ref = any($1) and ` + permitted("o") + `
    and o.observed_at > $2::timestamptz - make_interval(secs => $3::int) and o.observed_at <= $2::timestamptz
  order by o.station_id, o.product_code, o.observed_at desc)
select 'fuel', l.product_code, null::text, l.source_ref, count(*)::int, null::int,
       min(l.price)::float8, (percentile_cont(0.5) within group (order by l.price))::float8
from latest l
group by 2, 4
union all
select 'charging', c.source_ref, null, c.source_ref, count(distinct c.site_id)::int, count(p.point_id)::int, null, null
from contract.charging_site c
left join contract.charge_point p on p.site_id = c.site_id and ` + permitted("p") + `
where c.withdrawn_at is null and c.source_ref = any($1) and ` + permitted("c") + `
group by 2, 4
order by 1, 2, 4`,
	"parking": `
select 'parking', f.parking_kind, null::text, f.source_ref, count(*)::int, null::int, null::float8, null::float8
from contract.parking_facility f
where f.withdrawn_at is null and f.source_ref = any($1) and ` + permitted("f") + ` and $2::timestamptz is not null
group by 2, 4
order by 1, 2, 4`,
	"transit": `
select 'facilities', f.facility_kind || '/' || coalesce(f.facility_status, 'unknown'), null::text, f.source_ref,
       count(*)::int, count(distinct f.stop_place_id)::int, null::float8, null::float8
from contract.station_facility f
where f.source_ref = any($1) and ` + permitted("f") + ` and $2::timestamptz is not null
group by 2, 4
union all
select 'punctuality', p.area_level || ':' || p.area_code, p.area_name, p.source_ref, p.trips_observed, p.trips_late,
       null, p.median_worst_delay_s::float8
from contract.transit_punctuality p
where p.source_ref = any($1) and ` + permitted("p") + `
order by 1, 2, 4`,
	"weather": `
select 'warnings', w.warning_level::text, null::text, w.source_ref, count(*)::int, count(distinct w.region_code)::int, null::float8, null::float8
from contract.weather_warning w
where w.withdrawn_at is null and w.message_status <> 'cancelled' and w.source_ref = any($1) and ` + permitted("w") + `
  and w.valid_from <= $2 and (w.valid_to is null or w.valid_to > $2)
group by 2, 4
order by 1, 2, 4`,
}

// nationalInForce are the road situations of the families in force at $2.
const nationalInForce = `e.source_ref = any($1) and ` + eventPermitted + `
  and e.withdrawn_at is null and e.valid_from <= $2 and (e.valid_to is null or e.valid_to > $2)`

// National reads one domain's part of the national picture from the given
// families at now (features.National); fuelWindow is how young a price must
// be to count. A domain with no part reads nothing.
func (s *Store) National(ctx context.Context, domain string, families []string, now time.Time, fuelWindow time.Duration) ([]national.Row, error) {
	sql, ok := nationalSQL[domain]
	if !ok {
		return nil, nil
	}
	args := []any{families, now}
	if domain == "energy" {
		args = append(args, int(fuelWindow.Seconds()))
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]national.Row, error) {
		rows, _ := tx.Query(ctx, sql, args...)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (national.Row, error) {
			var v national.Row
			err := r.Scan(&v.Section, &v.Key, &v.Name, &v.Family, &v.N, &v.M, &v.Low, &v.Mid)
			return v, err
		})
	})
}
