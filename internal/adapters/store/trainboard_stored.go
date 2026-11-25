package store

import (
	"context"
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
// families of those stops with their modes and lines. A row is a DHID's
// root ("feed:dhid"), or a family's modes and lines.
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
select (k.feed || ':') || d.dhid, null::text, null::text[], null::text[] from k cross join d
union all
select null, f.root_id, f.modes, f.lines
from transit_stop_modes f
where f.gtfs_feed_key = any(array(select k.feed from k))
  and f.root_id = any(array(select distinct coalesce(t.parent_id, t.id) from t))
order by 1 nulls last, 2`, "text[]", "float8", "text[]")

// TrainInterchange reads the interchange of the station of the EVA numbers
// evas (features.TrainBoards): its stop families' lines, and the departing
// U-Bahn and tram calls of its timetable stops in [from, to).
func (s *Store) TrainInterchange(ctx context.Context, evas []string, from, to time.Time, families []string) (transit.InterchangeRead, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.InterchangeRead, error) {
		var out transit.InterchangeRead
		rows, _ := run(ctx, tx, interchangeLines, evas, float64(transit.StationPairingM), families)
		var (
			root, fam    *string
			modes, lines []string
			roots        []string
		)
		_, err := pgx.ForEachRow(rows, []any{&root, &fam, &modes, &lines}, func() error {
			if root != nil {
				roots = append(roots, *root)
			} else {
				out.Lines = append(out.Lines, transit.StopFamilyLines{Modes: modes, Lines: lines})
			}
			root, modes, lines = nil, nil, nil
			return nil
		})
		if err != nil || len(roots) == 0 {
			return out, err
		}
		out.Stops = true
		out.Calls, err = urbanRailCallsOf(ctx, tx, roots, from, to, families)
		return out, err
	})
}
