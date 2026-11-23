//go:build integration

package store_test

import "github.com/mavrovde/viafrei-api/internal/domain/energy"

// The parity cases of the hydrogen, bay, lorry, POI and train-board
// statements (namedCases).

func init() {
	for name, sql := range map[string]string{
		"api_sharing_stations_near": `with fam as (select array_agg(distinct source_feed) f from public.gbfs_stations),
pt as (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.gbfs_stations where geom is not null union all values (13.405::float8, 52.52::float8)),
t as (select max(observed_at) + interval '1 minute' t from public.gbfs_station_status union all select now())
select pt.lon, pt.lat, v.r, v.l, t.t, v.fs from pt, fam, t,
  lateral (values (1000::float8, 80, fam.f), (20000, 2, fam.f), (1000, 80, '{}'::text[])) v(r, l, fs)`,
		"api_sharing_vehicles_near": `with fam as (select array_agg(distinct source_feed) f from public.gbfs_vehicles),
pt as (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.gbfs_vehicles where geom is not null union all values (13.405::float8, 52.52::float8))
select pt.lon, pt.lat, v.r, v.l, v.fs from pt, fam,
  lateral (values (1000::float8, 200, fam.f), (20000, 2, fam.f), (1000, 200, '{}'::text[])) v(r, l, fs)`,
		"api_sharing_nearest": `with fam as (select array_agg(distinct source_feed) f from public.gbfs_stations)
select pt.lon, pt.lat, 100000::float8, v.fs from (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.gbfs_stations where geom is not null
  union all values (13.405::float8, 52.52::float8), (6.0, 47.0)) pt, fam, lateral (values (fam.f), ('{}'::text[])) v(fs)`,
		"api_speed_official": speedCases,
		"api_speed_osm":      speedCases,
		"api_register":       `select`,
		"api_sources": `select f from (select array_agg(source_ref) f from public.contract_sources union all select '{}'::text[]
       union all select array_agg(source_ref) from (select source_ref from public.contract_sources order by source_ref limit 3) x) v`,
		"api_terms_texts":    `select f from (select array_agg(terms_gate) f from public.source_terms union all select '{}'::text[] union all select '{nope}') v`,
		"api_charging_near":  chargingCases(false),
		"api_charging_by_id": chargingCases(true),
		"api_charging_in_radius": `with fam as (select array_agg(distinct source_feed) f from public.charging_sites)
select pt.lat, pt.lon, v.fs, v.r from (select st_y(geom::geometry) lat, st_x(geom::geometry) lon from public.charging_sites where geom is not null
  union all values (52.52::float8, 13.405::float8)) pt, fam, lateral (values (fam.f, 5000::float8), (fam.f, 50000), ('{}'::text[], 5000)) v(fs, r)`,
		"api_place_by_id": `with fam as (select array_agg(distinct source_id) f from public.gazetteer_places)
select i.id, v.fs from (select id::text id from public.gazetteer_places union all values ('0'), ('x1'), ('99999999999999999999'), ('')) i, fam,
  lateral (values (fam.f), ('{}'::text[])) v(fs)`,
		"api_hydrogen_near": `with fam as (select array_agg(distinct source_feed) f from public.hydrogen_stations),
pt as (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.hydrogen_stations where geom is not null
       union all values (13.405::float8, 52.52::float8))
select pt.lon, pt.lat, v.r, v.fs, w.v, v.l from pt, fam,
  lateral (values (50000::float8, fam.f, 10), (500000, fam.f, 2), (50000, '{}'::text[], 10)) v(r, fs, l),
  (values (null::text), ('Car'), ('Truck')) w(v)`,
		"api_hydrogen_nearest": `with fam as (select array_agg(distinct source_feed) f from public.hydrogen_stations)
select pt.lon, pt.lat, v.fs, w.v from (values (13.405::float8, 52.52::float8), (11.575, 48.137), (6.0, 47.0)) pt(lon, lat), fam,
  lateral (values (fam.f), ('{}'::text[])) v(fs), (values (null::text), ('Car'), ('Truck')) w(v)`,
		"api_bays_near": `with fam as (select array_agg(distinct source_feed) f from public.street_bays),
pt as (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.street_bays where geom is not null
       union all values (13.405::float8, 52.52::float8)),
k as (select distinct kind k from public.street_bays),
t as (select max(message_at) + interval '1 minute' t from public.street_bay_state union all select now())
select pt.lon, pt.lat, v.r, k.k, t.t, 900, v.l, v.fs, w.free from pt, fam, k, t,
  lateral (values (500::float8, 50::bigint, fam.f), (5000, 2, fam.f), (500, 50, '{}'::text[])) v(r, l, fs), (values (false), (true)) w(free)`,
		"api_lorry_rules": `with fam as (select array_agg(distinct f) f from (select source_feed f from public.lorry_long_network_live union select source_feed from public.lorry_sevas_live
       union select source_feed from public.lorry_ivm_live union select source_feed from public.lorry_hb_live union select source_feed from public.lorry_city_live) x),
pt as (select st_x(st_pointonsurface(geom::geometry)) lon, st_y(st_pointonsurface(geom::geometry)) lat from public.lorry_long_network_live
       union select st_x(st_pointonsurface(geom::geometry)), st_y(st_pointonsurface(geom::geometry)) from public.lorry_city_live
       union select null, null),
rd as (select distinct upper(replace(road, ' ', '')) r from public.lorry_long_network_live where road is not null union select '')
select v.fs, rd.r, l.l, pt.lon, pt.lat, 5000::float8, 50 from fam, pt, rd,
  (values ('long_lorry'), ('other')) l(l), lateral (values (fam.f), ('{}'::text[])) v(fs)`,
		"api_poi_match": `with fam as (select array_agg(distinct source_id) f from public.osm_pois),
q as (select distinct left(name_norm, 4) q, place_norm t, st_x(geom::geometry) lon, st_y(geom::geometry) lat, category c from public.osm_pois where name_norm is not null)
select q.q, v.t, v.lon, v.lat, v.r, v.c, v.fs, 51 from q, fam,
  lateral (values (null::text, null::float8, null::float8, 0::float8, null::text, fam.f), (q.t, null, null, 0, null, fam.f),
                  (null, q.lon, q.lat, 2000, null, fam.f), (null, q.lon, q.lat, 2000, q.c, fam.f), (null, null, null, 0, null, '{}'::text[])) v(t, lon, lat, r, c, fs)`,
		"api_train_board": `with fam as (select array_agg(distinct source_id) f from domains.train_board_departures)
select e.eva, v.fs from (select distinct eva from domains.train_board_departures union all select '0000000') e, fam,
  lateral (values (fam.f), ('{}'::text[])) v(fs)`,
	} {
		namedCases[name] = casesOf(sql)
	}
}

// speedCases are a point at every camera and in Berlin, under two radii,
// the day of the newest announced check and today, every family and none.
const speedCases = `with fam as (select array_agg(distinct f) f from (select source_feed f from public.speed_enforcement union select source_feed from public.hh_cameras
       union select source_id from public.osm_speed_cameras) x),
pt as (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.speed_enforcement where geom is not null
       union select st_x(geom::geometry), st_y(geom::geometry) from public.osm_speed_cameras union values (13.405::float8, 52.52::float8)),
d as (select coalesce(max(valid_from), current_date) d from public.speed_enforcement union select current_date)
select pt.lon, pt.lat, v.r, d.d, v.fs, v.l from pt, fam, d,
  lateral (values (2000::float8, fam.f, 50), (20000, fam.f, 3), (2000, '{}'::text[], 50)) v(r, fs, l)`

// chargingCases are a point at every site and in Berlin (by id: every
// site's id and one no site holds), now and before and after every report,
// under the filters each alone (access, power, plug, power ceiling,
// operator, terms), every family and none.
func chargingCases(byID bool) string {
	pt := `select st_y(geom::geometry) lat, st_x(geom::geometry) lon, '' id from public.charging_sites where geom is not null
       union all values (52.52::float8, 13.405::float8, '')`
	if byID {
		pt = `select 0::float8 lat, 0::float8 lon, source_feed || ':' || site_id id from public.charging_sites union all values (0, 0, 'nope:x')`
	}
	return `with fam as (select array_agg(distinct source_feed) f from public.charging_sites),
op as (select coalesce((select ` + energy.OperatorKeySQL("operator") + ` from public.charging_sites where operator is not null limit 1), 'x') k),
pt as (` + pt + `),
t as (select now() t union select max(observed_at) - interval '1 second' from public.charging_point_status
      union select min(observed_at) from public.charging_point_status)
select pt.lat, pt.lon, v.fs, v.r, v.acc, v.kw, v.l, t.t, v.plug, v.mx, v.op, v.pf, v.terms, pt.id
from pt, fam, op, t, lateral (values
  (fam.f, 5000::float8, null::text[], 0::float8, 11::bigint, null::text, 0::float8, null::text, false, null::text[]),
  (fam.f, 50000, null, 0, 2, null, 0, null, false, null),
  (fam.f, 5000, '{public}', 50, 11, null, 0, null, true, null),
  (fam.f, 5000, '{restricted}', 0, 11, null, 0, null, false, null),
  (fam.f, 5000, null, 0, 11, 'ccs2', 0, null, true, null),
  (fam.f, 5000, null, 0, 11, 'type2', 22, null, true, null),
  (fam.f, 5000, null, 0, 11, null, 0, op.k, false, null),
  (fam.f, 5000, null, 0, 11, null, 0, '` + energy.NoOperatorKey + `', false, null),
  (fam.f, 5000, null, 0, 11, null, 0, null, true, '{card}'),
  (fam.f, 5000, null, 0, 11, null, 0, null, true, '{free,open247}'),
  ('{}'::text[], 5000, null, 0, 11, null, 0, null, false, null)) v(fs, r, acc, kw, l, plug, mx, op, pf, terms)`
}
