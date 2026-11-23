package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/national"
)

// sourceRules are the families whose rows a reader may see, by the
// platform's source rules (the first pattern a source matches, by priority;
// no pattern = open and public): not excluded, not withheld, public or an
// own key. A charging site or point carries its source's rule, so the rule
// is matched once per source, not once per row as the relations do (most
// of the charging summary's seconds on prod). A site is one row of its
// source and id, and a point's site is its source and site id: the ids the
// relations join, source || ':' || id, are the same pairs, a source name
// holding no colon. The sources are the families ($1): the summary reads
// only their sites, and a point only with its site (listing the sources of
// every site and point took most of a second on prod).
const sourceRules = `
select src.feed
from (select distinct f as feed from unnest($1::text[]) f) src
left join lateral (
  select k.excluded, k.display_class, k.audience from contract_source_rules k
  where src.feed ~ k.pattern order by k.priority limit 1) k on true
where not coalesce(k.excluded, false) and coalesce(k.display_class, 'open') <> 'withhold'
  and (coalesce(k.audience, 'public') = 'public' or ` + ownKeyOn + `)`

// The national picture, one statement per domain: $1 the families, $2 the
// instant; the energy statement's $3 is the fuel price window in seconds.
// Each row is section, key, name, family, n, m, low, mid.
//
//nolint:misspell // materialized is the SQL keyword
var nationalSQL = map[string]stmt{
	"roads": namedOpaque("api_national_roads", `
with e as materialized (
  select e.event_kind, e.state_code, e.source_ref
  from (`+trafficEventsOfFamilies+`) e
  where `+nationalInForce+`)
select 'situations', e.event_kind, null::text, e.source_ref, count(*)::int, null::int, null::float8, null::float8
from e
group by 2, 4
union all
select 'states', e.state_code, null, e.source_ref, count(*)::int, null, null, null
from e
where e.state_code is not null
group by 2, 4
order by 1, 2, 4`, "text[]", "timestamp with time zone"),
	"energy": namedOpaque("api_national_energy", `
with rules as materialized (`+sourceRules+`),
latest as (
  select distinct on (o.station_id, o.product_code) o.product_code, o.price, o.source_ref
  from (`+relFuelPriceObservation+`) o
  join (`+relFuelStation+`) s on s.station_id = o.station_id and s.withdrawn_at is null and s.source_ref = any($1) and `+permitted("s")+`
  where o.price is not null and o.source_ref = any($1) and `+permitted("o")+`
    and o.observed_at > $2::timestamptz - make_interval(secs => $3::int) and o.observed_at <= $2::timestamptz
  order by o.station_id, o.product_code, o.observed_at desc)
select 'fuel', l.product_code, null::text, l.source_ref, count(*)::int, null::int,
       min(l.price)::float8, (percentile_cont(0.5) within group (order by l.price))::float8
from latest l
group by 2, 4
union all
select 'charging', c.source_feed, null, c.source_feed, count(*)::int, coalesce(sum(p.n), 0)::int, null, null
from charging_sites c
join rules r on r.feed = c.source_feed
left join (
  select p.source_feed, p.site_id, count(*) filter (where p.evse_id is not null or p.point_key is not null) as n
  from charging_points p
  join rules r on r.feed = p.source_feed
  where p.deleted_at is null and p.site_id is not null
    and coalesce(p.confidentiality, 'noRestriction') = 'noRestriction'
  group by 1, 2) p on p.source_feed = c.source_feed and p.site_id = c.site_id
where c.deleted_at is null and c.geom is not null and c.closed_at is null and c.source_feed = any($1)
  and coalesce(c.confidentiality, 'noRestriction') = 'noRestriction' and not coalesce(c.access_private, false)
group by 2, 4
order by 1, 2, 4`, "text[]", "timestamp with time zone", "integer"),
	"parking": namedOpaque("api_national_parking", `
select 'parking', f.parking_kind, null::text, f.source_ref, count(*)::int, null::int, null::float8, null::float8
from (`+relParkingFacility+`) f
where f.withdrawn_at is null and f.source_ref = any($1) and `+permitted("f")+` and $2::timestamptz is not null
group by 2, 4
order by 1, 2, 4`, "text[]", "timestamp with time zone"),
	"transit": namedOpaque("api_national_transit", `
select 'punctuality', p.area_level || ':' || p.area_code, p.area_name, p.source_ref, p.trips_observed, p.trips_late,
       null, p.median_worst_delay_s::float8
from (`+relTransitPunctuality+`) p
where p.source_ref = any($1) and `+permitted("p")+` and $2::timestamptz is not null
order by 1, 2, 4`, "text[]", "timestamp with time zone"),
	"accessibility": namedOpaque("api_national_accessibility", `
with f as (
  select e.kind as facility_kind, coalesce(st.state, 'unknown') as facility_status,
         coalesce(st.source_feed, e.source_feed) as source_ref, e.stop_place_id as station_ref
  from station_equipment e
  left join station_equipment_state st on st.equipment_id = e.equipment_id
  where e.closed_at is null
  union all
  select x.kind, x.state, x.source_feed, (select d.ifopt from db_stations d where d.station_no = x.station_no)
  from db_facilities x
), places as materialized (
  select u.station_ref, contract_stop_place_of(u.station_ref) as stop_place_id
  from (select distinct f.station_ref from f where f.station_ref is not null) u
)
select 'facilities', f.facility_kind || '/' || coalesce(f.facility_status, 'unknown'), null::text, f.source_ref,
       count(*)::int, count(distinct p.stop_place_id)::int, null::float8, null::float8
from f
left join places p on p.station_ref = f.station_ref
where f.source_ref = any($1) and $2::timestamptz is not null
group by 2, 4
order by 1, 2, 4`, "text[]", "timestamp with time zone"),
	"weather": namedOpaque("api_national_weather", `
select 'warnings', w.warning_level::text, null::text, w.source_ref, count(*)::int, count(distinct w.warn_cell_id)::int, null::float8, null::float8
from (`+relWeatherWarning+`) w
where w.withdrawn_at is null and w.message_status <> 'cancelled' and w.source_ref = any($1) and `+permitted("w")+`
  and w.valid_from <= $2 and (w.valid_to is null or w.valid_to > $2)
group by 2, 4
order by 1, 2, 4`, "text[]", "timestamp with time zone"),
}

// trafficEventsOfFamilies is the platform's traffic event relation with
// the source rules matched once per family of $1, not once per event: the
// matches are read once into an object by family (only the families a rule
// matches: a family no rule matches reads as one with no rule), and each
// event looks its source up there. A statement that reads it keeps only the events of $1
// (an event of another source finds no rule here and is left out by that
// filter). On prod the rule match per event was a fifth of the national
// road summary; a join to the matches was planned as a loop over them for
// each event.
var trafficEventsOfFamilies = strings.Replace(relTrafficEvent, rulesPerEvent, `LEFT JOIN LATERAL ( SELECT (r.j ->> 'excluded')::boolean AS excluded,
            r.j ->> 'audience' AS audience,
            r.j ->> 'display_class' AS display_class,
            r.j ->> 'origin_restriction' AS origin_restriction
           FROM ( SELECT ( SELECT jsonb_object_agg(u.f, to_jsonb(k_1))
                   FROM ( SELECT DISTINCT f FROM unnest($1::text[]) f) u
                   JOIN LATERAL ( SELECT k_2.excluded, k_2.audience, k_2.display_class, k_2.origin_restriction
                         FROM contract_source_rules k_2
                        WHERE u.f ~ k_2.pattern
                        ORDER BY k_2.priority
                       LIMIT 1) k_1 ON true) -> b.source_ref AS j) r) k ON true`, 1)

// rulesPerEvent is the traffic event relation's match of the source rules,
// one per event.
const rulesPerEvent = `LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE b.source_ref ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true`

// nationalInForce are the road situations of the families in force at $2.
const nationalInForce = `e.source_ref = any($1) and ` + eventPermitted + `
  and e.withdrawn_at is null and e.valid_from <= $2 and (e.valid_to is null or e.valid_to > $2)`

// National reads one domain's part of the national picture from the given
// families at now (features.National); fuelWindow is how young a price must
// be to count. A domain with no part reads nothing.
func (s *Store) National(ctx context.Context, domain string, families []string, now time.Time, fuelWindow time.Duration) ([]national.Row, error) {
	sql, ok := nationalSQL[domain]
	if !ok {
		return nil, nil
	}
	args := []any{families, now}
	if domain == "energy" {
		args = append(args, int(fuelWindow.Seconds()))
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]national.Row, error) {
		rows, _ := run(ctx, tx, sql, args...)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (national.Row, error) {
			var v national.Row
			err := r.Scan(&v.Section, &v.Key, &v.Name, &v.Family, &v.N, &v.M, &v.Low, &v.Mid)
			return v, err
		})
	})
}
