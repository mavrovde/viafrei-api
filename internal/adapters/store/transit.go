package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The public-transport repositories (anti-corruption layer).

// stationByID reads one station: $1 the id, $2 the families. A station
// stored with no position is placed at 0,0 nowhere: the board names it.
var stationByID = `
select s.stop_place_id, s.name, coalesce(st_y(s.location), 0), coalesce(st_x(s.location), 0), s.national_stop_code,
       s.region_code, coalesce(s.modes, '{}'), s.source_ref
from contract.stop_place s
where s.stop_place_id = $1 and s.withdrawn_at is null and s.source_ref = any($2) and ` + permitted("s")

// Station reads the station with this id (features.Transit).
func (s *Store) Station(ctx context.Context, id string, families []string) (transit.Station, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.Station, error) {
		var (
			v     transit.Station
			modes []string
		)
		err := tx.QueryRow(ctx, stationByID, id, families).Scan(&v.ID, &v.Name, &v.Location.Lat, &v.Location.Lon,
			&v.NationalCode, &v.RegionCode, &modes, &v.Family)
		if errors.Is(err, pgx.ErrNoRows) {
			// No such station is an answer, not a failure of storage.
			return transit.Station{}, nil
		}
		v.Modes = transit.MapModes(modes)
		return v, err
	})
	if err == nil && out.ID == "" {
		err = transit.ErrNotFound
	}
	return out, err
}

// stationsPage reads the stations of the given families that answer a
// search: $1 families; $2, $3 the position's latitude and longitude, both
// null for none; $4 the folded text, empty for none; $5 the radius; $6 the
// limit; $7 a station code, empty for none. A station stored with no position is left out: an answer places
// every station it lists. The folded text holds letters and digits only, so
// it carries no LIKE wildcard.
var stationsPage = `
with here as (
  select case when $2::float8 is null then null
              else st_setsrid(st_makepoint($3::float8, $2::float8), 4326)::geography end g)
select s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code,
       coalesce(s.modes, '{}'), s.source_ref,
       case when here.g is null then null else round(st_distance(s.location::geography, here.g))::int end dist,
       s.placement_source_ref
from contract.stop_place s, here
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1)
  and ` + permitted("s") + `
  and ($4::text = '' or s.search_fold like '%' || $4::text || '%')
  and (here.g is null or st_dwithin(s.location::geography, here.g, $5))
  and ($7::text = '' or s.eva_no = $7::text or s.national_stop_code = $7::text)
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
		rows, _ := tx.Query(ctx, stationsPage, families, lat, lon, transit.Fold(q.Text), q.RadiusM, q.Limit+1, q.Code)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Station, error) {
			var (
				v     transit.Station
				modes []string
			)
			err := r.Scan(&v.ID, &v.Name, &v.Location.Lat, &v.Location.Lon, &v.NationalCode, &v.RegionCode,
				&modes, &v.Family, &v.DistanceM, &v.PlacedBy)
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

// boardCalls reads the departing calls of a station in a window: $1 the
// station (a stop place), $2 and $3 the window's first and end instant
// (planned departure), $4 the families. A call is one a passenger can board
// (contract.departure.boarding) of a trip that calls somewhere outside the
// station after it: a trip's last call at the station is an arrival. The
// destination is the trip's headsign, else the name of its last stop.
var boardCalls = `
select d.journey_id, d.stop_point_id, d.call_order, d.line_name, d.transport_mode, d.route_type,
       d.train_number, d.train_category, nullif(btrim(j.destination_label), ''),
       coalesce(nullif(btrim(j.destination_label), ''),
                (select lp.name from contract.scheduled_call lc
                   join contract.stop_point lp on lp.stop_point_id = lc.stop_point_id
                  where lc.journey_id = d.journey_id
                  order by lc.call_order desc limit 1)),
       d.planned_departure_at, d.platform, d.planned_platform, d.platform_changed, d.realtime, d.realtime_source,
       d.cancelled, d.delay_min, d.expected_departure_at, d.no_forecast, d.source_ref, d.source_refs
from contract.departure d
left join contract.journey j on j.journey_id = d.journey_id
where d.stop_place_id = $1
  and d.planned_departure_at >= $2 and d.planned_departure_at < $3
  and d.boarding
  and d.source_ref = any($4)
  and ` + permitted("d") + `
  and exists (select 1 from contract.scheduled_call nx
                join contract.stop_point np on np.stop_point_id = nx.stop_point_id
               where nx.journey_id = d.journey_id and nx.call_order > d.call_order
                 and np.stop_place_id <> $1)
order by d.planned_departure_at, d.journey_id, d.call_order`

// Departures reads the departing calls of q's station in q's window
// (features.Transit); the board's rules are transit.Board's.
func (s *Store) Departures(ctx context.Context, q transit.DepartureQuery, families []string) ([]transit.Departure, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Departure, error) {
		rows, _ := tx.Query(ctx, boardCalls, q.StationID, q.From, q.From.Add(q.Window), families)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Departure, error) {
			var (
				v    transit.Departure
				mode string
			)
			err := r.Scan(&v.JourneyID, &v.StopPointID, &v.CallOrder, &v.Line, &mode, &v.RouteType, &v.TrainNumber,
				&v.Category, &v.Headsign, &v.Destination, &v.PlannedAt, &v.Platform, &v.PlannedPlatform,
				&v.PlatformChanged, &v.Live, &v.Source, &v.Cancelled, &v.DelayMin, &v.ExpectedAt, &v.NoForecast,
				&v.Family, &v.Families)
			v.Mode = transit.MapMode(mode)
			return v, err
		})
	})
}

// stationFacilities reads the facilities of a station: $1 the station, $2
// the families, $3 now. A state observed longer ago than its family's
// stale_after_s, or never observed, is unknown.
var stationFacilities = `
select f.facility_id, f.facility_kind, f.position_text,
       case when f.status_observed_at is null then 'unknown'
            when s.stale_after_s is not null and f.status_observed_at < $3::timestamptz - make_interval(secs => s.stale_after_s) then 'unknown'
            else f.facility_status end,
       f.status_observed_at, f.source_ref
from contract.station_facility f
join contract.source s on s.source_ref = f.source_ref
where f.stop_place_id = $1 and f.source_ref = any($2) and ` + permitted("f") + `
order by f.facility_id`

// Facilities reads the facilities of a station (features.Transit).
func (s *Store) Facilities(ctx context.Context, station string, families []string, now time.Time) ([]transit.Facility, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Facility, error) {
		rows, _ := tx.Query(ctx, stationFacilities, station, families, now)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Facility, error) {
			var v transit.Facility
			err := r.Scan(&v.ID, &v.Kind, &v.Position, &v.State, &v.ObservedAt, &v.Family)
			return v, err
		})
	})
}
