//go:build integration

package store

import (
	"strings"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// The frozen view statements of the charging reads (legacyReads).

func legacyChargingSites(byID bool) string {
	q := `
with here as (select st_setsrid(st_makepoint($2, $1), 4326)::geography g),
near as (
  select c.site_id, c.source_ref, c.name, c.operator_name, c.access, c.origin_key,
         st_y(c.location) lat, st_x(c.location) lon,
         round(st_distance(c.location::geography, here.g))::int dist,
         c.location::geography <-> here.g d
  from contract.charging_site c, here
  where c.withdrawn_at is null
    and (c.source_ref = any($3)) is true
    and ` + permitted("c") + `
    and st_dwithin(c.location::geography, here.g, $4) and $14::text = ''
    and ($5::text[] is null or c.access = any($5))
    and ($11::text is null or coalesce(` + energy.OperatorKeySQL("c.operator_name") + `, '` + energy.NoOperatorKey + `') = $11)
    and (not $12::boolean or exists (
          select 1 from contract.charge_point p
          where p.site_id = c.site_id and ` + pointFilter + ` and ` + permitted("p") + `))
  order by c.location::geography <-> here.g
  fetch first $7 rows with ties),
sites as (
  select n.site_id, n.source_ref, n.name, n.operator_name, n.access, n.lat, n.lon, n.dist,
         row_number() over (order by n.d, n.source_ref, n.origin_key) rn
  from near n
  order by rn
  limit $7)
select s.site_id, s.source_ref, s.name, s.operator_name, s.access, s.lat, s.lon, s.dist,
       p.point_id, p.current_kind, p.power_max_kw::float8, o.availability, o.observed_at, o.source_ref,
       nx.next_at
from sites s
left join contract.charge_point p on p.site_id = s.site_id and ` + pointFilter + ` and ` + permitted("p") + `
left join lateral (
  select availability, observed_at, source_ref
  from contract.charge_point_state_observation o
  where o.point_id = p.point_id and o.source_ref = any($3) and o.observed_at <= $8 and ` + permitted("o") + `
  order by o.observed_at desc
  limit 1) o on true
left join lateral (
  select min(o.observed_at) next_at
  from contract.charge_point_state_observation o
  where o.point_id = p.point_id and o.source_ref = any($3) and o.observed_at > $8 and ` + permitted("o") + `) nx on true
order by s.rn = $7, s.dist, s.site_id, p.point_id`
	if byID {
		q = strings.Replace(q, "st_dwithin(c.location::geography, here.g, $4) and $14::text = ''", "c.site_id = $14::text and $4::float8 >= 0", 1)
	}
	return q
}

func init() {
	legacyReads["api_charging_near"] = legacyChargingSites(false)
	legacyReads["api_charging_by_id"] = legacyChargingSites(true)
	legacyReads["api_charging_in_radius"] = `select count(*)::int
from contract.charging_site c
where c.withdrawn_at is null
  and (c.source_ref = any($3::text[])) is true
  and ` + permitted("c") + `
  and st_dwithin(c.location::geography, st_setsrid(st_makepoint($2::float8, $1::float8), 4326)::geography, $4::float8)`
}
