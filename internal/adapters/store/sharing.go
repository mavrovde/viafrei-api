package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// sharingStationsNear is the platform's station read: the open stations of
// admitted systems within $3 metres of ($1, $2), each with its latest count
// of the 24 hours before $5 (by the status key), nearest first, at most $4;
// $6 the families (those their source rule gives the key; a count is read
// unless its system's feed is excluded, as the view read it).
var sharingStationsNear = named("api_sharing_stations_near", `
with here as (select st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography g),
near as (
  select s.system_id || ':' || s.station_id station_id, s.system_id, y.name system_name, s.name, s.capacity, s.is_virtual,
         st_y(s.geom::geometry) lat, st_x(s.geom::geometry) lon,
         round(st_distance(s.geom, here.g))::int as distance_m,
         y.status_seen_at, s.source_feed, s.station_id raw_id, y.source_feed system_feed
    from gbfs_stations s
    join gbfs_systems y using (system_id)
    cross join here
   where s.closed_at is null and y.admitted and s.geom is not null
     and st_dwithin(s.geom, here.g, (select $3::float8))
     and s.source_feed = any((`+ruledFamilies("(select $6::text[])")+`)::text[])
)
select n.station_id, n.system_id, n.system_name, n.name, n.capacity, n.is_virtual, n.lat, n.lon, n.distance_m, n.status_seen_at, n.source_feed,
       c.vehicles_available, c.electric_available, c.docks_available, c.is_renting, c.is_returning, c.observed_at, c.reported_at
  from near n
  left join lateral (
    select t.vehicles_available, t.electric_available, t.docks_available, t.is_renting, t.is_returning, t.observed_at, t.reported_at
      from gbfs_station_status t
     where t.system_id = n.system_id and t.station_id = n.raw_id
       and t.observed_at > (select $5::timestamptz) - interval '24 hours' and t.observed_at <= (select $5::timestamptz)
       and not coalesce((select k.excluded from contract_source_rules k where n.system_feed ~ k.pattern order by k.priority limit 1), false)
     order by t.observed_at desc
     limit 1
  ) c on true
 order by n.distance_m, n.station_id
 limit (select $4::int)`, "float8", "float8", "float8", "int", "timestamptz", "text[]")

// sharingVehiclesNear is the platform's vehicle read: each position's latest
// vehicle of an admitted system within $3 metres, nearest first, at most
// $4; $5 the families.
var sharingVehiclesNear = named("api_sharing_vehicles_near", `
with here as (select st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography g)
select v.form_factor, v.propulsion, v.range_m, round(st_distance(v.geom, here.g))::int as distance_m, v.vehicles_seen_at, v.source_feed
  from (select distinct on (x.source_feed, st_astext(x.geom::geometry)) x.system_id || ':' || x.seq vehicle_id, x.form_factor, x.propulsion,
               x.range_m, x.geom, y.vehicles_seen_at, x.source_feed
          from gbfs_vehicles x
          join gbfs_systems y using (system_id)
          cross join here
         where y.admitted and x.geom is not null
           and st_dwithin(x.geom, here.g, (select $3::float8))
           and x.source_feed = any((`+ruledFamilies("(select $5::text[])")+`)::text[])
         order by x.source_feed, st_astext(x.geom::geometry), x.observed_at desc) v
  cross join here
 order by distance_m, v.vehicle_id
 limit (select $4::int)`, "float8", "float8", "float8", "int", "text[]")

// sharingNearest is the nearest station within $3 metres; $4 the families.
var sharingNearest = named("api_sharing_nearest", `
with here as (select st_setsrid(st_makepoint((select $1::float8), (select $2::float8)), 4326)::geography g)
select s.system_id || ':' || s.station_id, s.system_id, y.name, s.name, s.capacity, s.is_virtual,
       st_y(s.geom::geometry), st_x(s.geom::geometry), round(st_distance(s.geom, here.g))::int,
       y.status_seen_at, s.source_feed,
       null::int, null::int, null::int, null::bool, null::bool, null::timestamptz, null::timestamptz
  from gbfs_stations s
  join gbfs_systems y using (system_id)
  cross join here
 where s.closed_at is null and y.admitted and s.geom is not null
   and st_dwithin(s.geom, here.g, (select $3::float8))
   and s.source_feed = any((`+ruledFamilies("(select $4::text[])")+`)::text[])
 order by s.geom <-> here.g, 1
 limit 1`, "float8", "float8", "float8", "text[]")

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
		rows, _ := run(ctx, tx, sharingStationsNear, q.At.Lon, q.At.Lat, float64(q.RadiusM), transit.SharingStationRead, q.Now, families)
		return pgx.CollectRows(rows, scanSharingStation)
	})
}

// SharingVehicles reads the free-floating vehicles q asks for
// (features.Sharing).
func (s *Store) SharingVehicles(ctx context.Context, q transit.SharingQuery, families []string) ([]transit.SharingVehicle, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.SharingVehicle, error) {
		rows, _ := run(ctx, tx, sharingVehiclesNear, q.At.Lon, q.At.Lat, float64(q.RadiusM), transit.SharingVehicleRead, families)
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
		rows, _ := run(ctx, tx, sharingNearest, q.At.Lon, q.At.Lat, float64(transit.SharingNearestM), families)
		return pgx.CollectRows(rows, scanSharingStation)
	})
}
