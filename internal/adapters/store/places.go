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
// any of its names
// (contract.place_alias), a stop by its name (contract.stop_place, kind
// station_area), as the platform's place search finds the timetable's
// stops; a whole name before the start of one, the start before a part,
// then the more prominent, then the nearer. The distance is on the sphere,
// as the platform's place search measures it. A place carries its official
// key and its English name, the platform's nameEn: its English alias when
// it differs from the name (a stop and a motorway neither), read for the
// page's rows only: on prod, read for every match it took the search past
// 5 s where it took 2.8 s without. The folded text holds letters and digits
// only, so it carries no LIKE wildcard. It is sent as written, not opaque:
// each optional filter is decided by its argument, which a generic plan
// cannot see (stationsPage, the same shape, ran past 5 s under it).
var placesPage = `
with here as (
  select case when $4::float8 is null then null
              else st_setsrid(st_makepoint($5::float8, $4::float8), 4326)::geography end g),
hit as (
  select a.place_id, min(case when a.search_fold = $2 then 0 when a.search_fold like $2 || '%' then 1 else 2 end) rank
  from contract.place_alias a
  where a.search_fold like '%' || $2 || '%' and ` + permitted("a") + `
  group by a.place_id),
found as (
  select p.place_id id, p.name, p.place_kind kind, p.location, p.region_code, p.source_ref, hit.rank, p.prominence, p.official_key,
         true aliased
  from contract.place p
  join hit on hit.place_id = p.place_id
  where p.withdrawn_at is null
    and p.location is not null
    and (p.source_ref = any($1) or (p.place_kind = 'junction' and p.source_ref = any($9)))
    and ` + permitted("p") + `
  union all
  select s.stop_place_id, s.name, 'station_area', s.location, s.region_code, s.source_ref,
         case when s.search_fold = $2 then 0 when s.search_fold like $2 || '%' then 1 else 2 end, null, null, false
  from contract.stop_place s
  where s.search_fold like '%' || $2 || '%'
    and s.withdrawn_at is null
    and s.location is not null
    and s.source_ref = any($8)
    and ` + permitted("s") + `
  union all
  select r.road_ref, r.road_ref, 'motorway', pt.location, null, r.source_ref, 0, null, null, false
  from contract.road r
  cross join lateral (
    select coalesce((select j.location from contract.junction j
                      where j.road_ref = r.road_ref and j.location is not null and j.source_ref = any($9) and ` + permitted("j") + `
                      order by j.chainage_km nulls last, j.junction_id limit 1),
                    st_pointonsurface(r.geometry)) location) pt
  where $10::text <> ''
    and r.road_ref = $10::text
    and pt.location is not null
    and r.source_ref = any($9)
    and ` + permitted("r") + `)
select f.id, f.name, f.kind, st_y(f.location), st_x(f.location), f.region_code, f.source_ref, f.dist, f.official_key,
       case when f.aliased then (select min(e.label) from contract.place_alias e
         where e.place_id = f.id and e.lang = 'en' and btrim(e.label) <> '' and e.label <> f.name and ` + permitted("e") + `) end
from (
  select f.*, case when here.g is null then null else round(st_distance(f.location::geography, here.g, false))::int end dist
  from found f
  cross join here
  where ($3::text = '' or f.kind = $3::text)
    and (here.g is null or $6::int = 0 or st_dwithin(f.location::geography, here.g, $6::int, false))
  order by f.rank, f.prominence desc nulls last, dist nulls last, f.name, f.id
  limit $7) f
order by f.rank, f.prominence desc nulls last, f.dist nulls last, f.name, f.id`

// kreisNames are the names of the districts of the five-digit keys $1 that
// a family of $2 holds, as the platform names a place's Kreis: the open
// district rows of places.KreisFamily.
var kreisNames = `
select p.official_key, min(p.name)
from contract.place p
where p.place_kind = 'district' and p.official_key = any($1) and p.withdrawn_at is null
  and p.source_ref = '` + places.KreisFamily + `' and p.source_ref = any($2) and ` + permitted("p") + `
group by p.official_key`

// Places reads the places of families, the stops of stops and the motorway
// places of roads that answer q
// (features.Places), one more than the limit, to tell whether more answer.
func (s *Store) Places(ctx context.Context, q places.Query, families, stops, roads []string) ([]places.Place, bool, error) {
	var lat, lon *float64
	if q.At != nil {
		lat, lon = &q.At.Lat, &q.At.Lon
	}
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Place, error) {
		rows, _ := tx.Query(ctx, placesPage, families, transit.Fold(q.Text), string(q.Kind), lat, lon, q.RadiusM, q.Limit+1, stops, roads, places.RoadForm(q.Text))
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
		rows, _ := tx.Query(ctx, kreisNames, keys, families)
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
