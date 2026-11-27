package store

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The place resolver's reads (places.ResolveReads): today's platform
// statements on the gazetteer, each parameter cast at its use, and the rows
// of the families the key reads kept in the final select only, so every
// score is the one the platform's statement computes.

// gazetteerCols are the columns of a resolver row, as the platform's
// candidate statements select them.
const gazetteerCols = `g.id::text as id, g.kind, g.name, g.name_norm, g.name_en, g.admin_code, g.population, g.road, g.rank, g.source_id, g.external_id,
         st_y(g.geom::geometry) as lat,
         st_x(g.geom::geometry) as lon`

// placeCandidates is the platform's CANDIDATE_SQL: $1 the folded text, $2
// its umlaut-expanded form, $3 the transliteration penalty, $4 the limit, $5
// the Gemeinde rule, $6/$7 a kind and source the final rows are narrowed to,
// $8/$9 the prefix-qualified patterns of $1 and $2; $10 the families.
//
// The platform's rows, read so that no part reads a whole table or index
// and no candidate is tested twice (#291; "bamberg" on prod, EXPLAIN
// ANALYZE 2026-11-27: 455 ms custom, 689 ms generic, 29 503 buffers):
//   - hits: one arm per spelling and rule, so each bitmap heap scan
//     rechecks its own condition only. The platform's one OR rechecked every
//     arm on every trigram candidate (36 000 for "bamberg", whose trigrams
//     every "...berg" shares), the same similarity twice when $2 is $1. An
//     arm of $2 runs only when $2 is not $1 (whose rows it would repeat): a
//     one-time filter. The suffix rule ("<q> %") is the byte range
//     [q || ' ', q || '!') on the text_pattern_ops btree, which a LIKE with a
//     parameter pattern never uses in the generic plan; a fold is letters,
//     digits and single spaces, so the range holds exactly the rows LIKE
//     matched.
//   - the prefix-qualified namesakes: one range probe per pattern on that
//     btree (a lateral kept apart by offset 0), where the join of the
//     patterns to the table was planned as a scan of 390 000 rows.
//   - the answer rows: each fold's rows by its name (a lateral kept apart),
//     where the generic plan nested every fold over every row read (214 000
//     rows filtered for "bamberg").
//
//nolint:misspell // materialized is the SQL keyword
var placeCandidates = named("api_place_candidates", `
with exact as materialized (
  select coalesce(max(population), 0) as population
    from gazetteer_places
   where closed_at is null and name_norm in ($1::text, $2::text) and admin_code ~ '^[0-9]{8}$'
),
hits as (
  select id from gazetteer_places where closed_at is null and name_norm % $1::text
  union
  select id from gazetteer_places where $2::text <> $1::text and closed_at is null and name_norm % $2::text
  union
  select id from gazetteer_places
   where $5::boolean and closed_at is null
     and name_norm ~>=~ ($1::text || ' ') and name_norm ~<~ ($1::text || '!')
     and population > 0 and admin_code ~ '^[0-9]{8}$'
  union
  select id from gazetteer_places
   where $5::boolean and $2::text <> $1::text and closed_at is null
     and name_norm ~>=~ ($2::text || ' ') and name_norm ~<~ ($2::text || '!')
     and population > 0 and admin_code ~ '^[0-9]{8}$'
  union
  select id from gazetteer_places where closed_at is null and name_en is not null and lower(name_en) % $1::text
  union
  select id from gazetteer_places where $2::text <> $1::text and closed_at is null and name_en is not null and lower(name_en) % $2::text
  union
  select g.id
    from (select distinct pat from unnest($8::text[] || $9::text[]) as u(pat)) as p
    cross join lateral (
      select g.id from gazetteer_places g
       where g.closed_at is null
         and g.name_norm ~>=~ replace(p.pat, '%', '')
         and g.name_norm ~<~ (case when right(p.pat, 1) = '%' then left(p.pat, -2) || '!' else p.pat || chr(1) end)
         and g.population > 0 and g.population >= (select population from exact) and g.admin_code ~ '^[0-9]{8}$'
      offset 0) g
   where $5::boolean
),
scored as (
  select id, name_norm,
         greatest(
           similarity(name_norm, $1::text),
           coalesce(similarity(lower(name_en), $1::text), 0),
           similarity(name_norm, $2::text) - $3::double precision,
           coalesce(similarity(lower(name_en), $2::text), 0) - $3::double precision
         ) as score,
         case when $5::boolean and population > 0 and admin_code ~ '^[0-9]{8}$' then
           case when name_norm like $1::text || ' %' then 1::double precision
                when name_norm like $2::text || ' %' then 1::double precision - $3::double precision
                when population < (select population from exact) then 0::double precision
                when name_norm like any($8::text[]) then 1::double precision
                when name_norm like any($9::text[]) then 1::double precision - $3::double precision
                else 0::double precision end
         else 0::double precision end as qualified
    from gazetteer_places
   where id = any (array(select id from hits))
),
folds as materialized (
  select name_norm, max(score) as score, max(qualified) as qualified from scored group by name_norm
)
select `+gazetteerCols+`,
       greatest(f.score, case when g.kind in ('station', 'stop') then 0::double precision else f.qualified end) as score
  from folds f
  cross join lateral (
    select g.id, g.kind, g.name, g.name_norm, g.name_en, g.admin_code, g.population, g.road, g.rank, g.source_id, g.external_id, g.geom
      from gazetteer_places g
     where g.closed_at is null and g.name_norm = f.name_norm
       and ($6::text is null or (g.kind = $6::text and g.source_id = $7::text))
       and g.source_id = any((select $10::text[])::text[])
     offset 0) g
 order by score desc, g.rank desc, g.name asc, g.source_id asc, g.external_id asc
 limit $4::integer`, "text", "text", "double precision", "integer", "boolean", "text", "text", "text[]", "text[]", "text[]")

// placeRoadRows is the platform's ROAD_ONLY_SQL: the rows of road $1, by
// rank, $2 of them, of the families $3.
var placeRoadRows = named("api_place_road_rows", `
select `+gazetteerCols+`, 1::double precision as score
  from gazetteer_places g
 where g.closed_at is null and g.road = $1::text
   and g.source_id = any((select $3::text[])::text[])
 order by g.rank desc, g.name asc
 limit $2::integer`, "text", "integer", "text[]")

// ResolveReads are the place resolver's reads of the families.
func (s *Store) ResolveReads(families []string) places.ResolveReads {
	return resolveReads{s: s, families: families}
}

type resolveReads struct {
	s        *Store
	families []string
}

func (r resolveReads) CandidateRows(ctx context.Context, c places.CandidateRead) ([]places.GazetteerRow, error) {
	return r.rows(ctx, placeCandidates, c.Folded, c.Expanded, c.Penalty, c.Limit, c.Qualified, c.OnlyKind, c.OnlySource, c.Patterns, c.ExpandedPatterns, r.families)
}

func (r resolveReads) RoadRows(ctx context.Context, road string, limit int) ([]places.GazetteerRow, error) {
	return r.rows(ctx, placeRoadRows, road, limit, r.families)
}

func (r resolveReads) rows(ctx context.Context, s stmt, args ...any) ([]places.GazetteerRow, error) {
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) ([]places.GazetteerRow, error) {
		rows, _ := run(ctx, tx, s, args...)
		return pgx.CollectRows(rows, scanGazetteerRow)
	})
}

func scanGazetteerRow(r pgx.CollectableRow) (places.GazetteerRow, error) {
	var g places.GazetteerRow
	err := r.Scan(&g.ID, &g.Kind, &g.Name, &g.NameNorm, &g.NameEn, &g.AdminCode, &g.Population, &g.Road, &g.Rank, &g.SourceID, &g.ExternalID, &g.Lat, &g.Lon, &g.Score)
	return g, err
}

// placeLandsNear is the platform's LAND_NEAR_POINTS: for each point ($1 the
// longitudes, $2 the latitudes, $4 the row ids) the Land key of the
// Gemeinde its row is part of, else of the keyed stop or station within
// 1 500 m, else of the keyed Gemeinde within $3 metres; by ordinality.
var placeLandsNear = named("api_place_lands_near", `
select p.i::int as i,
       coalesce(
         (select left(o.part_of, 2)
            from gazetteer_places o
           where o.id = (case when p.id ~ '^[0-9]{1,18}$' then p.id::bigint end)
             and o.closed_at is null and o.part_of is not null),
         (select left(k.admin_code, 2)
            from gazetteer_places k
           where k.closed_at is null and k.kind in ('stop', 'station')
             and k.admin_code ~ '^[0-9]{8}$'
             and ST_DWithin(k.geom, ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography, 1500)
           order by k.geom <-> ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography
           limit 1),
         (select left(g.admin_code, 2)
            from gazetteer_places g
           where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$'
             and ST_DWithin(g.geom, ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography, $3::double precision)
           order by g.geom <-> ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography
           limit 1)) as land
  from unnest($1::float8[], $2::float8[], $4::text[]) with ordinality as p(lon, lat, id, i)`, "float8[]", "float8[]", "double precision", "text[]")

// placeKreisNames is the platform's KREIS_NAMES_SQL: the open BKG Kreis rows
// of the keys $1, of the families $2.
var placeKreisNames = named("api_place_kreis_names", `
select g.admin_code, g.name
  from gazetteer_places g
 where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'district' and g.admin_code = any($1::text[])
   and g.source_id = any((select $2::text[])::text[])`, "text[]", "text[]")

// placeShownStops is the platform's SHOWN_NAME_STOPS_SQL: the stops whose
// search name ends with the fold $1, $2 of them, of the families $3.
var placeShownStops = named("api_place_shown_stops", `
select `+gazetteerCols+`, 1::double precision as score
  from gazetteer_places g
 where g.closed_at is null and g.kind = 'stop' and g.name_norm like '% ' || $1::text
   and g.source_id = any((select $3::text[])::text[])
 order by g.rank desc, g.name asc, g.source_id asc, g.external_id asc
 limit $2::integer`, "text", "integer", "text[]")

func (r resolveReads) LandsNear(ctx context.Context, lons, lats []float64, withinM float64, ids []string) (map[int]string, error) {
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) (map[int]string, error) {
		rows, _ := run(ctx, tx, placeLandsNear, lons, lats, withinM, ids)
		out := map[int]string{}
		var i int
		var land *string
		_, err := pgx.ForEachRow(rows, []any{&i, &land}, func() error {
			if land != nil {
				out[i] = *land
			}
			return nil
		})
		return out, err
	})
}

func (r resolveReads) KreisNames(ctx context.Context, keys []string) (map[string]string, error) {
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) (map[string]string, error) {
		rows, _ := run(ctx, tx, placeKreisNames, keys, r.families)
		out := map[string]string{}
		var key, name string
		_, err := pgx.ForEachRow(rows, []any{&key, &name}, func() error { out[key] = name; return nil })
		return out, err
	})
}

func (r resolveReads) ShownStops(ctx context.Context, fold string, limit int) ([]places.GazetteerRow, error) {
	return r.rows(ctx, placeShownStops, fold, limit, r.families)
}

// placeJunctionsNamed is the platform's JUNCTIONS_OSM_NAMED: OpenStreetMap's
// motorway junction nodes whose name (with or without a junction word in
// front) scores $2 or more against the core $1, $3 of them, of the families
// $4.
var placeJunctionsNamed = named("api_place_junctions_named", `
select o.osm_type || '/' || o.osm_id::text as id, o.name, o.name_norm,
       ST_Y(o.geom::geometry) as lat, ST_X(o.geom::geometry) as lon,
       s.score
from osm_pois o
cross join lateral (
  select greatest(similarity(o.name_norm, $1::text),
                  similarity(regexp_replace(o.name_norm, '^(autobahnkreuz|autobahndreieck|autobahnanschlussstelle|anschlussstelle|kreuz|dreieck|ak|ad|as) ', ''), $1::text)) as score
) s
where o.closed_at is null
  and o.category = 'highway'
  and o.category_value = 'motorway_junction'
  and o.source_id = any((select $4::text[])::text[])
  and s.score >= $2::double precision
order by s.score desc, o.name asc, o.id asc
limit $3::integer`, "text", "double precision", "integer", "text[]")

func (r resolveReads) JunctionsNamed(ctx context.Context, core string, floor float64, limit int) ([]places.NamedJunction, error) {
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) ([]places.NamedJunction, error) {
		rows, _ := run(ctx, tx, placeJunctionsNamed, core, floor, limit, r.families)
		return pgx.CollectRows(rows, func(row pgx.CollectableRow) (places.NamedJunction, error) {
			var j places.NamedJunction
			var norm *string
			err := row.Scan(&j.ID, &j.Name, &norm, &j.Lat, &j.Lon, &j.Score)
			return j, err
		})
	})
}

func (r resolveReads) LCLNear(ctx context.Context, lons, lats []float64) ([][]places.LCLPoint, error) {
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) ([][]places.LCLPoint, error) {
		rows, _ := run(ctx, tx, junctionRoads, lons, lats, r.families)
		near := make([][]places.LCLPoint, len(lons))
		var (
			ord int
			p   places.LCLPoint
		)
		_, err := pgx.ForEachRow(rows, []any{&ord, &p.Road, &p.Name, &p.DistanceM}, func() error {
			near[ord-1] = append(near[ord-1], p)
			return nil
		})
		return near, err
	})
}

// poiStatements are the POI statements of the place resolver, by the name
// the domain reads them under.
var poiStatements = map[string]stmt{
	places.PoiAnywhere:        poiAnywhere,
	places.PoiByPlace:         poiByPlace,
	places.PoiByPlaceFuzzy:    poiByPlaceFuzzy,
	places.PoiNear:            poiNear,
	places.PoiNearFuzzy:       poiNearFuzzy,
	places.PoiCategoryByPlace: poiCategoryByPlace,
	places.PoiCategoryNear:    poiCategoryNear,
	places.PoiCategoryNearest: poiCategoryNearest,
}

func (r resolveReads) PoiRows(ctx context.Context, read places.PoiRead) ([]places.PoiHit, error) {
	s := poiStatements[read.Statement]
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) ([]places.PoiHit, error) {
		rows, _ := run(ctx, tx, s, append(slices.Clone(read.Args), r.families)...)
		return pgx.CollectRows(rows, func(row pgx.CollectableRow) (places.PoiHit, error) {
			var h places.PoiHit
			var extract *string
			err := row.Scan(&h.ID, &h.Name, &h.Category, &h.CategoryValue, &h.Brand, &h.Operator, &h.Street, &h.HouseNumber, &h.Postcode, &h.Place, &h.Land,
				&h.Lat, &h.Lon, &extract, &h.Score, &h.Tier, &h.DistanceM, &h.Vicinity)
			if extract != nil {
				h.ExtractDate = *extract
			}
			return h, err
		})
	})
}

func (r resolveReads) PlaceWords(ctx context.Context, words []string) ([]string, bool, error) {
	type read struct {
		words  []string
		loaded bool
	}
	got, err := ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) (read, error) {
		var out read
		rows, _ := run(ctx, tx, poiPlaceWords, words)
		_, err := pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (struct{}, error) {
			return struct{}{}, row.Scan(&out.words, &out.loaded)
		})
		return out, err
	})
	return got.words, got.loaded, err
}

// placeGazetteerLoaded is the platform's PLACE_GAZETTEER_LOADED_SQL.
var placeGazetteerLoaded = named("api_place_gazetteer_loaded", `select exists (select 1 from gazetteer_places) as loaded`)

var addressStatements = map[string]stmt{
	places.AddrByPostcode:       addrByPostcode,
	places.AddrStreetByPostcode: addrStreetByPostcode,
	places.AddrByPlace:          addrByPlace,
	places.AddrByPlacePrefix:    addrByPlacePrefix,
	places.AddrStreetByPlace:    addrStreetByPlace,
}

func (r resolveReads) AddressRows(ctx context.Context, name string, args []any) ([]places.AddressHit, error) {
	s := addressStatements[name]
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) ([]places.AddressHit, error) {
		rows, _ := run(ctx, tx, s, append(slices.Clone(args), r.families)...)
		return pgx.CollectRows(rows, func(row pgx.CollectableRow) (places.AddressHit, error) {
			var h places.AddressHit
			var osmType, street, number, extract, numberNorm *string
			dest := []any{&h.ID, &osmType, &street, &number, &h.Postcode, &h.Place, &h.Land, &h.Lat, &h.Lon, &extract}
			if len(row.FieldDescriptions()) == 12 {
				dest = append(dest, &numberNorm)
			}
			err := row.Scan(append(dest, &h.LiteralScore)...)
			h.Street, h.HouseNumber, h.ExtractDate, h.HouseNumberNorm = textOf(street), textOf(number), textOf(extract), textOf(numberNorm)
			return h, err
		})
	})
}

var streetStatements = map[string]stmt{places.StreetByPlace: streetByPlace, places.StreetByPostcode: streetByPostcode}

func (r resolveReads) StreetRows(ctx context.Context, name string, args []any) ([]places.StreetHit, error) {
	s := streetStatements[name]
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) ([]places.StreetHit, error) {
		rows, _ := run(ctx, tx, s, append(slices.Clone(args), r.families)...)
		return pgx.CollectRows(rows, func(row pgx.CollectableRow) (places.StreetHit, error) {
			var h places.StreetHit
			var street, extract *string
			err := row.Scan(&street, &h.Postcode, &h.Place, &h.Addresses, &h.Lat, &h.Lon, &extract)
			h.Street, h.ExtractDate = textOf(street), textOf(extract)
			return h, err
		})
	})
}

func (r resolveReads) IsLoaded(ctx context.Context, what string) (bool, error) {
	s := addrLoaded
	if what == places.LoadedGazetteer {
		s = placeGazetteerLoaded
	}
	return ReadValue(ctx, r.s, func(ctx context.Context, tx pgx.Tx) (bool, error) {
		rows, _ := run(ctx, tx, s)
		return pgx.CollectExactlyOneRow(rows, pgx.RowTo[bool])
	})
}

func textOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
