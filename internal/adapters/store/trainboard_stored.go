package store

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// storedTrainBoard reads the board the stored-boards worker wrote for the
// station whose EVA numbers hold $1, of the families $2: one row per
// station, its departures as stored (planned order), each departure a row.
var storedTrainBoard = named("api_train_board_stored", `
with b as (
  select s.eva_nos, s.read_at, s.degraded, s.covers_until, s.departures
  from db_station_boards s
  where s.eva_nos @> array[(select $1::text)]
    and s.source_feed = any((`+ruledFamilies("(select $2::text[])")+`)::text[])
  order by s.read_at desc, s.station_no
  limit 1)
select b.eva_nos, b.read_at, b.degraded, b.covers_until, d.line, d.category, d.destination, d."plannedAt", d."delayMin",
       d."plannedPlatform", d.platform, d."platformChanged", d.cancelled
from b
left join lateral jsonb_to_recordset(b.departures)
  as d(line text, category text, destination text, "plannedAt" text, "delayMin" int, "plannedPlatform" text, platform text,
       "platformChanged" boolean, cancelled boolean) on true`, "text", "text[]")

// StoredTrainBoard returns the board the stored-boards worker wrote for the
// station of eva, nil when none is (features.TrainBoards).
func (s *Store) StoredTrainBoard(ctx context.Context, eva string, families []string) (*transit.StoredBoard, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (*transit.StoredBoard, error) {
		rows, _ := run(ctx, tx, storedTrainBoard, eva, families)
		var (
			b    *transit.StoredBoard
			v    transit.StoredBoard
			d    transit.StoredDeparture
			line *string
		)
		_, err := pgx.ForEachRow(rows, []any{&v.EVAs, &v.ReadAt, &v.Degraded, &v.CoversUntil, &line, &d.Category, &d.Destination, &d.PlannedAt,
			&d.DelayMin, &d.PlannedPlatform, &d.Platform, &d.PlatformChanged, &d.Cancelled}, func() error {
			if b == nil {
				b = &transit.StoredBoard{EVAs: v.EVAs, ReadAt: v.ReadAt, Degraded: v.Degraded, CoversUntil: v.CoversUntil, Departures: []transit.StoredDeparture{}}
			}
			// The lateral join's one empty row of a board with no departure.
			if line != nil || d.PlannedAt != nil {
				d.Line = line
				b.Departures = append(b.Departures, d)
			}
			d, line = transit.StoredDeparture{}, nil
			return nil
		})
		return b, err
	})
}

// interchangeLines is today's station pairing and "Lines here"
// (STATION_DHIDS, STATION_TIMETABLE_STOPS, STATION_LINES_SQL): the zHV
// stops within $2 m of the StaDa points of the EVA numbers $1, the
// timetable stops of the DELFI timetable's feed (of the families $3) filed
// under them (the stop, its continuations, its children), and the stop
// families of those stops with their modes and lines. A row is a timetable
// stop ("feed:id"), or a family's modes and lines.
//
//nolint:misspell // materialized is the SQL keyword
var interchangeLines = named("api_train_interchange_lines", `
with d as materialized (
  select distinct z.external_id dhid
  from gazetteer_places s
  join gazetteer_places z on z.source_id = 'delfi_zhv' and z.kind = 'stop' and z.closed_at is null and z.geom is not null
                         and st_dwithin(z.geom, s.geom, (select $2::float8))
  where s.source_id = 'db_stada' and s.kind = 'station' and s.closed_at is null and s.geom is not null
    and s.external_id = any((select $1::text[])::text[])),
k as materialized (select g.gtfs_feed_key feed from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any((select $3::text[])::text[])),
t as materialized (
  select x.id, x.parent_id from k cross join d
  cross join lateral (
    select m.id, m.parent_id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.id = d.dhid
    union
    select m.id, m.parent_id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.id ~>=~ (d.dhid || ':') and m.id ~<~ (d.dhid || ';')
    union
    select m.id, m.parent_id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.id ~>=~ (d.dhid || '_') and m.id ~<~ (d.dhid || '`+"`"+`')
    union
    select m.id, m.parent_id from stops m where m.gtfs_feed_key = k.feed and m.deleted_at is null and m.parent_id = d.dhid) x)
select (k.feed || ':') || x.id, null::text, null::text[], null::text[] from k cross join (select distinct t.id from t) x
union all
select null, f.root_id, f.modes, f.lines
from transit_stop_modes f
where f.gtfs_feed_key = any(array(select k.feed from k))
  and f.root_id = any(array(select distinct coalesce(t.parent_id, t.id) from t))
order by 1 nulls last, 2`, "text[]", "float8", "text[]")

// callAt is a stop time's departure on its service day, as an instant
// (the timetable's day starts at noon minus twelve hours: on the day the
// clocks go back, the earlier of the two readings).
const callAt = `(case when ((((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour') at time zone 'Europe/Berlin') = (((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second'))
         then ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour' else ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') end)`

// interchangePlan is today's URBAN_RAIL_AT_STOPS_SQL on its tables: the
// U-Bahn and tram calls (route types 0, 1, 400 to 405, 900 to 906) of the
// timetable stops $2 of the feed $1 in [$3, $4), each stop's stop times
// read by the ranges of the window's time of day (stop_times_stop_tod);
// a trip must call at a stop outside $2 after it; one call of a line and
// destination a minute; the $5 last before $6 and the $5 first from it.
//
//nolint:misspell // materialized is the SQL keyword
var interchangePlan = named("api_train_interchange_plan", `
with fam as materialized (
  select f.stop_id, 'urban'::text grp from unnest((select $2::text[])) f(stop_id)),
fam_ids as materialized (select grp, array_agg(stop_id) ids from fam group by grp),
fam_tod as materialized (
  select f.stop_id, f.grp, nullif(btrim(s.platform_code), '') platform, tod.lo, tod.hi
  from fam f
  join stops s on s.gtfs_feed_key = $1::smallint and s.id = f.stop_id and s.deleted_at is null
  cross join (select r.lo, r.hi
    from (select mod(ceil(extract(epoch from (($3::timestamptz at time zone 'UTC') + interval '1 hour')::time))::bigint, 86400::bigint) c,
                 ceil(extract(epoch from ($4::timestamptz - $3::timestamptz)) + 3600)::bigint w) p
    cross join lateral (values
      (case when p.w >= 86400 then 0::bigint else p.c end,
       case when p.w >= 86400 then 86400::bigint else least(p.c + p.w, 86400::bigint) end),
      (0::bigint,
       case when p.w >= 86400 then 0::bigint else greatest(p.c + p.w - 86400, 0::bigint) end)) r(lo, hi)
    where r.hi > r.lo) tod),
win as materialized (
  select f.grp, st.trip_id, st.stop_id, st.stop_sequence, `+callAt+` at, sd.service_date, r.route_type,
         coalesce(nullif(btrim(r.short_name), ''), nullif(btrim(r.long_name), '')) line,
         nullif(btrim(t.trip_short_name), '') trip_short_name, nullif(btrim(t.headsign), '') destination, f.platform,
         nullif(btrim(r.extras->>'route_color'), '') route_color
  from fam_tod f
  join stop_times st on st.gtfs_feed_key = $1::smallint and st.stop_id = f.stop_id and st.deleted_at is null
                    and mod(date_part('epoch', st.departure)::bigint, 86400::bigint) >= f.lo
                    and mod(date_part('epoch', st.departure)::bigint, 86400::bigint) < f.hi
  join trips t on t.gtfs_feed_key = st.gtfs_feed_key and t.id = st.trip_id and t.deleted_at is null
  join routes r on r.gtfs_feed_key = t.gtfs_feed_key and r.id = t.route_id and r.deleted_at is null
  join gtfs_service_dates sd on sd.gtfs_feed_key = t.gtfs_feed_key and sd.service_id = t.service_id
  where st.departure is not null
    and st.pickup_type <> 1
    and (r.route_type in (0, 1) or r.route_type between 400 and 405 or r.route_type between 900 and 906)
    and (extract(epoch from ($4::timestamptz - $3::timestamptz)) + 3600 >= 86400
         or mod(mod(date_part('epoch', st.departure)::bigint
                    - ceil(extract(epoch from (($3::timestamptz at time zone 'UTC') + interval '1 hour')::time))::bigint, 86400) + 86400, 86400)
            < extract(epoch from ($4::timestamptz - $3::timestamptz)) + 3600)
    and `+callAt+` >= $3::timestamptz
    and `+callAt+` < $4::timestamptz),
called as (
  select w.grp, w.trip_id, w.stop_id, w.stop_sequence, w.at, w.service_date, w.route_type, w.line, w.trip_short_name,
         w.destination, w.platform, w.route_color
  from win w
  join fam_ids g on g.grp = w.grp
  where exists (select 1 from stop_times nx
                where nx.gtfs_feed_key = $1::smallint and nx.trip_id = w.trip_id and nx.deleted_at is null
                  and nx.stop_sequence > w.stop_sequence and nx.stop_id <> all(g.ids))),
same_line as (
  select distinct on (c.grp, c.at, c.line, c.destination, case when c.line is null or c.destination is null then c.stop_id end)
         c.grp, c.trip_id, c.stop_id, c.stop_sequence, c.at, c.service_date, c.route_type, c.line, c.trip_short_name,
         c.destination, c.platform, c.route_color
  from called c
  order by c.grp, c.at, c.line, c.destination, case when c.line is null or c.destination is null then c.stop_id end, c.trip_id),
ranked as (
  select s.grp, s.trip_id, s.stop_id, s.stop_sequence, s.at, s.service_date, s.route_type, s.line, s.trip_short_name,
         s.destination, s.platform, s.route_color,
         case when s.at >= $6::timestamptz
              then row_number() over (partition by s.grp, s.at >= $6::timestamptz order by s.at, s.line nulls last, s.trip_id)
              else row_number() over (partition by s.grp, s.at >= $6::timestamptz order by s.at desc, s.line nulls last, s.trip_id)
         end rn
  from same_line s)
select k.trip_id, k.stop_id, k.stop_sequence, k.at, k.service_date::text, k.route_type::int, k.line, k.trip_short_name,
       coalesce(k.destination,
                (select nullif(btrim(ls.name), '')
                 from stop_times lx
                 join stops ls on ls.gtfs_feed_key = lx.gtfs_feed_key and ls.id = lx.stop_id and ls.deleted_at is null
                 where lx.gtfs_feed_key = $1::smallint and lx.trip_id = k.trip_id and lx.deleted_at is null
                 order by lx.stop_sequence desc
                 limit 1)),
       k.platform, k.route_color
from ranked k
where k.rn <= $5::int
order by k.grp, k.at, k.line nulls last, k.trip_id`, "smallint", "text[]", "timestamptz", "timestamptz", "int", "timestamptz")

// tripUpdatesFeed is the feed_checks key of the DELFI Trip Updates.
const tripUpdatesFeed = "tripupdates"

// feedCheck is today's LIVE_FEEDS_CHECKED_SQL of the feed $1: its last
// check, cadence and whether its refresh is paused.
var feedCheck = named("api_feed_check", `
select f.checked_at, f.cadence_s, coalesce(d.paused, false)
from feed_checks f
left join control.data_status d on d.ledger = 'feed' and d.key = f.feed_key
where f.feed_key = (select $1::text)`, "text")

// tripUpdatesOf is today's TRIP_UPDATES_SQL on its table: the current
// state of each stop of the trips $1 (and of each trip, stop sequence
// null) in the DELFI Trip Updates of the last six hours, at most
// transit.TripUpdatesRead rows.
var tripUpdatesOf = named("api_trip_updates_of", `
select u.trip_id, u.stop_id, u.stop_sequence, u.delay_s, u.schedule_relationship, u.event_at, u.start_date, u.observed_at
from (
  select distinct on (d.trip_id, d.stop_sequence) d.trip_id, d.stop_id, d.stop_sequence, d.delay_s, d.schedule_relationship,
         case when jsonb_typeof(d.raw -> 'departure_time') = 'number' then to_timestamp((d.raw ->> 'departure_time')::float8)
              when jsonb_typeof(d.raw -> 'arrival_time') = 'number' then to_timestamp((d.raw ->> 'arrival_time')::float8) end event_at,
         case when d.stop_sequence is null then d.raw ->> 'start_date' end start_date, d.observed_at
  from transit_delays d
  where d.observed_at > now() - interval '6 hours' and d.observed_at <= now() and d.source = 'delfi_rt_tu'
    and d.trip_id = any((select $1::text[])::text[])
  order by d.trip_id, d.stop_sequence, d.observed_at desc) u
order by u.trip_id, u.stop_sequence nulls first
limit `+strconv.Itoa(transit.TripUpdatesRead), "text[]")

// TrainInterchange reads the interchange of the station of the EVA numbers
// evas (features.TrainBoards): its stop families' lines, the U-Bahn and
// tram calls of its timetable stops in [from, to) around now, and, when
// the DELFI Trip Updates are live and among the families, their rows of
// those calls' trips.
func (s *Store) TrainInterchange(ctx context.Context, evas []string, from, to, now time.Time, families []string) (transit.InterchangeRead, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.InterchangeRead, error) {
		var out transit.InterchangeRead
		rows, _ := run(ctx, tx, interchangeLines, evas, float64(transit.StationPairingM), families)
		var (
			stop, fam    *string
			modes, lines []string
			feed         string
			ids          []string
		)
		_, err := pgx.ForEachRow(rows, []any{&stop, &fam, &modes, &lines}, func() error {
			if stop != nil {
				var id string
				feed, id, _ = strings.Cut(*stop, ":")
				ids = append(ids, id)
			} else {
				out.Lines = append(out.Lines, transit.StopFamilyLines{Modes: modes, Lines: lines})
			}
			stop, modes, lines = nil, nil, nil
			return nil
		})
		if err != nil || len(ids) == 0 {
			return out, err
		}
		out.Stops = true
		rows, _ = run(ctx, tx, interchangePlan, feed, ids, from, to, transit.InterchangePlanRead, now)
		var v transit.PlannedCall
		_, err = pgx.ForEachRow(rows, []any{&v.TripID, &v.StopID, &v.StopSequence, &v.At, &v.ServiceDate, &v.RouteType, &v.Line,
			&v.TripShortName, &v.Destination, &v.Platform, &v.RouteColor}, func() error {
			out.Plan = append(out.Plan, v)
			v = transit.PlannedCall{}
			return nil
		})
		if err != nil || len(out.Plan) == 0 || !slices.Contains(families, transit.TripUpdatesFamily) {
			return out, err
		}
		var c transit.FeedCheck
		if err := runRow(ctx, tx, feedCheck, tripUpdatesFeed).Scan(&c.CheckedAt, &c.CadenceS, &c.Paused); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if out.TripUpdatesLive = transit.FeedLive(&c, now); !out.TripUpdatesLive {
			return out, nil
		}
		var trips []string
		for _, p := range out.Plan {
			trips = append(trips, p.TripID)
		}
		slices.Sort(trips)
		rows, _ = run(ctx, tx, tripUpdatesOf, slices.Compact(trips))
		var u transit.TripUpdateRow
		_, err = pgx.ForEachRow(rows, []any{&u.TripID, &u.StopID, &u.StopSequence, &u.DelayS, &u.Relationship, &u.EventAt, &u.StartDate, &u.ObservedAt}, func() error {
			out.TripUpdates = append(out.TripUpdates, u)
			u = transit.TripUpdateRow{}
			return nil
		})
		return out, err
	})
}
