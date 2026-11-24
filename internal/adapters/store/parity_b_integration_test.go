//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The parity cases of the Set B statements (namedCases): each is built by a
// statement over the seeded database, whose every row is one argument set.

func init() {
	for name, sql := range map[string]string{
		"api_fuel_near": `with fam as (select array_agg(distinct coalesce(source_feed, source)) f from public.fuel_stations),
pt as (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.fuel_stations where geom is not null
       union all values (13.405::float8, 52.52::float8))
select pt.lon, pt.lat, v.r, v.fs, v.lim from pt, fam,
  lateral (values (fam.f, 5000::float8, 200::bigint), (fam.f, 25000, 3), (array['tankerkoenig_hist'], 25000, 200), ('{}'::text[], 5000, 200)) v(fs, r, lim)`,
		"api_fuel_coverage": `with fam as (select array_agg(distinct coalesce(source_feed, source)) f from public.fuel_stations),
pt as (select st_x(geom::geometry) lon, st_y(geom::geometry) lat from public.fuel_stations where geom is not null
       union all values (13.405::float8, 52.52::float8)),
g as (select distinct product g from public.fuel_prices union all select null union all select 'hydrogen'),
t as (select max(observed_at) + interval '1 minute' t from public.fuel_prices union all select now())
select pt.lon, pt.lat, 5000::float8, fam.f, g.g, 25000::float8, v.s, t.t from pt, fam, g, t, (values (600), (86400 * 30)) v(s)`,
		"api_fuel_by_id": `with fam as (select array_agg(distinct coalesce(source_feed, source)) f from public.fuel_stations),
t as (select max(observed_at) + interval '1 minute' t from public.fuel_prices union all select now())
select x.i, v.fs, w.s, t.t from (select id i from public.fuel_stations union all select 'no-such-station') x, fam, t,
  lateral (values (fam.f), (array['tankerkoenig_hist']), ('{}'::text[])) v(fs), (values (600), (86400 * 30)) w(s)`,
		"api_coverage_kinds": coverageFamilies,
		"api_coverage_lands": coverageFamilies,
		"api_coverage_addresses": `select f from (select array_agg(distinct source_id) f from public.osm_addresses) x
union all select '{}'::text[] union all select array['osm_none']`,
		"api_address_exact": `with fam as (select array_agg(distinct source_id) f from public.osm_addresses),
a as (select street_norm s, house_number_norm h, postcode p, place_norm t from public.osm_addresses where street_norm is not null)
select a.s, a.h, v.p, v.t, fam.f, v.l from a, fam,
  lateral (values (a.p, null::text, 10), (null, a.t, 10), (a.p, a.t, 1), ('00000', a.t, 10), (null, null, 10), (a.p, 'nirgendwo', 10)) v(p, t, l)
union all select 'x', '1', '10115', null, '{}'::text[], 10`,
		"api_weather_warnings_page": weatherPageCases,
		"api_weather_warnings_here": `select f, t, lat, lon from (` + weatherPoints + `) x`,
		"api_weather_cell_at":       `select distinct f, lat, lon from (` + weatherPoints + `) x`,
		"api_weather_cell_named": `with fam as (select array_agg(distinct source_feed order by source_feed) f from public.weather_warnings)
select fam.f, n from fam, (select distinct area_desc n from public.weather_warnings where area_desc is not null union all select 'Nirgendwo') a
union all select '{}'::text[], (select min(area_desc) from public.weather_warnings)`,
		"api_weather_cell_warnings": `with fam as (select array_agg(distinct source_feed order by source_feed) f from public.weather_warnings),
k as (select warncell_id k from public.weather_warnings union select area_desc from public.weather_warnings where area_desc is not null),
t as (select valid_from + interval '1 minute' t from public.weather_warnings union select now() union select now() + interval '400 days')
select fam.f, k.k, t.t, l from fam, k, t, (values (0), (3)) v(l)`,
		"api_rule_topic": `select topic_code, l from (select topic_code from public.driving_rule_topics union all select 'no-such-topic') t, (values ('de'), ('en')) v(l)`,
		"api_zones_at": `with fam as (select array_agg(distinct source_feed order by source_feed) f from public.lez_zones_live),
pt as (select st_x(st_pointonsurface(geom)) lon, st_y(st_pointonsurface(geom)) lat from public.lez_zones_live
       union all select st_x(st_pointonsurface(geom)) + 0.05, st_y(st_pointonsurface(geom)) from public.lez_zones_live
       union all values (13.405::float8, 52.52::float8), (6.0, 47.0))
select f, r, t, lon, lat, e from fam, pt, (values (5000::float8, 500::float8), (0, 0), (100000, 100000)) v(r, e),
  (select c.checked_at + interval '1 minute' t from public.feed_checks c where c.feed_key in (select source_feed from public.lez_zones_live)
   union select now() union select now() + interval '400 days') w
union all select '{}'::text[], 5000::float8, now(), lon, lat, 500::float8 from pt`,
		"api_zone_entries": `select array[c] from (select distinct unnest(place_codes) c from public.lez_list_entries) x
union all select '{}'::text[] union all select array['00000000']
union all select array_agg(c) from (select distinct unnest(place_codes) c from public.lez_list_entries) y`,
		"api_zone_entry_named": `select entry_key from public.lez_list_entries
union all select unnest(aliases) from public.lez_list_entries union all select 'nirgendwo'`,
	} {
		namedCases[name] = casesOf(sql)
	}
}

// coverageFamilies are every gazetteer family, one, and none.
const coverageFamilies = `select f from (select array_agg(distinct source_id) f from public.gazetteer_places) x
union all select array[min(source_id)] from public.gazetteer_places union all select '{}'::text[]`

// weatherPoints are a point inside each seeded warning's area, at the start
// of the warning and a minute after, a day before the first and after the
// last, and at now; and a point in the sea.
const weatherPoints = `with fam as (select array_agg(distinct source_feed order by source_feed) f from public.weather_warnings),
pt as (select st_y(st_pointonsurface(area::geometry)) lat, st_x(st_pointonsurface(area::geometry)) lon from public.weather_warnings where area is not null
       union values (54.9::float8, 3.0::float8)),
t as (select valid_from t from public.weather_warnings union select valid_from + interval '1 minute' from public.weather_warnings
      union select min(valid_from) - interval '1 day' from public.weather_warnings
      union select max(coalesce(valid_to, valid_from)) + interval '1 day' from public.weather_warnings union select now())
select fam.f, t.t, pt.lat, pt.lon from fam, pt, t`

// weatherPageCases are weatherPoints under a floor, the hazards kept and
// whether the unmapped ones are, a limit and a look-ahead; and once with no
// family.
const weatherPageCases = `with every as (select array['thunderstorm','wind','flood','snow','ice','rain','fog','heat','frost','uv'] e)
select f, t, lat, lon, lvl, hz, other, every.e, lim, t + la
from (` + weatherPoints + `) x, every,
  (values (0, array['thunderstorm','wind','flood','snow','ice','rain','fog','heat','frost','uv'], true, 50::bigint, interval '0'),
          (2, array['wind','ice'], false, 2::bigint, interval '1 day'),
          (1, '{}'::text[], true, 1::bigint, interval '6 hours')) v(lvl, hz, other, lim, la)
union all select '{}'::text[], now(), 52.52::float8, 13.405::float8, 0, '{}'::text[], true, every.e, 5::bigint, now() from every`

// casesOf reads the argument sets of sql's rows.
func casesOf(sql string) func(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	return func(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
		t.Helper()
		rows, err := c.Query(ctx, sql)
		if err != nil {
			t.Fatal(err)
		}
		out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([]any, error) {
			v, err := r.Values()
			for i, x := range v {
				if a, ok := x.([]any); ok {
					strs := make([]string, len(a))
					for j, e := range a {
						strs[j], _ = e.(string)
					}
					v[i] = strs
				}
			}
			return v, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) == 0 {
			t.Fatal("no case")
		}
		return out
	}
}
