package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// atPosition is the position ($1 lon, $2 lat) on the sphere, read through
// subqueries so a custom plan sees no more than the generic one.
const atPosition = `st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography`

// statedM is a distance from the position on the sphere, as the platform
// measures and states it: metres under 50 m, else to 100 m.
func statedM(col string) string {
	d := `st_distance(` + col + `::geography, ` + atPosition + `, false)::numeric`
	return `(case when ` + d + ` < 50 then round(` + d + `) else round(` + d + ` / 100) * 100 end)::int`
}

// placeRegion is the region code of the gazetteer place g: the longest of
// its key's district and Land that the region register holds.
const placeRegion = `(select a.ags from ags_regions a
   where a.ags = any(array[left(g.admin_code, 5), left(g.admin_code, 2)]) order by length(a.ags) desc limit 1)`

// nearestPlace reads the nearest open place of the kind $3 within $4 metres
// of ($1, $2), of the families $5, by its point on the sphere, a tie broken
// by id. Each place kind (municipality, district or state: the kinds a
// position is named by) is a branch of its own with the gazetteer's kind as
// a literal, so each is planned on that kind's own rows; only the branch of
// $3 runs. The gazetteer's own GiST keeps the candidates within the reach (a
// place's point lies on its shape, so no place within the reach is left
// out); the reach and the order are then the point's, as the platform states
// a place's distance.
var nearestPlace = named("api_location_place",
	placeOfKind("municipality", "city", true)+"\nunion all\n"+placeOfKind("district", "district", false)+"\nunion all\n"+
		placeOfKind("state", "admin", false),
	"float8", "float8", "text", "float8", "text[]")

// placeOfKind is nearestPlace's branch of the place kind, the gazetteer's
// kind gkind. A kind with many places (spatial) is read through the
// gazetteer's GiST; one with few (the 800 districts, the 70 Länder) is read
// by its kind, which a GiST read of the reach would only slow.
func placeOfKind(kind, gkind string, spatial bool) string {
	within := ""
	if spatial {
		within = `
  and st_dwithin(g.geom, ` + atPosition + `, (select $4::float8))`
	}
	return `(select g.id::text, g.name, (select $3::text), st_y(l.p), st_x(l.p), ` + placeRegion + `, g.source_id,
       ` + statedM("l.p") + `, g.admin_code
from gazetteer_places g
cross join lateral (select st_pointonsurface(g.geom::geometry)::geometry(Point,4326) p) l
where (select $3::text) = '` + kind + `' and g.kind = '` + gkind + `' and g.closed_at is null and g.geom is not null` + within + `
  and st_dwithin(l.p::geography, ` + atPosition + `, (select $4::float8))
  and g.source_id = any((select $5::text[])::text[])
order by l.p::geography <-> ` + atPosition + `, g.id::text
limit 1)`
}

// addressSectors reads, for each wedge of $1 (WKT), whether it holds an open
// address of the families $2.
var addressSectors = named("api_location_sectors", `
select w.i::int, exists (select 1 from osm_addresses a
  where a.closed_at is null and a.geom is not null and a.source_id = any((select $2::text[])::text[])
    and st_intersects(a.geom, st_geogfromtext(w.wkt)))
from unnest((select $1::text[])::text[]) with ordinality as w(wkt, i)
order by w.i`, "text[]", "text[]")

// nearestOpenAddress is the sphere distance from the position to the open
// address of the families $n nearest it: the GiST's nearest-neighbour scan,
// which a second sort key would turn into a sort of every address.
func nearestOpenAddress(n string) string {
	return `(select a.geom <-> ` + atPosition + ` from osm_addresses a
   where a.closed_at is null and a.geom is not null and a.source_id = any((select $` + n + `::text[])::text[])
   order by a.geom <-> ` + atPosition + ` limit 1)`
}

// addressesAtNearest are the open addresses of the families $n at the
// distance of the nearest one, read from a metre around it through the GiST:
// the nearest address and those that tie with it.
func addressesAtNearest(n string) string {
	return `a.closed_at is null and a.geom is not null and a.source_id = any((select $` + n + `::text[])::text[])
  and st_dwithin(a.geom, ` + atPosition + `, ` + nearestOpenAddress(n) + ` + 1, false)
  and a.geom <-> ` + atPosition + ` = ` + nearestOpenAddress(n)
}

// nearestAddressFamily reads the family of the open address of the families
// $3 nearest to ($1, $2) on the sphere, however far, a tie broken by id.
var nearestAddressFamily = named("api_location_abroad_family", `
select a.source_id from osm_addresses a
where `+addressesAtNearest("3")+`
order by 'osm:' || a.osm_type || a.osm_id
limit 1`, "float8", "float8", "text[]")

// stateOf reads the Land whose key is the first two digits of the region
// code $1, of the families $2: the open gazetteer row of kind admin keyed by
// those two digits, never a Regierungsbezirk (a three-digit key under it),
// through the key's index.
var stateOf = named("api_location_state", `
select g.id::text, g.name, 'state', st_y(l.p), st_x(l.p), `+placeRegion+`, g.source_id, null::int, g.admin_code
from gazetteer_places g
cross join lateral (select st_pointonsurface(g.geom::geometry)::geometry(Point,4326) p) l
where g.admin_code = left((select $1::text), 2) and g.kind = 'admin' and g.closed_at is null and g.geom is not null
  and g.source_id = any((select $2::text[])::text[])
order by g.id::text
limit 1`, "text", "text[]")

// nearestAddress reads the open address of the families $4 nearest ($1, $2)
// on the sphere, a tie broken by id, when it lies within $3 metres. Within
// the reach the nearest is the GiST's nearest neighbour, so the read never
// sorts the addresses within the reach.
var nearestAddress = named("api_location_address", `
select 'osm:' || a.osm_type || a.osm_id, a.street, a.house_number, a.postcode, a.place, a.land,
       st_y(a.geom::geometry), st_x(a.geom::geometry), a.extract_date, a.source_id,
       round(st_distance(a.geom, `+atPosition+`))::int
from osm_addresses a
where `+addressesAtNearest("4")+`
  and st_dwithin(a.geom, `+atPosition+`, (select $3::float8))
order by 'osm:' || a.osm_type || a.osm_id
limit 1`, "float8", "float8", "float8", "text[]")

func scanPlace(r pgx.CollectableRow) (places.Place, error) {
	var v places.Place
	var kind string
	err := r.Scan(&v.ID, &v.Name, &kind, &v.Location.Lat, &v.Location.Lon, &v.RegionCode, &v.Family, &v.DistanceM, &v.OfficialKey)
	v.Kind = places.MapKind(kind)
	return v, err
}

func first[T any](v []T) *T {
	if len(v) == 0 {
		return nil
	}
	return &v[0]
}

// Location reads what lies at q.At (features.Location): the nearest
// address, municipality and district within the platform's reaches, the
// Land of the district (with none, the nearest Land), and whether the
// position may lie outside Germany.
func (s *Store) Location(ctx context.Context, q places.LocationQuery, families []string) (places.Location, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (places.Location, error) {
		var out places.Location
		rows, _ := run(ctx, tx, nearestAddress, q.At.Lon, q.At.Lat, places.AddressReachM, families)
		addr, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Address, error) {
			var v places.Address
			err := r.Scan(&v.ID, &v.Street, &v.HouseNumber, &v.Postcode, &v.Place, &v.Land, &v.Location.Lat, &v.Location.Lon, &v.DataAsOf, &v.Family, &v.DistanceM)
			return v, err
		})
		if err != nil {
			return out, err
		}
		out.Address = first(addr)
		for _, k := range []struct {
			kind  string
			reach int
			into  **places.Place
		}{{"municipality", places.MunicipalityReachM, &out.Municipality}, {"district", places.DistrictReachM, &out.District}} {
			rows, _ = run(ctx, tx, nearestPlace, q.At.Lon, q.At.Lat, k.kind, k.reach, families)
			ps, err := pgx.CollectRows(rows, scanPlace)
			if err != nil {
				return out, err
			}
			*k.into = first(ps)
		}
		if out.District != nil && out.District.RegionCode != nil {
			rows, _ = run(ctx, tx, stateOf, *out.District.RegionCode, families)
		} else {
			rows, _ = run(ctx, tx, nearestPlace, q.At.Lon, q.At.Lat, "state", places.StateReachM, families)
		}
		ps, err := pgx.CollectRows(rows, scanPlace)
		if err != nil {
			return out, err
		}
		out.State = first(ps)
		if out.Address != nil && *out.Address.DistanceM <= places.AbroadNearestM {
			out.Gemeinde, err = gemeinde(ctx, tx, q, out.Address, families)
			return out, err
		}
		rows, _ = run(ctx, tx, addressSectors, places.SectorWedges(q.At, places.AbroadScanM, places.AbroadSectors), families)
		hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (bool, error) {
			var (
				i   int
				hit bool
			)
			err := r.Scan(&i, &hit)
			return hit, err
		})
		if err != nil || places.LongestEmptyRun(hits) < places.AbroadEmptyRun {
			if err == nil {
				out.Gemeinde, err = gemeinde(ctx, tx, q, out.Address, families)
			}
			return out, err
		}
		out.MayLieAbroad = true
		rows, _ = run(ctx, tx, nearestAddressFamily, q.At.Lon, q.At.Lat, families)
		fam, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if v := first(fam); v != nil {
			out.AbroadFamily = *v
		}
		return out, err
	})
}

// gemeindeAt reads the Gemeinde the position ($2, $3) lies in, as today's
// GEMEINDE_AT_POINT: $1 the nearest address's town folded (nil for none),
// named within $4 metres, else the nearest within $5, of the families $6;
// with its Kreis and Regierungsbezirk by key.
var gemeindeAt = named("api_location_gemeinde", `
with pt as (select st_setsrid(st_makepoint((select $2::float8), (select $3::float8)), 4326)::geography g),
gem as (
  select x.pref, x.admin_code, x.name, x.source_id from (
    (select 1 pref, g.admin_code, g.name, g.source_id
       from gazetteer_places g, pt
      where (select $1::text) is not null
        and `+keyedGemeinde+` and g.name_norm = (select $1::text)
        and st_dwithin(g.geom, pt.g, (select $4::float8))
      order by g.geom <-> pt.g
      limit 1)
    union all
    (select 2 pref, g.admin_code, g.name, g.source_id
       from gazetteer_places g, pt
      where `+keyedGemeinde+`
        and st_dwithin(g.geom, pt.g, (select $5::float8))
      order by g.geom <-> pt.g
      limit 1)
  ) x
  order by pref
  limit 1)
select gem.pref = 1, gem.admin_code, gem.name,
       (select d.name from gazetteer_places d
         where d.closed_at is null and d.source_id = 'bkg_gvisys' and d.kind = 'district' and d.admin_code = left(gem.admin_code, 5)
         limit 1),
       (select r.name from gazetteer_places r
         where r.closed_at is null and r.source_id = 'bkg_gvisys' and r.kind = 'admin' and r.admin_code = left(gem.admin_code, 3)
         limit 1),
       gem.source_id
from gem
where gem.source_id = any((select $6::text[])::text[])`, "text", "float8", "float8", "float8", "float8", "text[]")

// keyedGemeinde is an open BKG Gemeinde row g with its eight-digit key, as
// the platform's keyedGemeinde.
const keyedGemeinde = `g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$'`

// gemeinde reads the Gemeinde the position lies in, by the nearest
// address's town; nil when none.
func gemeinde(ctx context.Context, tx pgx.Tx, q places.LocationQuery, addr *places.Address, families []string) (*places.Gemeinde, error) {
	var town *string
	if addr != nil {
		town = places.GemeindeFold(addr.Place)
	}
	rows, _ := run(ctx, tx, gemeindeAt, town, q.At.Lon, q.At.Lat, float64(places.GemeindeByNameM), float64(places.GemeindeNearestM), families)
	gs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (places.Gemeinde, error) {
		var g places.Gemeinde
		err := r.Scan(&g.Inside, &g.Code, &g.Name, &g.Kreis, &g.Bezirk, &g.Family)
		return g, err
	})
	return first(gs), err
}
