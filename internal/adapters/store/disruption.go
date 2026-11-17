package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// alertLive is the platform's gate on an alert row at the instant $at: its
// publication alive (its last check within three of its cadences, 600 s
// when it states none, or its refresh paused after a check), and a row of the
// secondary publication delivered again within the last day (absence never closes one).
func alertLive(a, at string) string {
	return `(` + a + `.checked_at is not null and (` + a + `.paused or ` + a + `.checked_at >= ` + at +
		`::timestamptz - make_interval(secs => 3 * coalesce(nullif(` + a + `.cadence_s, 0), 600))))
  and (` + a + `.primary_source or ` + a + `.seen_at > ` + at + `::timestamptz - interval '86400 seconds')`
}

// alertInForce is whether one of the alert's published periods covers $at
// (none meaning always).
func alertInForce(a, at string) string {
	return `(jsonb_array_length(coalesce(` + a + `.active_periods, '[]'::jsonb)) = 0
       or exists (select 1 from jsonb_array_elements(` + a + `.active_periods) p
                   where (p->>'start' is null or (p->>'start')::timestamptz <= ` + at + `::timestamptz)
                     and (p->>'end' is null or (p->>'end')::timestamptz > ` + at + `::timestamptz)))`
}

// regionAlerts are the alerts in force at $1 of the families $2 from a live
// publication, as the platform's region read: in the area $3 (null: every
// area) for the folded line $4 (null: every line), an alert of a Kreis of
// the area, or of the whole network of the Land asked. A row of the
// secondary publication carrying the dedupe key or the message number of a
// primary row in force
// is that row's duplicate: flagged, and left out of the list.
const regionAlerts = `in_force as materialized (
  select a.* from contract.service_alert a
  where a.source_ref = any($2) and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `
    and ` + "{live}" + ` and ` + "{inforce}" + `
), o as (
  select f.*, (not f.primary_source and exists (
           select 1 from in_force g where g.primary_source
              and (g.dedupe_key = f.dedupe_key or (f.message_ref is not null and g.message_ref = f.message_ref)))) as duplicate
  from in_force f
  where ($3::text is null
         or exists (select 1 from unnest(f.district_codes) k where k like $3::text || '%' or (length($3::text) > 5 and $3::text like k || '%'))
         or (f.placed_by = 'network' and length($3::text) = 2 and $3::text = any(f.state_codes)))
    and ($4::text is null or exists (select 1 from unnest(f.line_names) n where upper(regexp_replace(n, '\s+', '', 'g')) = $4::text))
)`

// serviceAlerts reads at most $5 of the region's alerts, duplicates left
// out: stop alerts first, then route, then network, each newest first.
var serviceAlerts = `with ` + alertsCTE + `
select o.alert_id, o.header, o.description, o.cause, o.effect, o.url, o.active_from, o.active_to,
       o.line_names, o.placed_by, o.changed_at, o.source_ref
from o where not o.duplicate
order by case o.placed_by when 'stop' then 0 when 'route' then 1 else 2 end, o.active_from desc nulls last, o.alert_id
limit $5`

// alertsSummary reads whether a publication of the families was alive to
// ask (an open alert of one), the region's alerts in force less the
// duplicates, the duplicates left out, and the families of every row
// counted, as the platform credits them.
var alertsSummary = `with ` + alertsCTE + `
select exists (select 1 from contract.service_alert a where a.source_ref = any($2) and ` + alertLive("a", "$1") + `),
       (count(*) filter (where not o.duplicate))::int, (count(*) filter (where o.duplicate))::int,
       coalesce(array_agg(distinct o.source_ref order by o.source_ref), '{}')
from o where $5::int >= 0 -- $5, the limit, is the list's alone`

var alertsCTE = strings.NewReplacer("{live}", alertLive("a", "$1"), "{inforce}", alertInForce("a", "$1")).Replace(regionAlerts)

// areaPunctuality reads the punctuality of the area $1, of the families $2.
var areaPunctuality = `select p.area_level, p.area_code, p.area_name, p.trips_observed, p.trips_late, p.trips_skipped_stop, p.trips_untimed,
       p.median_worst_delay_s, p.stops_seen, p.observed_at, p.source_ref
from contract.transit_punctuality p
where p.area_code = $1 and p.source_ref = any($2) and ` + permitted("p") + `
order by p.area_level
limit 1`

// areaTrips counts the trips observed at the stops whose official key starts
// with $1, and those more than $2 minutes late at one of them, of the
// families $3: the platform's region watch count.
var areaTrips = `select count(distinct t.trip_ref)::int, (count(distinct t.trip_ref) filter (where t.worst_delay_s > $2::int * 60))::int,
       max(t.observed_at), coalesce(min(t.source_ref), '')
from contract.transit_trip_delay t
where starts_with(t.area_code, $1) and t.source_ref = any($3) and ` + permitted("t")

// punctualityFamily is the family contract.transit_punctuality is of (its
// source_ref): the DELFI Trip Updates.
const punctualityFamily = "delfi_rt_tu"

// Disruptions reads the alerts and the punctuality q asks for
// (features.Disruptions).
func (s *Store) Disruptions(ctx context.Context, q transit.DisruptionQuery, families []string) (transit.Disruptions, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.Disruptions, error) {
		var out transit.Disruptions
		args := []any{q.Now, families, nullText(q.Area), nullText(q.Line), q.Limit}
		if err := tx.QueryRow(ctx, alertsSummary, args...).Scan(&out.AlertsChecked, &out.AlertsTotal, &out.AlertsDuplicates, &out.AlertFamilies); err != nil {
			return out, err
		}
		rows, _ := tx.Query(ctx, serviceAlerts, args...)
		alerts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.Alert, error) {
			var a transit.Alert
			err := r.Scan(&a.ID, &a.Header, &a.Description, &a.Cause, &a.Effect, &a.URL, &a.ActiveFrom, &a.ActiveTo,
				&a.Lines, &a.PlacedBy, &a.ChangedAt, &a.Family)
			return a, err
		})
		if err != nil {
			return out, err
		}
		out.Alerts = alerts
		if q.Area == "" {
			return out, nil
		}
		for _, f := range families {
			if f == punctualityFamily {
				out.PunctualityRead = append(out.PunctualityRead, f)
			}
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
		if err != nil || q.LateOverMin <= 0 {
			return out, err
		}
		var (
			c  transit.TripCount
			at *time.Time
		)
		if err := tx.QueryRow(ctx, areaTrips, q.Area, q.LateOverMin, families).Scan(&c.Observed, &c.Late, &at, &c.Family); err != nil {
			return out, err
		}
		if c.Observed > 0 {
			c.ObservedAt = at.UTC()
			out.Trips = &c
		}
		return out, nil
	})
}
