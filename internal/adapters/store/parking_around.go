package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// nearbyAmenityCase maps an OpenStreetMap tag to the platform's amenity word.
const nearbyAmenityCase = `case when o.category || '=' || o.category_value in ('amenity=toilets') then 'toilet' when o.category || '=' || o.category_value in ('amenity=shower') then 'shower' when o.category || '=' || o.category_value in ('amenity=restaurant') then 'restaurant' when o.category || '=' || o.category_value in ('amenity=fast_food') then 'snack' when o.category || '=' || o.category_value in ('amenity=cafe') then 'cafe' when o.category || '=' || o.category_value in ('shop=convenience', 'shop=kiosk', 'shop=supermarket') then 'shop' when o.category || '=' || o.category_value in ('amenity=drinking_water') then 'drinking_water' end`

// parkingNearbyOSM reads, for each site ($1 longitudes, $2 latitudes, $3
// radii), the nearest OpenStreetMap place of each amenity within its radius,
// of the families $4, as today's PARKING_NEARBY_OSM.
var parkingNearbyOSM = named("api_parking_nearby_osm", `
select s.ord::int as site, x.amenity, x.name, x.metres, x.lat, x.lon, x.extract_date
  from unnest((select $1::float8[]), (select $2::float8[]), (select $3::float8[])) with ordinality as s(lon, lat, radius, ord)
  cross join lateral (
    select distinct on (k.amenity) k.amenity, k.name, k.metres, k.lat, k.lon, k.extract_date
      from (
        select `+nearbyAmenityCase+` as amenity, o.name,
               round(st_distance(o.geom, st_setsrid(st_makepoint(s.lon, s.lat), 4326)::geography))::int as metres,
               st_y(o.geom::geometry) as lat, st_x(o.geom::geometry) as lon, o.extract_date::text as extract_date
          from osm_pois o
         where o.closed_at is null and (o.category || '=' || o.category_value) in ('amenity=toilets', 'amenity=shower', 'amenity=restaurant', 'amenity=fast_food', 'amenity=cafe', 'shop=convenience', 'shop=kiosk', 'shop=supermarket', 'amenity=drinking_water')
           and o.source_id = any((select $4::text[])::text[])
           and st_dwithin(o.geom, st_setsrid(st_makepoint(s.lon, s.lat), 4326)::geography, s.radius)
      ) k
     order by k.amenity, k.metres, k.name
  ) x
 order by s.ord, x.metres, x.amenity`, "float8[]", "float8[]", "float8[]", "text[]")

// parkingOSMNear reads OpenStreetMap's car parks within $3 m of ($1, $2)
// named after $4 (any name when null) and not after $6, open to the public,
// of the families $7, nearest first, at most $5, as today's PARKING_OSM_NEAR.
var parkingOSMNear = named("api_parking_osm_near", `
select o.osm_type || '/' || o.osm_id::text as id, o.name, o.operator, o.street, o.house_number, o.postcode, o.place,
       st_y(o.geom::geometry) as lat, st_x(o.geom::geometry) as lon,
       o.extract_date::text as extract_date,
       st_distance(o.geom, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography) as distance_m
from osm_pois o
where o.closed_at is null
  and o.category = 'amenity'
  and o.category_value = 'parking'
  and ((select $4::text) is null or o.name ~* (select $4::text))
  and o.name !~* (select $6::text)
  and (cardinality(array_remove(array(select lower(btrim(v)) from unnest(string_to_array(o.access, ';')) v), '')) = 0 or not (array_remove(array(select lower(btrim(v)) from unnest(string_to_array(o.access, ';')) v), '') <@ array['private', 'no', 'customers', 'permit', 'residents', 'delivery', 'agricultural', 'forestry', 'military', 'emergency', 'employees', 'staff']::text[]))
  and o.source_id = any((select $7::text[])::text[])
  and st_dwithin(o.geom, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography, (select $3::float8))
order by distance_m asc, o.id asc
limit (select $5::int8)`, "float8", "float8", "float8", "text", "int8", "text", "text[]")

// roadContextNear reads, for each kind ($3, with its radius $4, count $5,
// preferred variant $7, one per name $8, parts counted $9 and flag $10), the
// open, answerable road context rows of the families $11 within the radius
// of ($1, $2), whose period holds the Berlin day of $6 and whose feed was
// checked within three of its cycles, as today's ROAD_CONTEXT_NEAR over
// road_context_live.
var roadContextNear = named("api_parking_road_context", `
select k.kind, x.source_feed, x.feature_id, x.variant, x.name, x.ref, x.detail_text, x.props,
       st_y(x.at) as lat, st_x(x.at) as lon, x.distance_m, x.last_seen_at, x.total, x.variants, x.parts_in, x.flagged
  from unnest((select $3::text[]), (select $4::float8[]), (select $5::int[]), (select $7::text[]), (select $8::boolean[]), (select $9::boolean[]), (select $10::text[]))
       with ordinality as k(kind, radius_m, lim, prefer, one_per_name, count_parts, flag, ord)
  cross join lateral (
    with m as (
      select w.source_feed, w.feature_id, w.variant, w.name, w.ref, w.detail_text, w.props, w.last_seen_at,
             st_closestpoint(w.geom, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)) as at,
             st_distance(w.geom::geography, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography) as distance_m,
             case when k.count_parts
                  then (select count(*)::int from st_dump(w.geom) d where st_dwithin(d.geom::geography, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography, k.radius_m))
                  else 1 end as parts_in
        from road_context_features w
       where w.closed_at is null and w.answerable
         and w.kind = k.kind
         and w.source_feed = any((select $11::text[])::text[])
         and w.geom && st_expand(st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326), k.radius_m / 111320.0 / greatest(cos(radians((select $2::float8))), 0.2))
         and st_dwithin(w.geom::geography, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography, k.radius_m)
         and (w.props->>'valid_from' is null or w.props->>'valid_from' <= to_char((select $6::timestamptz) at time zone 'Europe/Berlin', 'YYYY-MM-DD'))
         and (w.props->>'valid_to' is null or w.props->>'valid_to' >= to_char((select $6::timestamptz) at time zone 'Europe/Berlin', 'YYYY-MM-DD'))
         and exists (select 1 from feed_checks c
                      where c.feed_key = w.source_feed
                        and (c.checked_at > (select $6::timestamptz) - make_interval(secs => 3 * coalesce(c.cadence_s, 86400)) or (c.paused and c.checked_at is not null)))
    ),
    n as (
      select distinct on (case when k.one_per_name then m.name else m.source_feed || ':' || m.feature_id end)
             m.source_feed, m.feature_id, m.variant, m.name, m.ref, m.detail_text, m.props, m.last_seen_at, m.at, m.distance_m, m.parts_in
        from m
       order by case when k.one_per_name then m.name else m.source_feed || ':' || m.feature_id end,
                (m.variant is not distinct from k.prefer) desc, m.distance_m, m.props->>'valid_to' desc nulls first, m.source_feed, m.feature_id
    )
    select n.source_feed, n.feature_id, n.variant, n.name, n.ref, n.detail_text, n.props, n.last_seen_at, n.at, n.distance_m, n.parts_in,
           (select sum(m.parts_in)::int from m) as total,
           (select jsonb_object_agg(v.variant, v.n) from (select variant, sum(parts_in)::int as n from m group by variant) v) as variants,
           case when k.flag is null then null else (select count(*)::int from m where m.props->>k.flag = 'true') end as flagged
      from n
     order by (n.variant is not distinct from k.prefer) desc, n.distance_m, n.source_feed, n.feature_id
     limit k.lim
  ) x
 order by k.ord, (x.variant is not distinct from k.prefer) desc, x.distance_m, x.source_feed, x.feature_id`,
	"float8", "float8", "text[]", "float8[]", "int[]", "timestamptz", "text[]", "boolean[]", "boolean[]", "text[]", "text[]")

// contextArgs are roadContextNear's arguments for the rules.
func contextArgs(q parking.AroundQuery) []any {
	n := len(parking.ContextRules)
	kinds, prefer, flag := make([]string, n), make([]*string, n), make([]*string, n)
	radii, limits := make([]float64, n), make([]int, n)
	onePerName, countParts := make([]bool, n), make([]bool, n)
	for i, r := range parking.ContextRules {
		kinds[i], radii[i], limits[i], prefer[i], onePerName[i], countParts[i], flag[i] = r.Kind, r.RadiusM, r.Limit, r.Prefer, r.OnePerName, r.CountParts, r.Flag
	}
	return []any{q.At.Lon, q.At.Lat, kinds, radii, limits, q.Now, prefer, onePerName, countParts, flag, q.ContextFamilies}
}

// ParkingAround reads what q asks beside a parking list, in one snapshot:
// each read is issued only when asked, and a failed read fails the whole.
func (s *Store) ParkingAround(ctx context.Context, q parking.AroundQuery) (parking.AroundRead, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (parking.AroundRead, error) {
		var out parking.AroundRead
		var errs []error
		if len(q.Sites) > 0 && len(q.OSMFamilies) > 0 {
			lons, lats := make([]float64, len(q.Sites)), make([]float64, len(q.Sites))
			for i, p := range q.Sites {
				lons[i], lats[i] = p.Lon, p.Lat
			}
			rows, _ := run(ctx, tx, parkingNearbyOSM, lons, lats, q.Radii, q.OSMFamilies)
			var err error
			out.Nearby, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (parking.NearbyRow, error) {
				var v parking.NearbyRow
				err := r.Scan(&v.Site, &v.Amenity, &v.Name, &v.Metres, &v.At.Lat, &v.At.Lon, &v.ExtractDate)
				return v, err
			})
			errs = append(errs, err)
		}
		if q.CarParks && len(q.OSMFamilies) > 0 {
			rows, _ := run(ctx, tx, parkingOSMNear, q.At.Lon, q.At.Lat, q.CarParkRadiusM, q.Pattern, q.CarParkFetch, parking.OSMRestrictedNamePattern, q.OSMFamilies)
			var err error
			out.CarParks, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (parking.OSMCarPark, error) {
				var v parking.OSMCarPark
				err := r.Scan(&v.ID, &v.Name, &v.Operator, &v.Street, &v.HouseNumber, &v.Postcode, &v.Place, &v.At.Lat, &v.At.Lon, &v.ExtractDate, &v.DistanceM)
				return v, err
			})
			errs = append(errs, err)
		}
		if q.Context && len(q.ContextFamilies) > 0 {
			rows, _ := run(ctx, tx, roadContextNear, contextArgs(q)...)
			var err error
			out.Context, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (parking.ContextRow, error) {
				var (
					v    parking.ContextRow
					seen time.Time
				)
				err := r.Scan(&v.Kind, &v.Family, &v.ID, &v.Variant, &v.Name, &v.Ref, &v.Detail, &v.Facts, &v.At.Lat, &v.At.Lon, &v.DistanceM, &seen, &v.Total, &v.Variants, &v.PartsIn, &v.Flagged)
				v.LastSeenAt = seen.UTC()
				return v, err
			})
			errs = append(errs, err)
		}
		return out, errors.Join(errs...)
	})
}
