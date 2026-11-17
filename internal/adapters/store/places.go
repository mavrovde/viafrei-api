package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// placesPage reads the places that answer a name: $1 the families; $2 the
// folded text; $3 the kind, empty for any; $4, $5 the position's latitude
// and longitude, both null for none; $6 the radius, 0 for none; $7 the
// limit; $8 the stop families. A place is found by any of its names
// (contract.place_alias), a stop by its name (contract.stop_place, kind
// station_area), as the platform's place search finds the timetable's
// stops; a whole name before the start of one, the start before a part,
// then the more prominent, then the nearer. The distance is on the sphere,
// as the platform's place search measures it. The folded text holds
// letters and digits only, so it carries no LIKE wildcard.
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
  select p.place_id id, p.name, p.place_kind kind, p.location, p.region_code, p.source_ref, hit.rank, p.prominence
  from contract.place p
  join hit on hit.place_id = p.place_id
  where p.withdrawn_at is null
    and p.location is not null
    and p.source_ref = any($1)
    and ` + permitted("p") + `
  union all
  select s.stop_place_id, s.name, 'station_area', s.location, s.region_code, s.source_ref,
         case when s.search_fold = $2 then 0 when s.search_fold like $2 || '%' then 1 else 2 end, null
  from contract.stop_place s
  where s.search_fold like '%' || $2 || '%'
    and s.withdrawn_at is null
    and s.location is not null
    and s.source_ref = any($8)
    and ` + permitted("s") + `)
select f.id, f.name, f.kind, st_y(f.location), st_x(f.location), f.region_code, f.source_ref,
       case when here.g is null then null else round(st_distance(f.location::geography, here.g, false))::int end dist
from found f
cross join here
where ($3::text = '' or f.kind = $3::text)
  and (here.g is null or $6::int = 0 or st_dwithin(f.location::geography, here.g, $6::int, false))
order by f.rank, f.prominence desc nulls last, dist nulls last, f.name, f.id
limit $7`

// Places reads the places of families and the stops of stops that answer q
// (features.Places), one more than the limit, to tell whether more answer.
func (s *Store) Places(ctx context.Context, q places.Query, families, stops []string) ([]places.Place, bool, error) {
	var lat, lon *float64
	if q.At != nil {
		lat, lon = &q.At.Lat, &q.At.Lon
	}
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Place, error) {
		rows, _ := tx.Query(ctx, placesPage, families, transit.Fold(q.Text), string(q.Kind), lat, lon, q.RadiusM, q.Limit+1, stops)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Place, error) {
			var (
				v    places.Place
				kind string
			)
			err := r.Scan(&v.ID, &v.Name, &kind, &v.Location.Lat, &v.Location.Lon, &v.RegionCode, &v.Family, &v.DistanceM)
			v.Kind = places.MapKind(kind)
			return v, err
		})
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > q.Limit {
		return out[:q.Limit], true, nil
	}
	return out, false, nil
}
