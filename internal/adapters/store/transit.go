package store

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The public-transport repositories (anti-corruption layer).

// stationByID reads one station: $1 the id, $2 the families. A station
// stored with no position is placed at 0,0 nowhere: the board names it. It
// reads the station's stop points too (contract.stop_point of the stop
// place), the family the platform's departure board reads.
var stationByID = namedOpaque("api_station_by_id", `
select s.stop_place_id, s.name, coalesce(st_y(s.location), 0), coalesce(st_x(s.location), 0), s.national_stop_code,
       s.region_code, coalesce(s.modes, '{}'), s.source_ref,
       array(select p.stop_point_id from (`+relStopPoint+`) p
              where p.stop_place_id = s.stop_place_id and p.source_ref = any($2) and `+permitted("p")+`
              order by p.stop_point_id)
from (`+relStopPlace+`) s
where s.stop_place_id = $1 and s.withdrawn_at is null and s.source_ref = any($2) and `+permitted("s"), "text", "text[]")

// Station reads the station with this id (features.Transit).
func (s *Store) Station(ctx context.Context, id string, families []string) (transit.Station, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.Station, error) {
		var (
			v      transit.Station
			modes  []string
			points []string
		)
		err := runRow(ctx, tx, stationByID, id, families).Scan(&v.ID, &v.Name, &v.Location.Lat, &v.Location.Lon,
			&v.NationalCode, &v.RegionCode, &modes, &v.Family, &points)
		v.StopIDs = transit.FamilyOf(v.ID, points)
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
// limit; $7 a station code, empty for none; $8 railway stations only; $9 an
// official region key the district code starts with, empty for any (a
// station with no district then left out). A station stored with no position is left out: an answer places
// every station it lists. The folded text holds letters and digits only, so
// it carries no LIKE wildcard. It is sent as written, not opaque: each
// optional filter is decided by its argument, and on prod the generic plan
// that cannot see them ran past 5 s where the asked plan took 7 ms.
var stationsPage = namedOpaque("api_stations_page", `
with here as (
  select case when $2::float8 is null then null
              else st_setsrid(st_makepoint($3::float8, $2::float8), 4326)::geography end g)
select s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code,
       coalesce(s.modes, '{}'), s.source_ref,
       case when here.g is null then null else round(st_distance(s.location::geography, here.g))::int end dist,
       s.placement_source_ref, s.eva_no, s.eva_numbers
from (`+relStopPlace+`) s, here
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1)
  and `+permitted("s")+`
  and ($4::text = '' or s.search_fold like '%' || $4::text || '%')
  and (here.g is null or st_dwithin(s.location::geography, here.g, $5))
  and ($7::text = '' or s.eva_no = $7::text or s.national_stop_code = $7::text)
  and (not $8::bool or s.eva_no is not null)
  and ($9::text = '' or s.region_code like $9::text || '%')
order by case when here.g is null and not s.search_fold like $4::text || '%' then 1 else 0 end,
         dist, s.name, s.stop_place_id
limit $6`, "text[]", "double precision", "double precision", "text", "double precision", "bigint", "text", "boolean", "text")

// Stations reads the stations that answer q (features.Transit), one more
// than the limit, to tell whether more answer.
func (s *Store) Stations(ctx context.Context, q transit.Query, families []string) ([]transit.Station, bool, error) {
	var lat, lon *float64
	if q.At != nil {
		lat, lon = &q.At.Lat, &q.At.Lon
	}
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Station, error) {
		rows, _ := run(ctx, tx, stationsPage, families, lat, lon, transit.Fold(q.Text), q.RadiusM, q.Limit+1, q.Code, q.Railway, q.Area)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Station, error) {
			var (
				v     transit.Station
				modes []string
			)
			err := r.Scan(&v.ID, &v.Name, &v.Location.Lat, &v.Location.Lon, &v.NationalCode, &v.RegionCode,
				&modes, &v.Family, &v.DistanceM, &v.PlacedBy, &v.EVA, &v.EVANumbers)
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
// destination is the trip's headsign, else the name of its last stop. The
// delay is read in seconds and stated in minutes by transit.DelayMinOf.
var boardCalls = namedOpaque("api_board_calls", `
select d.journey_id, d.stop_point_id, d.call_order, d.line_name, d.transport_mode, d.route_type,
       d.train_number, d.train_category, nullif(btrim(j.destination_label), ''),
       coalesce(nullif(btrim(j.destination_label), ''),
                (select lp.name from (`+relScheduledCall+`) lc
                   join (`+relStopPoint+`) lp on lp.stop_point_id = lc.stop_point_id
                  where lc.journey_id = d.journey_id
                  order by lc.call_order desc limit 1)),
       d.planned_departure_at, d.platform, d.planned_platform, d.platform_changed, d.realtime, d.realtime_source,
       d.cancelled, d.delay_s, d.expected_departure_at, d.no_forecast, d.source_ref, d.source_refs
from (`+relDeparture+`) d
left join (`+relJourney+`) j on j.journey_id = d.journey_id
where d.stop_place_id = $1
  and d.planned_departure_at >= $2 and d.planned_departure_at < $3
  and d.boarding
  and d.source_ref = any($4)
  and `+permitted("d")+`
  and exists (select 1 from (`+relScheduledCall+`) nx
                join (`+relStopPoint+`) np on np.stop_point_id = nx.stop_point_id
               where nx.journey_id = d.journey_id and nx.call_order > d.call_order
                 and np.stop_place_id <> $1)
order by d.planned_departure_at, d.journey_id, d.call_order`, "text", "timestamp with time zone", "timestamp with time zone", "text[]")

// Departures reads the departing calls of q's station in q's window
// (features.Transit); the board's rules are transit.Board's.
func (s *Store) Departures(ctx context.Context, q transit.DepartureQuery, families []string) ([]transit.Departure, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Departure, error) {
		rows, _ := run(ctx, tx, boardCalls, q.StationID, q.From, q.From.Add(q.Window), families)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Departure, error) {
			var (
				v      transit.Departure
				mode   string
				delayS *int
			)
			err := r.Scan(&v.JourneyID, &v.StopPointID, &v.CallOrder, &v.Line, &mode, &v.RouteType, &v.TrainNumber,
				&v.Category, &v.Headsign, &v.Destination, &v.PlannedAt, &v.Platform, &v.PlannedPlatform,
				&v.PlatformChanged, &v.Live, &v.Source, &v.Cancelled, &delayS, &v.ExpectedAt, &v.NoForecast,
				&v.Family, &v.Families)
			v.DelayMin = transit.DelayMinOf(delayS)
			v.Mode = transit.MapMode(mode)
			return v, err
		})
	})
}

// stationFacilities reads the facilities of a station: $1 the station, $2
// the families, $3 now. A state observed longer ago than its family's
// stale_after_s, or never observed, is unknown.
var stationFacilities = namedOpaque("api_station_facilities", `
select f.facility_id, f.facility_kind, f.position_text,
       case when f.status_observed_at is null then 'unknown'
            when s.stale_after_s is not null and f.status_observed_at < $3::timestamptz - make_interval(secs => s.stale_after_s) then 'unknown'
            else f.facility_status end,
       f.status_observed_at, f.source_ref
from (`+stationFacilityOf+`) f
join (`+relSource+`) s on s.source_ref = f.source_ref
where f.stop_place_id = $1 and f.source_ref = any($2) and `+permitted("f")+`
order by f.facility_id`, "text", "text[]", "timestamp with time zone")

// stationFacilityOf is the station facility relation of the station $1
// only. A facility's stop place is contract_stop_place_of its ref, the
// timetable's station whose id is the ref's DHID or starts with it and a
// colon: so the DHID of that station's id is the ref's DHID, and only refs
// of that DHID are mapped (on prod mapping every ref took 850 ms a station).
const stationFacilityOf = `
with k as (select substring(substr($1::text, 3), '` + dhidOf + `') d where left($1::text, 2) = '1:')
select 'equipment:' || e.equipment_id facility_id, contract_stop_place_of(e.stop_place_id) stop_place_id, e.kind facility_kind,
       e.description position_text, coalesce(st.state, 'unknown') facility_status,
       coalesce(st.status_since, st.last_seen_at, e.last_seen_at) status_observed_at, coalesce(st.source_feed, e.source_feed) source_ref,
       'open' display_class, 'public' audience
from station_equipment e
left join station_equipment_state st on st.equipment_id = e.equipment_id
where e.closed_at is null and substring(e.stop_place_id, '` + dhidOf + `') = (select k.d from k)
union all
select 'fasta:' || f.equipment_no, (select contract_stop_place_of(d.ifopt) from db_stations d where d.station_no = f.station_no),
       f.kind, f.description, f.state, coalesce(f.state_since, f.last_seen_at), f.source_feed, 'open', 'public'
from db_facilities f
where f.station_no in (select d.station_no from db_stations d where substring(d.ifopt, '` + dhidOf + `') = (select k.d from k))`

// dhidOf is the DHID a ref names, as contract_stop_place_of reads it.
const dhidOf = `^(?:dhid:)?([a-z]{2}:[0-9]{5}:[0-9]+)`

// Facilities reads the facilities of a station (features.Transit).
func (s *Store) Facilities(ctx context.Context, station string, families []string, now time.Time) ([]transit.Facility, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Facility, error) {
		rows, _ := run(ctx, tx, stationFacilities, station, families, now)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Facility, error) {
			var v transit.Facility
			err := r.Scan(&v.ID, &v.Kind, &v.Position, &v.State, &v.ObservedAt, &v.Family)
			return v, err
		})
	})
}

// The affected-stations board reads each licence boundary in a statement of
// its own and puts the answers side by side, as the platform's board does:
// the departures of the next hour cancelled or late, the service alerts in
// force and the lifts out of service name the stations (by stop place id or
// IFOPT); then the stations themselves.

// affectedAlerts are the refs the alerts in force at $2 of the families ($1)
// name, with the alert and its family; only of a live publication, as the
// platform's station card asks only the alert sources alive (alertLive).
var affectedAlerts = namedOpaque("api_affected_alerts", `
select distinct a.alert_id, a.source_ref, u.sid
from (`+relServiceAlert+`) a, unnest(a.stop_ids) u(sid)
where a.source_ref = any($1) and `+"a.display_class <> 'withhold' and (a.audience = 'public' or "+ownKeyOn+")"+`
  and (a.active_from is null or a.active_from <= $2) and (a.active_to is null or a.active_to > $2)
  and `+alertLive("a", "$2"), "text[]", "timestamp with time zone")

// affectedLifts are the stations' lifts and escalators out of service, of
// the families ($1). The out-of-service rows are kept first, in a subquery
// the filter on the stop place id is not pushed into (offset 0): the view's
// stop place id is a lookup per row, which that filter would otherwise run
// for every facility, in service or not.
var affectedLifts = namedOpaque("api_affected_lifts", `
select f.stop_place_id, count(*)::int, array_agg(distinct f.source_ref order by f.source_ref)
from (
  select f.stop_place_id, f.source_ref
  from (`+relStationFacility+`) f
  where f.facility_status = 'inactive' and f.source_ref = any($1)
    and `+"f.display_class <> 'withhold' and (f.audience = 'public' or "+ownKeyOn+")"+`
  offset 0
) f
where f.stop_place_id is not null
group by 1`, "text[]")

// affectedCalls are the stations (by IFOPT) with departures of the next hour
// cancelled or late, of the families ($1), as the platform's board counts
// them: per station, minute and line the sources are folded (the most
// cancelled, and as late the most cancelled or late less the most
// cancelled), then summed per station; with the families read.
var affectedCalls = namedOpaque("api_affected_calls", `
with d as (
  select d.station_ifopt, d.aimed_minute, d.line_key, d.cancelled, d.late, d.source_ref
  from (`+relStationDisruption+`) d
  where d.source_ref = any($1) and `+"d.display_class <> 'withhold' and (d.audience = 'public' or "+ownKeyOn+")"+`
), f as (
  select station_ifopt, aimed_minute, line_key, max(cancelled) as c, greatest(max(cancelled + late) - max(cancelled), 0) as l
  from d group by 1, 2, 3
), s as (
  select station_ifopt, sum(c)::int as c, sum(l)::int as l from f group by 1 having sum(c) + sum(l) > 0
)
select s.station_ifopt, s.c, s.l, (select array_agg(distinct d.source_ref order by d.source_ref) from d where d.station_ifopt = s.station_ifopt)
from s`, "text[]")

// affectedPlaces are the stations of the families ($1) with these ids or
// IFOPTs ($2), each with the ref it was named by.
var affectedPlaces = namedOpaque("api_affected_places", `
with hits as (
  select distinct q.ref, s.gtfs_feed_key, s.id
  from (select distinct ref from unnest($2::text[]) u(ref)) q
  cross join lateral (
    select s.gtfs_feed_key, s.id from stops s
    where (s.gtfs_feed_key::text || ':') || s.id = q.ref and s.deleted_at is null
    union all
    select s.gtfs_feed_key, s.id from feeds f join stops s on s.gtfs_feed_key = f.gtfs_feed_key and s.id = q.ref
    where s.deleted_at is null
      and (s.id ~ '^[a-z]{2}:[0-9]{5}:[0-9]+$' or exists (select 1 from db_stations x where x.ifopt = s.id and x.closed_at is null))) s)
select r.ref, s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code, coalesce(s.modes, '{}'), s.source_ref
from unnest($2::text[]) r(ref)
join hits h on h.ref = r.ref
join (`+relStopPlace+`) s on s.stop_place_id = (h.gtfs_feed_key::text || ':') || h.id
where s.withdrawn_at is null and s.location is not null and s.source_ref = any($1)
  and `+"s.display_class <> 'withhold' and (s.audience = 'public' or "+ownKeyOn+")", "text[]", "text[]")

// affectedRef is what one ref (a stop place id or an IFOPT) is named by.
type affectedRef struct {
	alerts          map[string]bool
	lifts           int
	cancelled, late int
	fams            []string
}

// AffectedStations ranks the stations of the families by what affects them
// at now (features.Affected): cancelled departures of the next hour first,
// then late ones, then alerts in force, then lifts out of service; ties by
// name, then id. A station is listed when it has a departure of the next
// hour cancelled or late, or an alert in force or a lift out of service
// names it. The departures are counted by the read contract at the instant
// of the read.
func (s *Store) AffectedStations(ctx context.Context, families []string, now time.Time, limit int) ([]transit.Affected, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.Affected, error) {
		by, err := affectedRefs(ctx, tx, families, now)
		if err != nil {
			return nil, err
		}
		stations, err := affectedPlacesOf(ctx, tx, families, by)
		if err != nil {
			return nil, err
		}
		out := make([]transit.Affected, 0, len(stations))
		for _, a := range stations {
			fs := slices.Clone(a.Families)
			slices.Sort(fs)
			own := a.Station.Family
			a.Families = append([]string{own}, slices.DeleteFunc(slices.Compact(fs), func(f string) bool { return f == own })...)
			out = append(out, *a)
		}
		slices.SortFunc(out, func(x, y transit.Affected) int {
			return cmp.Or(cmp.Compare(y.Cancelled, x.Cancelled), cmp.Compare(y.Late, x.Late), cmp.Compare(y.Alerts, x.Alerts),
				cmp.Compare(y.LiftsOut, x.LiftsOut), cmp.Compare(x.Station.Name, y.Station.Name), cmp.Compare(x.Station.ID, y.Station.ID))
		})
		return out[:min(limit, len(out))], nil
	})
}

// affectedRefs reads what the alerts in force and the lifts out of service
// name, by ref.
func affectedRefs(ctx context.Context, tx pgx.Tx, families []string, now time.Time) (map[string]*affectedRef, error) {
	by := map[string]*affectedRef{}
	at := func(ref string) *affectedRef {
		if by[ref] == nil {
			by[ref] = &affectedRef{alerts: map[string]bool{}}
		}
		return by[ref]
	}
	var (
		id, fam, ref string
		lifts        int
		fams         []string
	)
	rows, _ := run(ctx, tx, affectedAlerts, families, now)
	if _, err := pgx.ForEachRow(rows, []any{&id, &fam, &ref}, func() error {
		n := at(ref)
		n.alerts[fam+":"+id], n.fams = true, append(n.fams, fam)
		return nil
	}); err != nil {
		return nil, err
	}
	rows, _ = run(ctx, tx, affectedLifts, families)
	if _, err := pgx.ForEachRow(rows, []any{&ref, &lifts, &fams}, func() error {
		n := at(ref)
		n.lifts, n.fams = lifts, append(n.fams, fams...)
		return nil
	}); err != nil {
		return nil, err
	}
	var cancelled, late int
	rows, _ = run(ctx, tx, affectedCalls, families)
	_, err := pgx.ForEachRow(rows, []any{&ref, &cancelled, &late, &fams}, func() error {
		n := at(ref)
		n.cancelled, n.late, n.fams = cancelled, late, append(n.fams, fams...)
		return nil
	})
	return by, err
}

// affectedPlacesOf reads the stations the refs name, each with the alerts and
// lifts its refs carry.
func affectedPlacesOf(ctx context.Context, tx pgx.Tx, families []string, by map[string]*affectedRef) (map[string]*transit.Affected, error) {
	refs := make([]string, 0, len(by))
	for r := range by {
		refs = append(refs, r)
	}
	slices.Sort(refs)
	stations := map[string]*transit.Affected{}
	alerts := map[string]map[string]bool{}
	var (
		ref   string
		st    transit.Station
		modes []string
	)
	rows, _ := run(ctx, tx, affectedPlaces, families, refs)
	_, err := pgx.ForEachRow(rows, []any{&ref, &st.ID, &st.Name, &st.Location.Lat, &st.Location.Lon, &st.NationalCode, &st.RegionCode, &modes, &st.Family}, func() error {
		a := stations[st.ID]
		if a == nil {
			st.Modes = transit.MapModes(modes)
			a = &transit.Affected{Station: st}
			stations[st.ID], alerts[st.ID] = a, map[string]bool{}
		}
		n := by[ref]
		a.LiftsOut += n.lifts
		a.Cancelled += n.cancelled
		a.Late += n.late
		for k := range n.alerts {
			alerts[st.ID][k] = true
		}
		a.Alerts = len(alerts[st.ID])
		a.Families = append(a.Families, n.fams...)
		return nil
	})
	return stations, err
}
