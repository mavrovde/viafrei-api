package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// sharingStationsNear is the platform's station read: the stations within
// $3 metres of ($1, $2), each with its latest count of the 24 hours before
// $5, nearest first, at most $4; $6 the families.
var sharingStationsNear = `
with near as (
  select s.station_id, s.system_id, s.system_name, s.name, s.capacity, s.is_virtual,
         st_y(s.location), st_x(s.location),
         round(st_distance(s.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int as distance_m,
         s.status_seen_at, s.source_ref
    from contract.sharing_station s
   where st_dwithin(s.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
     and s.source_ref = any($6) and ` + permitted("s") + `
)
select n.*, c.vehicles_available, c.electric_available, c.docks_available, c.is_renting, c.is_returning, c.observed_at, c.reported_at
  from near n
  left join lateral (
    select t.vehicles_available, t.electric_available, t.docks_available, t.is_renting, t.is_returning, t.observed_at, t.reported_at
      from contract.sharing_station_status t
     where t.station_id = n.station_id
       and t.observed_at > $5::timestamptz - interval '24 hours' and t.observed_at <= $5::timestamptz
     order by t.observed_at desc
     limit 1
  ) c on true
 order by n.distance_m, n.station_id
 limit $4`

// sharingVehiclesNear is the platform's vehicle read: each position's latest
// vehicle within $3 metres, nearest first, at most $4; $5 the families.
var sharingVehiclesNear = `
select v.form_factor, v.propulsion, v.range_m,
       round(st_distance(v.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int as distance_m,
       v.vehicles_seen_at, v.source_ref
  from (select distinct on (x.source_ref, st_astext(x.location)) x.*
          from contract.sharing_vehicle x
         where st_dwithin(x.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
           and x.source_ref = any($5) and ` + permitted("x") + `
         order by x.source_ref, st_astext(x.location), x.observed_at desc) v
 order by distance_m, v.vehicle_id
 limit $4`

// sharingNearest is the nearest station within $3 metres; $4 the families.
var sharingNearest = `
select s.station_id, s.system_id, s.system_name, s.name, s.capacity, s.is_virtual,
       st_y(s.location), st_x(s.location),
       round(st_distance(s.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int,
       s.status_seen_at, s.source_ref,
       null::int, null::int, null::int, null::bool, null::bool, null::timestamptz, null::timestamptz
  from contract.sharing_station s
 where st_dwithin(s.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
   and s.source_ref = any($4) and ` + permitted("s") + `
 order by s.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, s.station_id
 limit 1`

func scanSharingStation(r pgx.CollectableRow) (transit.SharingStation, error) {
	var v transit.SharingStation
	err := r.Scan(&v.ID, &v.SystemID, &v.SystemName, &v.Name, &v.Capacity, &v.IsVirtual, &v.Location.Lat, &v.Location.Lon,
		&v.DistanceM, &v.StatusSeenAt, &v.Family, &v.VehiclesAvailable, &v.ElectricAvailable, &v.DocksAvailable,
		&v.IsRenting, &v.IsReturning, &v.CountedAt, &v.ReportedAt)
	return v, err
}

// SharingStations reads the stations q asks for (features.Sharing).
func (s *Store) SharingStations(ctx context.Context, q transit.SharingQuery, families []string) ([]transit.SharingStation, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.SharingStation, error) {
		rows, _ := tx.Query(ctx, sharingStationsNear, q.At.Lon, q.At.Lat, q.RadiusM, transit.SharingStationRead, q.Now, families)
		return pgx.CollectRows(rows, scanSharingStation)
	})
}

// SharingVehicles reads the free-floating vehicles q asks for
// (features.Sharing).
func (s *Store) SharingVehicles(ctx context.Context, q transit.SharingQuery, families []string) ([]transit.SharingVehicle, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.SharingVehicle, error) {
		rows, _ := tx.Query(ctx, sharingVehiclesNear, q.At.Lon, q.At.Lat, q.RadiusM, transit.SharingVehicleRead, families)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.SharingVehicle, error) {
			var v transit.SharingVehicle
			err := r.Scan(&v.FormFactor, &v.Propulsion, &v.RangeM, &v.DistanceM, &v.VehiclesSeenAt, &v.Family)
			return v, err
		})
	})
}

// SharingNearest reads the nearest station within transit.SharingNearestM
// of q.At, or none (features.Sharing).
func (s *Store) SharingNearest(ctx context.Context, q transit.SharingQuery, families []string) ([]transit.SharingStation, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.SharingStation, error) {
		rows, _ := tx.Query(ctx, sharingNearest, q.At.Lon, q.At.Lat, transit.SharingNearestM, families)
		return pgx.CollectRows(rows, scanSharingStation)
	})
}
