package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// serviceAlerts reads the alerts in force at $1 of the families $2 in the
// area $3 (null: every area) for the folded line $4 (null: every line), at
// most $5, as the platform's region read: an alert of a Kreis of the area,
// or of the whole network of the Land asked; stop alerts first, then route,
// then network, each newest first.
var serviceAlerts = `select a.alert_id, a.header, a.description, a.cause, a.effect, a.url, a.active_from, a.active_to,
       a.line_names, a.placed_by, a.changed_at, a.source_ref, count(*) over ()::int
from contract.service_alert a
where a.source_ref = any($2) and ` + permitted("a") + `
  and (jsonb_array_length(coalesce(a.active_periods, '[]'::jsonb)) = 0
       or exists (select 1 from jsonb_array_elements(a.active_periods) p
                   where (p->>'start' is null or (p->>'start')::timestamptz <= $1::timestamptz)
                     and (p->>'end' is null or (p->>'end')::timestamptz > $1::timestamptz)))
  and ($3::text is null
       or exists (select 1 from unnest(a.district_codes) k where k like $3::text || '%' or (length($3::text) > 5 and $3::text like k || '%'))
       or (a.placed_by = 'network' and length($3::text) = 2 and $3::text = any(a.state_codes)))
  and ($4::text is null or exists (select 1 from unnest(a.line_names) n where upper(regexp_replace(n, '\s+', '', 'g')) = $4::text))
order by case a.placed_by when 'stop' then 0 when 'route' then 1 else 2 end, a.active_from desc nulls last, a.alert_id
limit $5`

// areaPunctuality reads the punctuality of the area $1, of the families $2.
var areaPunctuality = `select p.area_level, p.area_code, p.area_name, p.trips_observed, p.trips_late, p.trips_skipped_stop, p.trips_untimed,
       p.median_worst_delay_s, p.stops_seen, p.observed_at, p.source_ref
from contract.transit_punctuality p
where p.area_code = $1 and p.source_ref = any($2) and ` + permitted("p") + `
order by p.area_level
limit 1`

// Disruptions reads the alerts and the punctuality q asks for
// (features.Disruptions).
func (s *Store) Disruptions(ctx context.Context, q transit.DisruptionQuery, families []string) (transit.Disruptions, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.Disruptions, error) {
		var out transit.Disruptions
		rows, _ := tx.Query(ctx, serviceAlerts, q.Now, families, nullText(q.Area), nullText(q.Line), q.Limit)
		alerts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Alert, error) {
			var a transit.Alert
			err := r.Scan(&a.ID, &a.Header, &a.Description, &a.Cause, &a.Effect, &a.URL, &a.ActiveFrom, &a.ActiveTo,
				&a.Lines, &a.PlacedBy, &a.ChangedAt, &a.Family, &out.AlertsTotal)
			return a, err
		})
		if err != nil {
			return out, err
		}
		out.Alerts = alerts
		if q.Area == "" {
			return out, nil
		}
		rows, _ = tx.Query(ctx, areaPunctuality, q.Area, families)
		ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Punctuality, error) {
			var p transit.Punctuality
			err := r.Scan(&p.AreaLevel, &p.AreaCode, &p.AreaName, &p.TripsObserved, &p.TripsLate, &p.TripsSkipping, &p.TripsUntimed,
				&p.MedianWorstDelay, &p.StopsSeen, &p.ObservedAt, &p.Family)
			return p, err
		})
		if len(ps) > 0 {
			out.Punctuality = &ps[0]
		}
		return out, err
	})
}
