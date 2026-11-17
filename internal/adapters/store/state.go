package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// stateWarnings are the warnings of the families ($1) in force at $2 in the
// Land $3, one row per event, as the platform's Land page reads them: of the
// Gemeinde and coast or district cells (ids 8… and 7…) with an area, at level
// 1 or above, the Land the cell's own key (digits 2 and 3); the highest
// level, the warnings by identifier, the cells, the latest end. Worst first,
// then by event.
var stateWarnings = `
select w.event, max(w.warning_level)::int, count(distinct w.identifier)::int, count(distinct w.warn_cell_id)::int,
       max(w.valid_to), array_agg(distinct w.source_ref order by w.source_ref)
from contract.weather_warning w
where w.source_ref = any($1) and ` + permitted("w") + `
  and w.withdrawn_at is null and w.area is not null
  and (w.valid_to is null or w.valid_to > $2::timestamptz) and w.valid_from <= $2::timestamptz
  and left(w.warn_cell_id, 1) in ('7', '8') and substring(w.warn_cell_id from 2 for 2) = $3
  and w.warning_level >= 1
group by w.event
order by 2 desc, 1`

// StateWarnings returns the warnings of the families in force at now in the
// Land, one per event (features.States).
func (s *Store) StateWarnings(ctx context.Context, land string, families []string, now time.Time) ([]weather.StateWarning, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]weather.StateWarning, error) {
		rows, _ := tx.Query(ctx, stateWarnings, families, now, land)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (weather.StateWarning, error) {
			var w weather.StateWarning
			return w, r.Scan(&w.Event, &w.Level, &w.Notices, &w.Cells, &w.Until, &w.Families)
		})
	})
}

// stateAlerts are the service alerts of the families ($1) in force at $2
// (the published periods, none meaning always) that name a stop, with the
// message each belongs to and the Länder of the districts their stops lie
// in, of the messages touching the Land $3, as the platform's Land page reads
// them. Rows sharing a dedupe key are one message; its key is the row of the
// primary family (the GTFS-RT alerts) where one carries it, and every family
// carrying it is listed.
var stateAlerts = `
with f as materialized (
  select a.alert_id, a.header, a.active_to, coalesce(a.line_names, '{}') as line_names, a.dedupe_key, a.source_ref,
         array(select distinct left(k, 2) from unnest(a.district_codes) k where k ~ '^[0-9]{5}$' order by 1) as lands
  from contract.service_alert a
  where a.source_ref = any($1) and ` + permitted("a") + `
    and cardinality(a.district_codes) > 0
    and (jsonb_array_length(coalesce(a.active_periods, '[]'::jsonb)) = 0
         or exists (select 1 from jsonb_array_elements(a.active_periods) p
                     where (p->>'start' is null or (p->>'start')::timestamptz <= $2::timestamptz)
                       and (p->>'end' is null or (p->>'end')::timestamptz > $2::timestamptz)))
), k as (
  select o.alert_id, o.header, o.active_to, o.line_names, o.lands,
         coalesce((select g.alert_id from f g where g.dedupe_key = o.dedupe_key
                    order by (g.source_ref <> 'delfi_rt_sa'), g.source_ref, g.alert_id limit 1), o.alert_id) as message_key,
         coalesce((select array_agg(distinct g.source_ref order by g.source_ref) from f g where g.dedupe_key = o.dedupe_key),
                  array[o.source_ref]) as carried_by
  from f o
)
select k.alert_id, k.header, k.active_to, k.line_names, k.message_key, k.carried_by, k.lands
from k
where k.message_key in (select m.message_key from k m where $3 = any(m.lands))
order by k.message_key, k.alert_id`

// StateAlerts returns the alert rows of the messages in force at now whose
// stops lie in the Land, of the families (features.States); the caller folds
// them per message (transit.StateAlertsOf).
func (s *Store) StateAlerts(ctx context.Context, land string, families []string, now time.Time) ([]transit.StateAlertRow, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]transit.StateAlertRow, error) {
		rows, _ := tx.Query(ctx, stateAlerts, families, now, land)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.StateAlertRow, error) {
			var a transit.StateAlertRow
			return a, r.Scan(&a.ID, &a.Header, &a.Until, &a.Lines, &a.MessageKey, &a.CarriedBy, &a.Lands)
		})
	})
}
