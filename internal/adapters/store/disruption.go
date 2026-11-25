package store

import (
	"context"
	"errors"
	"fmt"
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
//
//nolint:misspell // materialized is the SQL keyword
const regionAlerts = `in_force as materialized (
  select a.alert_id, a.header, a.description, a.cause, a.effect, a.url, a.active_from, a.active_to, a.line_names, a.district_codes, a.state_codes, a.placed_by, a.dedupe_key, a.source_ref, a.changed_at, a.message_ref, a.primary_source
  from (` + relServiceAlert + `) a
  where a.source_ref = any($2) and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `
    and ` + "{live}" + ` and ` + "{inforce}" + `
), o as (
  select f.alert_id, f.header, f.description, f.cause, f.effect, f.url, f.active_from, f.active_to, f.line_names, f.district_codes, f.state_codes, f.placed_by, f.dedupe_key, f.source_ref, f.changed_at, f.message_ref, f.primary_source,
         (not f.primary_source and exists (
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
var serviceAlerts = namedOpaque("api_service_alerts", `with `+alertsCTE+`
select o.alert_id, o.header, o.description, o.cause, o.effect, o.url, o.active_from, o.active_to,
       o.line_names, o.placed_by, o.changed_at, o.source_ref
from o where not o.duplicate
order by case o.placed_by when 'stop' then 0 when 'route' then 1 else 2 end, o.active_from desc nulls last, o.alert_id
limit $5`, "timestamp with time zone", "text[]", "text", "text", "bigint")

// alertsSummary reads whether a publication of the families was alive to
// ask (an open alert of one), the region's alerts in force less the
// duplicates, the duplicates left out, and the families of every row
// counted, as the platform credits them.
var alertsSummary = namedOpaque("api_alerts_summary", `with `+alertsCTE+`
select exists (select 1 from (`+relServiceAlert+`) a where a.source_ref = any($2) and `+alertLive("a", "$1")+`),
       (count(*) filter (where not o.duplicate))::int, (count(*) filter (where o.duplicate))::int,
       coalesce(array_agg(distinct o.source_ref order by o.source_ref), '{}')
from o where $5::int >= 0 -- $5, the limit, is the list's alone`, "timestamp with time zone", "text[]", "text", "text", "integer")

var alertsCTE = strings.NewReplacer("{live}", alertLive("a", "$1"), "{inforce}", alertInForce("a", "$1")).Replace(regionAlerts)

// alertsCoverage reads the alerts coverage of the area's Land
// (relAlertsCoverage).
var alertsCoverage = named("api_alerts_coverage", relAlertsCoverage, "text", "timestamptz", "text[]")

// areaPunctuality reads the punctuality of the area $1, of the families $2,
// from the Land of the area alone (areaBound).
var areaPunctuality = namedOpaque("api_area_punctuality", `select p.area_level, p.area_code, p.area_name, p.trips_observed, p.trips_late, p.trips_skipped_stop, p.trips_untimed,
       p.median_worst_delay_s, p.stops_seen, p.observed_at, p.source_ref
from (`+areaBound(relTransitPunctuality)+`) p
where p.area_code = $1 and p.source_ref = any($2) and `+permitted("p")+`
order by p.area_level
limit 1`, "text", "text[]")

// areaTrips counts the trips observed at the stops whose official key starts
// with $1, and those more than $2 minutes late at one of them, of the
// families $3: the platform's region watch count.
var areaTrips = namedOpaque("api_area_trips", `select count(distinct t.trip_ref)::int, (count(distinct t.trip_ref) filter (where t.worst_delay_s > $2::int * 60))::int,
       max(t.observed_at), coalesce(min(t.source_ref), '')
from (`+tripDelaysOfLand(relTransitTripDelay)+`) t
where starts_with(t.area_code, $1) and t.source_ref = any($3) and `+permitted("t"), "text", "integer", "text[]")

// tripDelaysOfLand is the trip delays relation rel bound to the Land of the
// area $1 (landOfDelay); it panics at init when the relation no longer
// holds the one place the bound goes, so the bound is never dropped quietly.
func tripDelaysOfLand(rel string) string {
	const at = "WHERE d.source = 'delfi_rt_tu'::text AND"
	if n := strings.Count(rel, at); n != 1 {
		panic(fmt.Sprintf("store: the trip delays relation holds %q %d times, not once: the Land bound has no place", at, n))
	}
	return strings.Replace(rel, at, "WHERE "+landOfDelay+" = left($1::text, 2) AND "+at[len("WHERE "):], 1)
}

// landOfDelay is the Land of a Trip Updates delay d, as the delays' index
// of the Land and time keys it (its expression exactly, so the index is
// read): the area key's first two digits. An area's delays are its Land's,
// and the 30 minutes of every Land's delays were read and grouped to count
// one area's trips, 0.6 to 1.7 s on prod (2026-11-24).
const landOfDelay = `"left"(CASE WHEN ((d.stop_id ~* '^de:[0-9]+(:|$)'::text) AND (length(split_part(d.stop_id, ':'::text, 2)) = ANY (ARRAY[2, 3, 5, 8]))) THEN split_part(d.stop_id, ':'::text, 2) ELSE NULL::text END, 2)`

// lineInTimetable reads whether a line of the families $2 may be read at all
// (a timetable to ask), and the families whose line of the folded name $1
// is in their timetable.
var lineInTimetable = namedOpaque("api_line_in_timetable", `select exists (select 1 from (`+relTransitLine+`) l where l.source_ref = any($2) and `+permitted("l")+`),
       coalesce((select array_agg(distinct l.source_ref order by l.source_ref) from (`+relTransitLine+`) l
                  where upper(regexp_replace(l.line_name, '\s+', '', 'g')) = $1::text and l.source_ref = any($2) and `+permitted("l")+`), '{}')`, "text", "text[]")

// punctualityFamily is the family contract.transit_punctuality is of (its
// source_ref): the DELFI Trip Updates under its licence's id, not the
// family the register publishes (tripUpdatesFamily), so a key holding the
// published family is given no area punctuality. Stamped with the
// published family, the read is served and takes 2 to 6 s on prod
// (2026-11-24): transit_punctuality_area_live counts all sixteen Länder in
// a materialised CTE no area filter reaches, and the API's role may not
// read the untimed-trips relation a read of one Land would need. It
// waits for the database to give a Land's punctuality.
const punctualityFamily = "delfi_rt_tu"

// Disruptions reads the alerts and the punctuality q asks for
// (features.Disruptions).
func (s *Store) Disruptions(ctx context.Context, q transit.DisruptionQuery, families []string) (transit.Disruptions, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (transit.Disruptions, error) {
		var out transit.Disruptions
		args := []any{q.Now, families, nullText(q.Area), nullText(q.Line), q.Limit}
		if err := runRow(ctx, tx, alertsSummary, args...).Scan(&out.AlertsChecked, &out.AlertsTotal, &out.AlertsDuplicates, &out.AlertFamilies); err != nil {
			return out, err
		}
		rows, _ := run(ctx, tx, serviceAlerts, args...)
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
		if q.Line != "" {
			var l transit.LineCheck
			if err := runRow(ctx, tx, lineInTimetable, q.Line, families).Scan(&l.Checked, &l.Families); err != nil {
				return out, err
			}
			l.InTimetable = len(l.Families) > 0
			out.Line = &l
		}
		if q.Area == "" {
			return out, nil
		}
		cov := transit.AlertsCoverage{LookbackDays: transit.AlertsLookbackDays, Covered: len(alerts) > 0,
			Lands: []transit.LandCoverage{{Land: transit.LandOf(q.Area)}}}
		since := q.Now.Add(-time.Duration(transit.AlertsLookbackDays) * 24 * time.Hour)
		if err := runRow(ctx, tx, alertsCoverage, q.Area, since, families).Scan(&cov.Lands[0].Land, &cov.Lands[0].AlertsSeen, &cov.Lands[0].LastSeenAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		cov.Covered = cov.Covered || cov.Lands[0].AlertsSeen > 0
		out.Coverage = &cov
		for _, f := range families {
			if f == punctualityFamily {
				out.PunctualityRead = append(out.PunctualityRead, f)
			}
		}
		rows, _ = run(ctx, tx, areaPunctuality, q.Area, families)
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
		if err := runRow(ctx, tx, areaTrips, q.Area, q.LateOverMin, families).Scan(&c.Observed, &c.Late, &at, &c.Family); err != nil {
			return out, err
		}
		if c.Observed > 0 {
			c.ObservedAt = at.UTC()
			out.Trips = &c
		}
		return out, nil
	})
}

// areaBound is the punctuality relation rel read from the Land of the area
// $1 alone (punctualityOfArea) in place of the view of every Land; it
// panics at init when rel no longer reads the view once.
func areaBound(rel string) string {
	const view = "FROM transit_punctuality_area_live p"
	if n := strings.Count(rel, view); n != 1 {
		panic(fmt.Sprintf("store: the punctuality relation reads %q %d times, not once: the area bound has no place", view, n))
	}
	return strings.Replace(rel, view, "FROM ("+punctualityOfArea+") p", 1)
}

// punctualityOfArea is the platform's punctuality of every area
// (transit_punctuality_area_live, its definition at schema head 348) with
// its Länder bound to the one the area $1 lies in (the first two digits of
// its key; every Land for the nation, 'de'): the view counts each Land in
// its own lateral read and derives its Kreise and cities from that Land's
// alone, so one Land's rows are the view's rows of that Land. The view
// counted all sixteen in a materialised CTE no area filter reached, 2.9 to
// 9.6 s on prod for Berlin; bound, 26 to 55 ms (2026-11-25).
var punctualityOfArea = landsOfArea(punctualityView)

// landsOfArea binds the Länder of the punctuality definition def to the area
// $1; it panics at init when def no longer lists the sixteen Länder once,
// so the bound is never dropped quietly.
func landsOfArea(def string) string {
	const all = "( VALUES ('01'::text), ('02'::text), ('03'::text), ('04'::text), ('05'::text), ('06'::text), ('07'::text), ('08'::text), ('09'::text), ('10'::text), ('11'::text), ('12'::text), ('13'::text), ('14'::text), ('15'::text), ('16'::text)) l(land)"
	if n := strings.Count(def, all); n != 1 {
		panic(fmt.Sprintf("store: the punctuality definition lists the Länder %d times, not once: the area bound has no place", n))
	}
	return strings.Replace(def, all, `unnest(CASE WHEN $1::text = 'de'::text
               THEN ARRAY['01'::text, '02'::text, '03'::text, '04'::text, '05'::text, '06'::text, '07'::text, '08'::text,
                          '09'::text, '10'::text, '11'::text, '12'::text, '13'::text, '14'::text, '15'::text, '16'::text]
               ELSE ARRAY["left"($1::text, 2)] END) l(land)`, 1)
}
