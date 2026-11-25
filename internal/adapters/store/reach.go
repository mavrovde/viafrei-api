package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// timetableReach reads how far the loaded timetable reaches, as today's
// board reads it (HORIZON_SQL): the newest loaded run of the timetable
// (status ok or degraded) and the last service day it wrote, from its note,
// else the last day the calendar holds; the reach ends at the Berlin
// midnight after that day. With no run recorded, the calendar alone, and
// no load time; with no calendar, nothing.
var timetableReach = named("api_timetable_reach", `
with run as (
  select r.started_at,
         substring(r.note from '"w":"[0-9]{4}-[0-9]{2}-[0-9]{2}[.][.]([0-9]{4}-[0-9]{2}-[0-9]{2})"')::date wrote_to
  from ingest_runs r
  where r.provider = 'delfi_static' and r.status in ('ok', 'degraded')
  order by r.started_at desc
  limit 1),
bounds as (select max(d.service_date) last_date from gtfs_service_dates d where d.gtfs_feed_key = 1)
select (coalesce((select wrote_to from run), b.last_date) + 1)::timestamp at time zone 'Europe/Berlin', (select started_at from run)
from bounds b`)

// TimetableReach reads how far the loaded timetable reaches
// (features.Departures).
func (s *Store) TimetableReach(ctx context.Context) (transit.Reach, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.Reach, error) {
		var r transit.Reach
		err := scanOne(run(ctx, tx, timetableReach))(&r.Until, &r.LoadedAt)
		return r, err
	})
}
