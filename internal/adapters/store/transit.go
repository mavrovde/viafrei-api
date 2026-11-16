package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The public-transport repositories (anti-corruption layer).

// stationsPage reads the stations of the given families that answer a
// search: $1 families; $2, $3 the position's latitude and longitude, both
// null for none; $4 the folded text, empty for none; $5 the radius; $6 the
// limit. A station stored with no position is left out: an answer places
// every station it lists. The folded text holds letters and digits only, so
// it carries no LIKE wildcard.
var stationsPage = `
with here as (
  select case when $2::float8 is null then null
              else st_setsrid(st_makepoint($3::float8, $2::float8), 4326)::geography end g)
select s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code,
       coalesce(s.modes, '{}'), s.source_ref,
       case when here.g is null then null else round(st_distance(s.location::geography, here.g))::int end dist
from contract.stop_place s, here
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1)
  and ` + permitted("s") + `
  and ($4::text = '' or s.search_fold like '%' || $4::text || '%')
  and (here.g is null or st_dwithin(s.location::geography, here.g, $5))
order by case when here.g is null and not s.search_fold like $4::text || '%' then 1 else 0 end,
         dist, s.name, s.stop_place_id
limit $6`

// Stations reads the stations that answer q (features.Transit), one more
// than the limit, to tell whether more answer.
func (s *Store) Stations(ctx context.Context, q transit.Query, families []string) ([]transit.Station, bool, error) {
	var lat, lon *float64
	if q.At != nil {
		lat, lon = &q.At.Lat, &q.At.Lon
	}
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Station, error) {
		rows, _ := tx.Query(ctx, stationsPage, families, lat, lon, transit.Fold(q.Text), q.RadiusM, q.Limit+1)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Station, error) {
			var (
				v     transit.Station
				modes []string
			)
			err := r.Scan(&v.ID, &v.Name, &v.Location.Lat, &v.Location.Lon, &v.NationalCode, &v.RegionCode,
				&modes, &v.Family, &v.DistanceM)
			v.Modes = transit.MapModes(modes)
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
