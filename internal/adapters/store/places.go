package store

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// placesPage reads the places that answer a name: $1 the families; $2 the
// folded text; $3 the kind, empty for any; $4, $5 the position's latitude
// and longitude, both null for none; $6 the radius, 0 for none; $7 the
// limit; $8 the stop families; $9 the roads families, which answer with
// their junction places and, for $10 a motorway's number (places.RoadForm,
// empty for none), the motorway itself at its first junction by chainage, or
// a point on it where the road holds no junction (G5). A place is found by
// any of its names (the gazetteer's name and English name), a stop by its
// name (kind station_area), as the platform's place search finds the
// timetable's stops; a whole name before the start of one, the start before
// a part, then the more prominent, then the nearer. The distance is on the
// sphere, as the platform's place search measures it. A place carries its
// official key and its English name, the platform's nameEn, when it differs
// from the name (a stop and a motorway neither). The folded text holds
// letters and digits only, so it carries no LIKE wildcard. Every parameter
// is read through a subquery, so a custom plan sees no more than the
// generic one and the cached generic plan is taken after five custom ones:
// on prod (2026-11-23) the statement settled on it at 10-40 ms a search
// where the view statement re-planned every call at about 230 ms. A text of
// fewer than three letters has no trigram and reads every name (A7: 8.5 s
// on prod, as before).
var placesPage = named("api_places_page", `
with here as (
  select case when (select $4::float8) is null then null
              else st_setsrid(st_makepoint((select $5::float8), (select $4::float8)), 4326)::geography end g),
named_as as (
  select g.id, case when contract_search_fold(g.name) = (select $2::text) then 0
                    when contract_search_fold(g.name) like (select $2::text) || '%' then 1 else 2 end rank
  from gazetteer_places g
  where contract_search_fold(g.name) like '%' || (select $2::text) || '%' and g.geom is not null
    and g.closed_at is null and (g.source_id = any((select $1::text[])::text[]) or g.kind = 'junction' and g.source_id = any((select $9::text[])::text[]))
  union all
  select g.id, case when contract_search_fold(g.name_en) = (select $2::text) then 0
                    when contract_search_fold(g.name_en) like (select $2::text) || '%' then 1 else 2 end
  from gazetteer_places g
  where g.name_en is not null and contract_search_fold(g.name_en) like '%' || (select $2::text) || '%' and g.geom is not null
    and g.closed_at is null and (g.source_id = any((select $1::text[])::text[]) or g.kind = 'junction' and g.source_id = any((select $9::text[])::text[]))),
found as (
  select g.id::text id, g.name,
         case g.kind when 'city' then 'municipality' when 'district' then 'district' when 'admin' then 'state'
                     when 'station' then 'station_area' when 'stop' then 'station_area' when 'island' then 'locality'
                     when 'motorway' then 'motorway' when 'junction' then 'junction' else 'other' end kind,
         st_pointonsurface(g.geom::geometry)::geometry(Point,4326) location, g.admin_code region_key, false stop,
         g.source_id source_ref, h.rank, coalesce(g.rank, 0) prominence, g.admin_code official_key, g.name_en
  from (select n.id, min(n.rank) rank from named_as n group by n.id) h
  join gazetteer_places g on g.id = h.id
  union all
  select s.gtfs_feed_key::text || ':' || s.id, s.name, 'station_area', s.geom::geometry(Point,4326), split_part(s.id, ':', 2), true,
         f.source_feed,
         case when contract_search_fold(s.name) = (select $2::text) then 0 when contract_search_fold(s.name) like (select $2::text) || '%' then 1 else 2 end,
         null, null, null
  from stops s
  join feeds f on f.gtfs_feed_key = s.gtfs_feed_key
  where contract_search_fold(s.name) like '%' || (select $2::text) || '%'
    and s.deleted_at is null and s.geom is not null and s.name is not null
    and (s.location_type = 1 or coalesce(s.location_type::integer, 0) = 0 and s.parent_id is null)
    and f.source_feed = any((select $8::text[])::text[])
  union all
  select r.road, r.road, 'motorway', pt.location, null, false, 'autobahn', 0, null, null, null
  from autobahn_roads r
  cross join lateral (
    select coalesce((select t.geom::geometry(Point,4326) from tmc_locations t
                      where t.road_number = r.road and t.cid = 58 and t.tabcd = 1 and t.tcd = 1
                        and t.road_number ~ '^A[1-9][0-9]{0,2}$' and t.name is not null and t.geom is not null
                        and 'bast_lcl' = any((select $9::text[])::text[]) and `+sourceRuled("'bast_lcl'")+`
                      order by (((t.cid || ':') || t.tabcd) || ':') || t.lcd limit 1),
                    (select st_pointonsurface(c.line) from contract_road_shape(r.road) c(line, length_km, states))) location) pt
  where (select $10::text) <> '' and r.road = (select $10::text) and pt.location is not null and 'autobahn' = any((select $9::text[])::text[])
    and `+sourceRuled("'autobahn'")+`)
select f.id, f.name, f.kind, st_y(f.location), st_x(f.location),
       case when f.stop then (select a.ags from ags_regions a where a.ags = f.region_key)
            when f.region_key is not null then (select a.ags from ags_regions a
              where a.ags = any(array[left(f.region_key, 5), left(f.region_key, 2)]) order by length(a.ags) desc limit 1) end,
       f.source_ref, f.dist, f.official_key,
       case when btrim(f.name_en) <> '' and f.name_en <> f.name then f.name_en end
from (
  select f.id, f.name, f.kind, f.location, f.region_key, f.stop, f.source_ref, f.rank, f.prominence, f.official_key, f.name_en,
         case when here.g is null then null else round(st_distance(f.location::geography, here.g, false))::int end dist
  from found f
  cross join here
  where ((select $3::text) = '' or f.kind = (select $3::text))
    and (here.g is null or (select $6::int) = 0 or st_dwithin(f.location::geography, here.g, (select $6::int), false))
  order by f.rank, f.prominence desc nulls last, dist nulls last, f.name, f.id
  limit (select $7::int)) f
order by f.rank, f.prominence desc nulls last, f.dist nulls last, f.name, f.id`,
	"text[]", "text", "text", "float8", "float8", "int", "int", "text[]", "text[]", "text")

// kreisNames are the names of the districts of the five-digit keys $1 that
// a family of $2 holds, as the platform names a place's Kreis: the open
// district rows of places.KreisFamily.
var kreisNames = named("api_kreis_names", `
select g.admin_code, min(g.name)
from gazetteer_places g
where g.kind = 'district' and g.admin_code = any($1::text[]) and g.closed_at is null and g.geom is not null
  and g.source_id = '`+places.KreisFamily+`' and g.source_id = any($2::text[])
group by g.admin_code`, "text[]", "text[]")

// Places reads the places of families, the stops of stops and the motorway
// places of roads that answer q
// (features.Places), one more than the limit, to tell whether more answer.
func (s *Store) Places(ctx context.Context, q places.Query, families, stops, roads []string) ([]places.Place, bool, error) {
	var lat, lon *float64
	if q.At != nil {
		lat, lon = &q.At.Lat, &q.At.Lon
	}
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Place, error) {
		rows, _ := run(ctx, tx, placesPage, families, transit.Fold(q.Text), string(q.Kind), lat, lon, q.RadiusM, q.Limit+1, stops, roads, places.RoadForm(q.Text))
		ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Place, error) {
			var (
				v    places.Place
				kind string
			)
			err := r.Scan(&v.ID, &v.Name, &kind, &v.Location.Lat, &v.Location.Lon, &v.RegionCode, &v.Family, &v.DistanceM, &v.OfficialKey, &v.NameEn)
			v.Kind = places.MapKind(kind)
			return v, err
		})
		if err != nil {
			return nil, err
		}
		return ps, nameKreise(ctx, tx, ps, families)
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > q.Limit {
		return out[:q.Limit], true, nil
	}
	return out, false, nil
}

// nameKreise sets each place's Kreis (places.KreisOf), reading the district
// names its keys need in one statement, none when no place needs one.
func nameKreise(ctx context.Context, tx pgx.Tx, ps []places.Place, families []string) error {
	var keys []string
	for _, p := range ps {
		if k := places.KreisKey(p.Kind, p.OfficialKey); k != nil && !slices.Contains(keys, *k) {
			keys = append(keys, *k)
		}
	}
	names := map[string]string{}
	if len(keys) > 0 {
		rows, _ := run(ctx, tx, kreisNames, keys, families)
		var key, name string
		if _, err := pgx.ForEachRow(rows, []any{&key, &name}, func() error { names[key] = name; return nil }); err != nil {
			return err
		}
	}
	for i := range ps {
		ps[i].Kreis = places.KreisOf(ps[i].Kind, ps[i].OfficialKey, names)
		if k := places.KreisKey(ps[i].Kind, ps[i].OfficialKey); k != nil && ps[i].Kreis != nil {
			ps[i].KreisFamily = places.KreisFamily
		}
	}
	return nil
}
