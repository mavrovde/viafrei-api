package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// needPlaces reads, for the need $3 of the kinds $4, the nearest
// places.NeedRows places of interest within places.NeedMaxM of ($1, $2), of
// the families $5; a post office named after a parcel service is no post
// office, as the platform's.
var needPlaces = `select p.poi_id, p.name, p.category || '=' || p.category_value, p.street, p.house_number,
       st_y(p.location), st_x(p.location),
       round(st_distance(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int, p.source_ref
from contract.poi p
where p.category || '=' || p.category_value = any($4) and p.name is not null
  and not ($3 = 'money' and p.category_value = 'post_office' and p.name ~* '\m(dpd|hermes|gls|ups|fedex)\M')
  and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $6)
  and p.source_ref = any($5) and ` + permitted("p") + `
order by p.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, p.poi_id
limit $7`

// junctionsNear reads the junctions within $3 metres of ($1, $2), of the
// families $4, nearest first, at most $5.
var junctionsNear = `select j.junction_id, j.road_ref, j.number, j.name, st_y(j.location), st_x(j.location),
       round(st_distance(j.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int, j.source_ref
from contract.junction j
where j.name is not null and st_dwithin(j.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
  and j.source_ref = any($4) and ` + permitted("j") + `
order by 7, j.junction_id
limit $5`

// junctionOverfetch is how many more junctions are read than shown, so the
// rest areas the platform drops leave enough.
const junctionOverfetch = 4

// NeedPlaces reads each need's places within places.NeedMaxM
// (features.Nearby).
func (s *Store) NeedPlaces(ctx context.Context, q places.NearbyQuery, families []string) (map[string][]places.NeedPlace, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (map[string][]places.NeedPlace, error) {
		out := map[string][]places.NeedPlace{}
		for _, n := range q.Needs {
			rows, _ := tx.Query(ctx, needPlaces, q.At.Lon, q.At.Lat, n, places.NeedKinds[n], families, places.NeedMaxM, places.NeedRows)
			ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.NeedPlace, error) {
				var p places.NeedPlace
				err := r.Scan(&p.ID, &p.Name, &p.Kind, &p.Street, &p.HouseNumber, &p.Location.Lat, &p.Location.Lon, &p.DistanceM, &p.Family)
				return p, err
			})
			if err != nil {
				return nil, err
			}
			out[n] = ps
		}
		return out, nil
	})
}

// Junctions reads the junctions within q's radius, rest areas left out, at
// most q.Limit (features.Nearby).
func (s *Store) Junctions(ctx context.Context, q places.NearbyQuery, families []string) ([]places.Junction, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]places.Junction, error) {
		rows, _ := tx.Query(ctx, junctionsNear, q.At.Lon, q.At.Lat, q.RadiusM, families, q.Limit*junctionOverfetch)
		js, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Junction, error) {
			var j places.Junction
			err := r.Scan(&j.ID, &j.Road, &j.Number, &j.Name, &j.Location.Lat, &j.Location.Lon, &j.DistanceM, &j.Family)
			return j, err
		})
		out := []places.Junction{}
		for _, j := range js {
			if !places.IsRestAreaName(j.Name) && len(out) < q.Limit {
				out = append(out, j)
			}
		}
		return out, err
	})
}
