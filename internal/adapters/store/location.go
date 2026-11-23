package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// statedM is a distance from ($1, $2) on the sphere, as the platform
// measures and states it: metres under 50 m, else to 100 m.
func statedM(col string) string {
	d := `st_distance(` + col + `::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, false)::numeric`
	return `(case when ` + d + ` < 50 then round(` + d + `) else round(` + d + ` / 100) * 100 end)::int`
}

// nearestPlace reads the nearest place of the kind $3 within $4 metres of
// ($1, $2), of the families $5.
var nearestPlace = opaque(`select p.place_id, p.name, p.place_kind, st_y(p.location), st_x(p.location), p.region_code, p.source_ref,
       `+statedM("p.location")+`, p.official_key
from contract.place p
where p.place_kind = $3 and p.withdrawn_at is null and p.location is not null
  and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $4)
  and p.source_ref = any($5) and `+permitted("p")+`
order by p.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, p.place_id
limit 1`, "double precision", "double precision", "text", "double precision", "text[]")

// addressSectors reads, for each wedge of $1 (WKT), whether it holds an
// address of the families $2. It is sent as written, not opaque: on the
// fixtures its generic plan saves 0.4 ms of planning a call and loses 0.8 ms
// executing (6.6 ms against 5.8 ms for the plan made for the asked wedges).
var addressSectors = `select w.i::int, exists (select 1 from contract.address a
  where a.source_ref = any($2) and ` + permitted("a") + ` and st_intersects(a.location::geography, st_geogfromtext(w.wkt)))
from unnest($1::text[]) with ordinality as w(wkt, i)
order by w.i`

// nearestAddressFamily reads the family of the nearest address of the
// families $3 to ($1, $2), however far.
var nearestAddressFamily = `select a.source_ref from contract.address a
where a.source_ref = any($3) and ` + permitted("a") + `
order by a.location <-> st_setsrid(st_makepoint($1, $2), 4326), a.address_id
limit 1`

// stateOf reads the Land whose region code begins the code $1.
var stateOf = opaque(`select p.place_id, p.name, p.place_kind, st_y(p.location), st_x(p.location), p.region_code, p.source_ref, null::int, p.official_key
from contract.place p
where p.place_kind = 'state' and p.withdrawn_at is null and p.location is not null and p.region_code = left($1, 2)
  and p.source_ref = any($2) and `+permitted("p")+`
order by p.place_id
limit 1`, "text", "text[]")

// nearestAddress reads the nearest address within $3 metres of ($1, $2).
var nearestAddress = `select a.address_id, a.street, a.house_number, a.postcode, a.place_name, a.land_name,
       st_y(a.location), st_x(a.location), a.data_as_of, a.source_ref,
       round(st_distance(a.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int
from contract.address a
where st_dwithin(a.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
  and a.source_ref = any($4) and ` + permitted("a") + `
order by a.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, a.address_id
limit 1`

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
		rows, _ := tx.Query(ctx, nearestAddress, q.At.Lon, q.At.Lat, places.AddressReachM, families)
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
			rows, _ = tx.Query(ctx, nearestPlace, q.At.Lon, q.At.Lat, k.kind, k.reach, families)
			ps, err := pgx.CollectRows(rows, scanPlace)
			if err != nil {
				return out, err
			}
			*k.into = first(ps)
		}
		if out.District != nil && out.District.RegionCode != nil {
			rows, _ = tx.Query(ctx, stateOf, *out.District.RegionCode, families)
		} else {
			rows, _ = tx.Query(ctx, nearestPlace, q.At.Lon, q.At.Lat, "state", places.StateReachM, families)
		}
		ps, err := pgx.CollectRows(rows, scanPlace)
		if err != nil {
			return out, err
		}
		out.State = first(ps)
		if out.Address != nil && *out.Address.DistanceM <= places.AbroadNearestM {
			return out, nil
		}
		rows, _ = tx.Query(ctx, addressSectors, places.SectorWedges(q.At, places.AbroadScanM, places.AbroadSectors), families)
		hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (bool, error) {
			var (
				i   int
				hit bool
			)
			err := r.Scan(&i, &hit)
			return hit, err
		})
		if err != nil || places.LongestEmptyRun(hits) < places.AbroadEmptyRun {
			return out, err
		}
		out.MayLieAbroad = true
		rows, _ = tx.Query(ctx, nearestAddressFamily, q.At.Lon, q.At.Lat, families)
		fam, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if v := first(fam); v != nil {
			out.AbroadFamily = *v
		}
		return out, err
	})
}
