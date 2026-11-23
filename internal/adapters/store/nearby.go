package store

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// needPlaces is each need's statement: the nearest places of interest of
// its kinds (places.NeedKinds) with a name within $3 m of ($1, $2), of the
// families $4, at most $5, a distance tie broken by id; a post office named
// after a parcel service is no post office, as the platform's. Each need's
// kinds are its statement's own literal list, so its plan reads the need's
// own partial GiST (osm_pois_need_<need>_geom).
var needPlaces = func() map[string]stmt {
	out := map[string]stmt{}
	for need, kinds := range places.NeedKinds {
		out[need] = needStatement(need, kinds)
	}
	return out
}()

// needStatement is the statement of the need with its kinds: the nearest
// $5 through the GiST's nearest-neighbour scan (needNearest) bound the
// distance, and the places within it are ordered by distance and id.
//
//nolint:misspell // materialized is the SQL keyword
func needStatement(need string, kinds []string) stmt {
	lits := make([]string, len(kinds))
	for i, k := range kinds {
		lits[i] = "'" + strings.ReplaceAll(k, "'", "''") + "'"
	}
	where := `p.closed_at is null and p.geom is not null and p.name is not null
  and p.category || '=' || p.category_value = any(array[` + strings.Join(lits, ", ") + `])
  and p.source_id = any((select $4::text[])::text[])
  and st_dwithin(p.geom, ` + atPosition + `, (select $3::float8), false)`
	if need == "money" {
		where += `
  and not (p.category_value = 'post_office' and p.name ~* '\m(dpd|hermes|gls|ups|fedex)\M')`
	}
	//nolint:misspell // materialized is the SQL keyword
	return named("api_nearby_need_"+need, `
with k as materialized (
  select p.geom <-> `+atPosition+` d from osm_pois p
  where `+where+`
  order by p.geom <-> `+atPosition+` limit (select $5::int8))
select 'osm:' || p.osm_type || p.osm_id, p.name, p.category || '=' || p.category_value, p.street, p.house_number,
       st_y(p.geom::geometry), st_x(p.geom::geometry), round(st_distance(p.geom, `+atPosition+`))::int, p.source_id
from osm_pois p
where `+where+`
  and st_dwithin(p.geom, `+atPosition+`, (select max(k.d) from k), false)
  and p.geom <-> `+atPosition+` <= (select max(k.d) from k)
order by p.geom <-> `+atPosition+`, 'osm:' || p.osm_type || p.osm_id
limit (select $5::int8)`, "float8", "float8", "float8", "text[]", "int8")
}

// junctionsNear reads the LCL's German motorway points with a name within $3
// metres of ($1, $2), of the families $4, nearest first by the whole metre,
// a tie broken by id, at most $5.
var junctionsNear = named("api_nearby_junctions", `
select t.cid || ':' || t.tabcd || ':' || t.lcd, t.road_number, t.junction_no, t.name,
       st_y(t.geom::geometry), st_x(t.geom::geometry),
       round(st_distance(t.geom::geometry(Point,4326)::geography, `+atPosition+`))::int, 'bast_lcl'
from tmc_locations t
where t.cid = 58 and t.tabcd = 1 and t.tcd = 1 and t.road_number ~ '^A[1-9][0-9]{0,2}$'
  and t.geom is not null and t.name is not null
  and 'bast_lcl' = any((select $4::text[])::text[])
  and st_dwithin(t.geom::geometry(Point,4326)::geography, `+atPosition+`, (select $3::float8))
order by 7, 1
limit (select $5::int8)`, "float8", "float8", "float8", "text[]", "int8")

// junctionOverfetch is how many more junctions are read than shown, so the
// rest areas the platform drops leave enough.
const junctionOverfetch = 4

// NeedPlaces reads each need's places within places.NeedMaxM
// (features.Nearby).
func (s *Store) NeedPlaces(ctx context.Context, q places.NearbyQuery, families []string) (map[string][]places.NeedPlace, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (map[string][]places.NeedPlace, error) {
		out := map[string][]places.NeedPlace{}
		for _, n := range q.Needs {
			st, ok := needPlaces[n]
			if !ok {
				// A need the platform does not know has no kinds, so no places.
				out[n] = []places.NeedPlace{}
				continue
			}
			rows, _ := run(ctx, tx, st, q.At.Lon, q.At.Lat, places.NeedMaxM, families, places.NeedRows)
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
		rows, _ := run(ctx, tx, junctionsNear, q.At.Lon, q.At.Lat, q.RadiusM, families, q.Limit*junctionOverfetch)
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
