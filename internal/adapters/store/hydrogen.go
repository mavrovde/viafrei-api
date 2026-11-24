package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// hydrogenColumns are the columns of a hydrogen station, $1 and $2 the
// position asked (lon, lat).
const hydrogenColumns = `h.source_feed || ':' || h.id, h.name, h.operator, h.brand, st_y(h.geom::geometry), st_x(h.geom::geometry),
       round(st_distance(h.geom, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography))::int,
       h.address, h.refill, h.opening, h.provider_updated_at, h.last_seen_at, h.source_feed
from hydrogen_stations h`

// hydrogenOpen keeps an open station with a place, of the families $n whose
// source rule gives them to the key (ruledFamilies).
func hydrogenOpen(n string) string {
	return `h.closed_at is null and h.geom is not null
  and h.source_feed = any((` + ruledFamilies("(select $"+n+"::text[])") + `)::text[])`
}

// hydrogenSuffix is the ending of a refill solution's code for a vehicle, as
// AFIR names them (gaseousHydrogen700barCar, gaseousHydrogen350barTruck) and
// the platform's hydrogen search matches them; empty for any vehicle.
func hydrogenSuffix(vehicle string) string {
	switch vehicle {
	case "car":
		return "Car"
	case "truck":
		return "Truck"
	}
	return ""
}

// hydrogenVehicle keeps a station with a refill solution for the vehicle $n
// (null: any), as the platform's hydrogen search does.
func hydrogenVehicle(n string) string {
	return `((select $` + n + `::text) is null or exists (select 1 from jsonb_array_elements(h.refill) p where p->>'solution' like '%' || (select $` + n + `::text)))`
}

// hydrogenNear reads the stations within $3 metres, of the families $4, for
// the vehicle $5, nearest first then by name, at most $6.
var hydrogenNear = named("api_hydrogen_near", `select `+hydrogenColumns+`
where st_dwithin(h.geom, st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography, (select $3::float8))
  and `+hydrogenOpen("4")+` and `+hydrogenVehicle("5")+`
order by 7, h.name nulls last, 1
limit (select $6::int)`, "float8", "float8", "float8", "text[]", "text", "int")

// hydrogenNearest reads the one nearest station beyond any radius.
var hydrogenNearest = named("api_hydrogen_nearest", `select `+hydrogenColumns+`
where `+hydrogenOpen("3")+` and `+hydrogenVehicle("4")+`
order by h.geom <-> st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography, 1
limit 1`, "float8", "float8", "text[]", "text")

func scanHydrogen(r pgx.CollectableRow) (energy.HydrogenStation, error) {
	var v energy.HydrogenStation
	err := r.Scan(&v.ID, &v.Name, &v.Operator, &v.Brand, &v.Location.Lat, &v.Location.Lon, &v.DistanceM,
		&v.Address, &v.Refill, &v.Opening, &v.ProviderUpdatedAt, &v.LastSeenAt, &v.Family)
	return v, err
}

func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// HydrogenNear reads the hydrogen stations q asks for, limit+1 to tell
// whether more are inside (features.Hydrogen).
func (s *Store) HydrogenNear(ctx context.Context, q energy.HydrogenQuery, families []string) ([]energy.HydrogenStation, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.HydrogenStation, error) {
		rows, _ := run(ctx, tx, hydrogenNear, q.At.Lon, q.At.Lat, float64(q.RadiusM), families, nullText(hydrogenSuffix(q.Vehicle)), q.Limit+1)
		return pgx.CollectRows(rows, scanHydrogen)
	})
}

// HydrogenNearest reads the nearest hydrogen station for q's vehicle at any
// distance, or none (features.Hydrogen).
func (s *Store) HydrogenNearest(ctx context.Context, q energy.HydrogenQuery, families []string) ([]energy.HydrogenStation, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.HydrogenStation, error) {
		rows, _ := run(ctx, tx, hydrogenNearest, q.At.Lon, q.At.Lat, families, nullText(hydrogenSuffix(q.Vehicle)))
		return pgx.CollectRows(rows, scanHydrogen)
	})
}
