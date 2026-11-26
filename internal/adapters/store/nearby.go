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

// junctionsOSM reads OpenStreetMap's motorway junction nodes within $3
// metres of ($1, $2), of the families $4, nearest first, at most $5, as
// today's JUNCTIONS_OSM_NEAR.
var junctionsOSM = named("api_nearby_junctions_osm", `
select 'osm:' || o.osm_type || o.osm_id, coalesce(o.name, ''), st_y(o.geom::geometry), st_x(o.geom::geometry),
       round(st_distance(o.geom, `+atPosition+`))::int, o.source_id
from osm_pois o
where o.closed_at is null and o.category = 'highway' and o.category_value = 'motorway_junction'
  and o.source_id = any((select $4::text[])::text[])
  and st_dwithin(o.geom, `+atPosition+`, (select $3::float8))
order by st_distance(o.geom, `+atPosition+`), o.id
limit (select $5::int8)`, "float8", "float8", "float8", "text[]", "int8")

// junctionRoads reads, for each node ($1 longitudes, $2 latitudes), the
// five nearest LCL motorway points within places.LCLReachM, of the family
// bast_lcl where $3 holds it, as today's JUNCTION_ROAD_LCL.
var junctionRoads = named("api_junction_roads_lcl", `
select u.ord::int, p.road, p.name, p.d
from unnest((select $1::float8[]), (select $2::float8[])) with ordinality u(lon, lat, ord)
cross join lateral (
  select m.road_number road, m.name, st_distance(m.geom::geometry(Point,4326)::geography, st_setsrid(st_makepoint(u.lon, u.lat), 4326)::geography) d
  from tmc_locations m
  where m.cid = 58 and m.tabcd = 1 and m.road_number ~ '^A[1-9][0-9]{0,2}$' and m.geom is not null
    and 'bast_lcl' = any((select $3::text[])::text[])
    and st_dwithin(m.geom::geometry(Point,4326)::geography, st_setsrid(st_makepoint(u.lon, u.lat), 4326)::geography, 1500)
  order by m.geom::geometry(Point,4326)::geography <-> st_setsrid(st_makepoint(u.lon, u.lat), 4326)::geography
  limit 5) p
order by u.ord, p.d`, "float8[]", "float8[]", "text[]")

// junctionsGazetteer reads the gazetteer's junction points within $3
// metres of ($1, $2), of the families $4, nearest first, at most $5, as
// today's findPlacesAround of kind junction.
var junctionsGazetteer = named("api_nearby_junctions_gazetteer", `
select g.id::text, g.name, g.road, st_y(g.geom::geometry), st_x(g.geom::geometry),
       round(st_distance(g.geom, `+atPosition+`))::int, g.source_id
from gazetteer_places g
where g.closed_at is null and g.geom is not null and g.kind = 'junction'
  and g.source_id = any((select $4::text[])::text[])
  and st_dwithin(g.geom, `+atPosition+`, (select $3::float8))
order by g.geom <-> `+atPosition+`
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

// Junctions reads the junctions within q's radius, as today's
// nearestJunctions: OpenStreetMap's nodes, rest areas left out, same-named
// ones within places.SameJunctionM folded, at most q.Limit, each with the
// road the LCL places it on; with none, the gazetteer's junction points;
// with neither, none from OpenStreetMap (features.Nearby).
func (s *Store) Junctions(ctx context.Context, q places.NearbyQuery, families []string) (places.Junctions, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (places.Junctions, error) {
		return junctionsNearTx(ctx, tx, q, families)
	})
}

// junctionsNearTx reads the junctions near q.At in tx (Junctions).
func junctionsNearTx(ctx context.Context, tx pgx.Tx, q places.NearbyQuery, families []string) (places.Junctions, error) {
	out := places.Junctions{From: places.JunctionsFromOSM, Rows: []places.Junction{}}
	rows, _ := run(ctx, tx, junctionsOSM, q.At.Lon, q.At.Lat, float64(q.RadiusM), families, q.Limit*junctionOverfetch)
	js, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Junction, error) {
		var j places.Junction
		err := r.Scan(&j.ID, &j.Name, &j.Location.Lat, &j.Location.Lon, &j.DistanceM, &j.Family)
		return j, err
	})
	if err != nil {
		return out, err
	}
	var nodes []places.Junction
	for _, j := range js {
		if !places.IsRestAreaName(j.Name) {
			nodes = append(nodes, j)
		}
	}
	if len(nodes) > 0 {
		nodes = places.FoldSameNamed(nodes, places.SameJunctionM)
		out.Rows = nodes[:min(len(nodes), q.Limit)]
		return out, junctionRoadsOf(ctx, tx, out.Rows, families)
	}
	rows, _ = run(ctx, tx, junctionsGazetteer, q.At.Lon, q.At.Lat, float64(q.RadiusM), families, q.Limit*junctionOverfetch)
	gs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Junction, error) {
		var j places.Junction
		err := r.Scan(&j.ID, &j.Name, &j.Road, &j.Location.Lat, &j.Location.Lon, &j.DistanceM, &j.Family)
		return j, err
	})
	for _, j := range gs {
		if !places.IsRestAreaName(j.Name) && len(out.Rows) < q.Limit {
			out.Rows = append(out.Rows, j)
		}
	}
	if len(out.Rows) > 0 {
		out.From = places.JunctionsFromGazetteer
	}
	return out, err
}

// junctionRoadsOf sets the road each OpenStreetMap node lies on, read from
// the LCL points near it (places.JunctionRoad).
func junctionRoadsOf(ctx context.Context, tx pgx.Tx, js []places.Junction, families []string) error {
	lons, lats := make([]float64, len(js)), make([]float64, len(js))
	for i, j := range js {
		lons[i], lats[i] = j.Location.Lon, j.Location.Lat
	}
	rows, _ := run(ctx, tx, junctionRoads, lons, lats, families)
	near := make([][]places.LCLPoint, len(js))
	var (
		ord int
		p   places.LCLPoint
	)
	_, err := pgx.ForEachRow(rows, []any{&ord, &p.Road, &p.Name, &p.DistanceM}, func() error {
		near[ord-1] = append(near[ord-1], p)
		return nil
	})
	for i := range js {
		if r := places.JunctionRoad(js[i].Name, near[i]); r != nil {
			js[i].Road, js[i].RoadBy = r, "bast_lcl"
		}
	}
	return err
}
