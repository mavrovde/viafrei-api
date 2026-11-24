//go:build integration

package store

// The frozen view statements of the sharing reads (legacyReads).

func init() {
	for name, sql := range map[string]string{
		"api_sharing_stations_near": `
with near as (
  select s.station_id, s.system_id, s.system_name, s.name, s.capacity, s.is_virtual,
         st_y(s.location), st_x(s.location),
         round(st_distance(s.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography))::int as distance_m,
         s.status_seen_at, s.source_ref
    from contract.sharing_station s
   where st_dwithin(s.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)
     and s.source_ref = any($6::text[]) and ` + permitted("s") + `
)
select n.*, c.vehicles_available, c.electric_available, c.docks_available, c.is_renting, c.is_returning, c.observed_at, c.reported_at
  from near n
  left join lateral (
    select t.vehicles_available, t.electric_available, t.docks_available, t.is_renting, t.is_returning, t.observed_at, t.reported_at
      from contract.sharing_station_status t
     where t.station_id = n.station_id
       and t.observed_at > $5::timestamptz - interval '24 hours' and t.observed_at <= $5::timestamptz
     order by t.observed_at desc
     limit 1
  ) c on true
 order by n.distance_m, n.station_id
 limit $4::int`,
		"api_sharing_vehicles_near": `
select v.form_factor, v.propulsion, v.range_m,
       round(st_distance(v.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography))::int as distance_m,
       v.vehicles_seen_at, v.source_ref
  from (select distinct on (x.source_ref, st_astext(x.location)) x.vehicle_id, x.form_factor, x.propulsion, x.range_m, x.location,
               x.vehicles_seen_at, x.source_ref
          from contract.sharing_vehicle x
         where st_dwithin(x.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)
           and x.source_ref = any($5::text[]) and ` + permitted("x") + `
         order by x.source_ref, st_astext(x.location), x.observed_at desc) v
 order by distance_m, v.vehicle_id
 limit $4::int`,
		"api_sharing_nearest": `
select s.station_id, s.system_id, s.system_name, s.name, s.capacity, s.is_virtual,
       st_y(s.location), st_x(s.location),
       round(st_distance(s.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography))::int,
       s.status_seen_at, s.source_ref,
       null::int, null::int, null::int, null::bool, null::bool, null::timestamptz, null::timestamptz
  from contract.sharing_station s
 where st_dwithin(s.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)
   and s.source_ref = any($4::text[]) and ` + permitted("s") + `
 order by s.location::geography <-> st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, s.station_id
 limit 1`,
	} {
		legacyReads[name] = sql
	}
}
