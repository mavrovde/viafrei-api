package store

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The road-conditions repositories (anti-corruption layer): every stored
// code is mapped through the roads domain, every reference is shown in its
// canonical form, and no storage name leaves this file.

// codes returns the values of a list of domain codes.
func codes[T ~string](list []T) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = string(v)
	}
	return out
}

// roadByRef reads the motorway of the Autobahn register whose reference,
// upper case without spaces, is $1, with the length and the Länder of the
// shape the platform draws for it (contract_road_shape), drawn for that
// road alone.
var roadByRef = named("api_road", `
select r.road, 'motorway', l.length_km::numeric(6,1)::float8, l.states
from autobahn_roads r
left join lateral contract_road_shape(r.road) l(line, length_km, states) on true
where upper(replace(r.road, ' ', '')) = (select $1::text)
order by r.road
limit 1`, "text")

// Road reads one road by its canonical reference.
func (s *Store) Road(ctx context.Context, ref string) (roads.Road, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roads.Road, error) {
		rows, _ := run(ctx, tx, roadByRef, ref)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.Road, error) {
			var (
				v          roads.Road
				ref, class string
			)
			err := r.Scan(&ref, &class, &v.LengthKm, &v.States)
			v.Ref, v.Class = roads.Canonical(ref), roads.MapClass(class)
			return v, err
		})
	})
	if err == nil && len(out) == 0 {
		err = roads.ErrUnknownRoad
	}
	if err != nil {
		return roads.Road{}, err
	}
	return out[0], nil
}

// situationColumns are the columns every situation read selects, in the
// order scanSituation reads them, followed by its two question-dependent
// columns: the position along the road and the distance.
// roadDelayMin is a situation's delay in whole minutes as the platform
// states it, Math.round(delay_s / 60): half a minute rounds up. The
// contract's delay_min truncates (100 s is 1 there, 2 on the platform).
const roadDelayMin = `floor(e.delay_s / 60.0 + 0.5)::int`

const situationColumns = `e.event_id, e.source_ref, e.event_kind, e.severity, e.road_ref, e.road_class,
       e.direction, e.direction_label, st_y(e.location), st_x(e.location), e.valid_from, e.valid_to,
       e.lanes_closed, e.lanes_total, ` + roadDelayMin + `, e.affected_length_m, e.headline, e.description,
       e.text_lang, e.changed_at, e.subtitle, coalesce(e.is_blocked, false)`

// situationFilter is the condition every list read applies: an active,
// published situation of the given families whose validity overlaps the
// asked window, then the asked kinds and the severity floor. $1 families,
// $3 kinds kept, $4 whether unmapped kinds are kept, $5 every mapped kind,
// $6 the ordered severities, $7 the floor; $w and $w+1 the window's start
// and end (both now for the situations in force now).
func situationFilter(w int) string {
	return fmt.Sprintf(`e.withdrawn_at is null
  and e.source_ref = any($1)
  and `+eventPermitted+`
  and e.valid_from <= greatest($2::timestamptz, $%[2]d) and (e.valid_to is null or e.valid_to > $%[1]d)
  and (e.event_kind = any($3) or ($4 and not e.event_kind = any($5)))
  and coalesce(array_position($6::text[], e.severity), 0) >= $7`, w, w+1)
}

// eventRow is every column of trafficEvents a list read carries
// forward, never state_code: a Land is looked up only where it is asked for.
var eventRow = "e." + strings.Join(strings.Split("event_id, event_kind, severity, road_ref, road_class, direction, direction_label, location, valid_from, valid_to, lanes_closed, lanes_total, delay_min, affected_length_m, headline, description, text_lang, source_ref, origin_key, published_at, recorded_at, changed_at, withdrawn_at, record_class, subtitle, place_text, is_blocked, traffic_type, delay_s, source_refs, display_class, audience, confidentiality, origin_restriction", ", "), ", e.")

// eventPermitted is rowRule over the traffic events aliased e.
const eventPermitted = `e.display_class <> 'withhold' and (e.audience = 'public' or ` + ownKeyOn + `)`

// canonicalRef is a stored road reference compared in its canonical form,
// as Road compares it.
const canonicalRef = `upper(replace(%s, ' ', ''))`

// opaqueNamed is the named statement name of sql with each parameter read
// opaque ((select $n::type)::type, of types in order), so a custom plan sees
// no more than the generic one and auto keeps the generic plan.
func opaqueNamed(name, sql string, types ...string) stmt {
	return named(name, parameter.ReplaceAllStringFunc(sql, func(p string) string {
		n, _ := strconv.Atoi(p[1:])
		return "((select " + p + "::" + types[n-1] + ")::" + types[n-1] + ")"
	}), types...)
}

// autobahnLand is the Land of an Autobahn row: the one stored, or for a row
// written before it was stored, the platform's rule on the row's point.
const autobahnLand = `coalesce(e.land_code, contract_junction_land(st_pointonsurface(e.geom::geometry)::geography))`

// autobahnInForce is whether an Autobahn message is in force now, the
// motorway read's rule for an announced entry turned round: the publisher
// marks it future, or its description lists validity windows of which none
// holds now. A closure announced for next week, or one valid only at night,
// gives the lamp no reason while it is not in force (it is still listed).
const autobahnInForce = `not (coalesce(a.future, '') = 'true' or coalesce(not autobahn_validity_overlaps(a.lines, now(), now()), false))`

// autobahnStarted is the start an Autobahn message states, when it states
// one as a time, else when it was seen.
const autobahnStarted = `coalesce(case when a.starts ~ '^\d{4}-\d\d-\d\dT\d\d:\d\d' then a.starts::timestamptz end, a.observed_at)`

// trafficEvents are the situations as the platform publishes them, read
// from its tables as the read contract's situation reads them: the
// Autobahn messages of each road's newest sweep, when that sweep is under 30
// minutes old (today's motorway read: a road's latest tick within the 30
// minutes before the clock, and the messages at that tick), and the open
// traffic messages. A kind whose newest sweep is older than its road's
// newest is no longer published: a jam the sweep has stopped reporting is
// gone, though no sweep of its kind has replaced it; each with its family's rule (excluded
// families dropped, a message under a confidentiality withheld), the
// columns of eventRow. An Autobahn message's start, blocking and
// description are fields of its provider message, which a sweep stores only
// when it changed: a row without it takes those of the newest row of the
// same message and the same message hash since the start of its UTC day.
// With in (a Land code, a parameter), only the situations placed in that
// Land are read (with de, all), each with its Land (state_code, the
// platform's rule: the Land of the nearest municipality within 20 km) and
// the family that placed it there. The Land is the one stored when the
// situation was written (land_code); an Autobahn row written before the
// column was stored has none, and its Land is looked up by the same rule.
// Without in, the Land is not read.
func trafficEvents(in string) string {
	state, landA, landR := `null::text state_code, null::text placement_source_ref`, `null::text`, `null::text`
	inLand, inLandRE := ``, ``
	if in != "" {
		state = `b.land_code state_code, case when b.land_code is not null then 'bkg_gvisys' end placement_source_ref`
		landA, landR = autobahnLand, `e.land_code`
		inLand = `
  where ` + in + ` = 'de' or ` + autobahnLand + ` = ` + in
		inLandRE = `
    and (` + in + ` = 'de' or e.land_code = ` + in + `)`
	}
	return `(with newest as (
  select x.road, x.kind, x.at from (
    select s.road, s.kind, s.observed_at as at, max(s.observed_at) over (partition by s.road) road_at from autobahn_sweeps s
    where s.kind = any(array['warning', 'roadworks', 'closure', 'closure_entry_exit'])) x
  where x.at = x.road_at and x.at > now() - interval '30 minutes'),
autobahn as (
  select e.observed_at, e.road, e.kind, e.ext_id, e.title, e.subtitle, e.abnormal_traffic_type, e.delay_s, e.geom, e.source, ` + landA + ` land_code,
         coalesce(e.raw->>'isBlocked', c.blocked) blocked, coalesce(e.raw->>'startTimestamp', c.starts) starts,
         coalesce(e.raw->'description', c.lines) lines, coalesce(e.raw->>'future', c.future) future
  from autobahn_events e
  join newest n on n.road = e.road and n.kind = e.kind and n.at = e.observed_at
  left join lateral (
    select r.raw->>'isBlocked' blocked, r.raw->>'startTimestamp' starts, r.raw->'description' lines, r.raw->>'future' future
    from autobahn_events r
    where e.raw is null and r.ext_id = e.ext_id and r.raw is not null and r.raw_hash = e.raw_hash
      and r.observed_at <= e.observed_at and r.observed_at >= date_trunc('day', e.observed_at, 'UTC')
    order by r.observed_at desc
    limit 1) c on true` + inLand + `),
b as (
  select 'autobahn:' || a.ext_id event_id,
         case when a.kind like 'closure%' then 'closure' when a.kind = 'roadworks' then 'roadworks'
              when a.kind = 'warning' and a.abnormal_traffic_type = any(array['QUEUING_TRAFFIC', 'STATIONARY_TRAFFIC', 'SLOW_TRAFFIC', 'HEAVY_TRAFFIC']) then 'jam'
              when a.kind = 'warning' then 'hazard' else 'other' end event_kind,
         case when a.kind = 'closure' or a.blocked = 'true' then 'road_closed' when a.kind = 'closure_entry_exit' then 'major'
              when a.kind = 'roadworks' then 'lane_closed'
              when a.kind = 'warning' and a.abnormal_traffic_type = any(array['QUEUING_TRAFFIC', 'STATIONARY_TRAFFIC', 'SLOW_TRAFFIC', 'HEAVY_TRAFFIC'])
                then case when a.delay_s >= 600 then 'major' else 'minor' end
              when a.kind = 'warning' then 'info' else 'other' end severity,
         a.road road_ref, 'motorway' road_class, 'unknown' direction, null::text direction_label,
         st_pointonsurface(a.geom::geometry)::geometry(Point,4326) location,
         ` + autobahnStarted + ` valid_from, null::timestamptz valid_to,
         null::int lanes_closed, null::int lanes_total, case when a.delay_s >= 0 then a.delay_s / 60 end delay_min,
         null::int affected_length_m, coalesce(a.title, a.kind) headline,
         (select string_agg(l.line, e'\n' order by l.n)
          from jsonb_array_elements_text(case when jsonb_typeof(a.lines) = 'array' then a.lines end) with ordinality l(line, n)) description,
         'de' text_lang, a.source source_ref, a.ext_id origin_key, null::timestamptz published_at,
         a.observed_at recorded_at, a.observed_at changed_at, null::timestamptz withdrawn_at, 'operator_event' record_class,
         a.subtitle, null::text place_text,
         case a.blocked when 'true' then true when 'false' then false end is_blocked,
         a.abnormal_traffic_type traffic_type, case when a.delay_s >= 0 then a.delay_s end delay_s, null::text confidentiality,
         a.land_code, ` + autobahnInForce + ` in_force
  from autobahn a
  union all
  select 'datex:' || e.source_id || ':' || e.publication_id,
         case when e.event_type ~* '(maintenance|roadworks|construction)' then 'roadworks'
              when e.event_type ~* '(accident|unfall)' then 'accident'
              when e.event_type ~* 'abnormaltraffic' then 'jam'
              when e.event_type ~* '(weather|environment)' then 'weather'
              when e.event_type ~* '(closure|sperrung)' then 'closure'
              when e.event_type ~* '(speedmanagement|restriction|reroutingmanagement|carriagewayorlanemanagement|networkmanagement)' then 'restriction'
              when e.event_type ~* '(obstruction|hazard|animal|vehicle|equipment)' then 'hazard'
              else 'other' end,
         case when e.event_type ~* '(closure|sperrung)' then 'road_closed'
              when e.event_type ~* '(accident|unfall)' then 'major'
              when e.event_type ~* '(maintenance|roadworks|construction)' then 'lane_closed'
              when e.event_type ~* '(abnormaltraffic|speedmanagement|restriction|reroutingmanagement|carriagewayorlanemanagement|networkmanagement)' then 'minor'
              when e.event_type ~* '(weather|environment|obstruction|hazard|animal|vehicle|equipment)' then 'info'
              else 'other' end,
         e.road_number,
         case when e.road_number ~ '^A ?[0-9]' then 'motorway' when e.road_number ~ '^B ?[0-9]' then 'federal'
              when e.road_number ~ '^(L|S|St) ?[0-9]' then 'state' else 'other' end,
         'unknown', null::text, st_pointonsurface(e.geom)::geometry(Point,4326),
         coalesce(e.overall_start, e.first_seen_at), e.overall_end,
         null::int, null::int, null::int, null::int, coalesce(e.event_type, 'situation'), null::text, 'de',
         e.source_feed, e.publication_id, e.feed_ts, e.first_seen_at, e.updated_at, e.closed_at, 'traffic_message',
         null::text, null::text, null::boolean, null::text, null::int, e.confidentiality, ` + landR + `, true
  from road_events e
  where e.deleted_at is null` + inLandRE + `)
select b.event_id, b.event_kind, b.severity, b.road_ref, b.road_class, b.direction, b.direction_label, b.location,
       b.valid_from, b.valid_to, b.lanes_closed, b.lanes_total, b.delay_min, b.affected_length_m, b.headline,
       b.description, b.text_lang, b.source_ref, b.origin_key, b.published_at, b.recorded_at, b.changed_at,
       b.withdrawn_at, b.record_class, b.subtitle, b.place_text, b.is_blocked, b.traffic_type, b.delay_s,
       array[b.source_ref] source_refs,
       case when coalesce(b.confidentiality, 'noRestriction') <> 'noRestriction' then 'withhold'
            else coalesce(k.display_class, 'open') end display_class,
       coalesce(k.audience, 'public') audience, b.confidentiality, k.origin_restriction, b.in_force, ` + state + `
from b
` + eventRulePerRow + `
where not coalesce(k.excluded, false))`
}

// eventRulePerRow is trafficEvents' match of the source rules for each
// situation, by its source.
const eventRulePerRow = `left join lateral (select k.excluded, k.audience, k.display_class, k.origin_restriction from contract_source_rules k
                   where b.source_ref ~ k.pattern order by k.priority limit 1) k on true`

// trafficEventsOf is trafficEvents for a statement that keeps only the
// situations of the families $1 (text[]): the source rules are matched once
// per family of $1 (rulesOfFamilies), not once per situation, and each
// situation looks its source up there. A situation of another source finds
// no rule and is left out by the statement's own filter on $1. On prod the
// rule match per situation was most of the motorway list's and the lamps'
// time (thousands of situations, a regular expression test each).
func trafficEventsOf(in string) string {
	return strings.Replace(trafficEvents(in), eventRulePerRow, rulesOfFamilies("b.source_ref"), 1)
}

// filterArgs are the arguments $1 to $7 of situationFilter.
func filterArgs(f roads.Filter, families []string, now time.Time) []any {
	kinds, other := f.KnownKinds()
	return []any{families, now, codes(kinds), other, codes(roads.Kinds[:len(roads.Kinds)-1]), codes(roads.Ordered()), f.MinSeverity.Floor()}
}

// nextChange is, over the rows (as x) a scope read selects that have not
// ended at $2 (now), the earliest instant after now at which one starts or
// ends: its start when it starts after now, else its end; null when none
// does.
const nextChange = `min(case when x.valid_from > $2 then x.valid_from else x.valid_to end)`

// messagesSelect aggregates the lamp's view of every situation in the scope
// as a JSON array of {"r": record, "k": kind, "h": headline, "s": subtitle,
// "t": description, "p": place words, "b": blocked, "y": traffic type,
// "ds": delay in seconds, "d": delay in minutes}, null when the scope holds
// none; and the scope's next change (nextChange). It reads the scope's
// situations that have not ended, so one that starts later is seen too; only
// those active at now are aggregated.
func messagesSelect(in string) string {
	return `select ` + messagesAgg + `,
       ` + nextChange + `
` + messagesFrom(in)
}

// messagesAgg is the lamp's view of the situations of messagesFrom active
// at $2, as messagesSelect aggregates it.
const messagesAgg = `jsonb_agg(distinct jsonb_build_object('r', e.record_class, 'k', e.event_kind, 'h', e.headline,
                                         's', e.subtitle, 't', e.description, 'p', e.place_text,
                                         'b', coalesce(e.is_blocked, false), 'y', e.traffic_type,
                                         'ds', e.delay_s, 'd', ` + roadDelayMin + `)) filter (where e.valid_from <= $2 and e.in_force)`

// messagesFrom reads the situations of the families $1 that have not ended
// at $2, with their Land when they may lie in the Land in (trafficEvents).
func messagesFrom(in string) string {
	return `from ` + trafficEventsOf(in) + ` e
cross join lateral (select e.valid_from, e.valid_to) x
where e.withdrawn_at is null
  and e.source_ref = any($1::text[])
  and ` + eventPermitted + `
  and (e.valid_to is null or e.valid_to > $2::timestamptz)`
}

// storedMessage is one element of messagesSelect, in stored codes.
type storedMessage struct {
	Record      string  `json:"r"`
	Kind        string  `json:"k"`
	Headline    string  `json:"h"`
	Subtitle    *string `json:"s"`
	Description *string `json:"t"`
	PlaceText   *string `json:"p"`
	Blocked     bool    `json:"b"`
	TrafficType *string `json:"y"`
	DelayS      *int    `json:"ds"`
	DelayMin    *int    `json:"d"`
}

// sourcesSelect reads the freshness of the given families and the newest
// confirmation of those that have a freshness row. $1 families. A family is
// a registered source of the platform that its rule does not exclude and
// whose licence is not pending; it is stale after three of its cadences,
// its own or else its status rows' longest; its freshness is its status
// row's.
var sourcesSelect = opaqueNamed("api_road_sources", `select s.source_ref,
       3 * coalesce(s.cadence_s, (select max(x.cadence_s) from control.data_status x where x.key = s.source_ref)),
       d.last_changed_at, d.key is not null, d.held_since, d.cadence_s::float8, coalesce(d.paused, false)
from contract_sources s
left join lateral (select k.excluded from contract_source_rules k where s.source_ref ~ k.pattern order by k.priority limit 1) k on true
left join control.data_status d on d.key = s.source_ref
where s.source_ref = any($1) and not coalesce(k.excluded, false) and coalesce(s.licence_code, '') !~* '^pending'
order by s.source_ref`, "text[]")

// coverageSelect reads which of the given families cover the traffic
// messages of the given states. $1 states, $2 families. Coverage is the
// platform's own measure of the families, open to every key.
var coverageSelect = named("api_road_coverage", `
select m.state_code, m.source_ref, m.coverage_role, m.current_only
from message_coverage_sources m
where m.state_code = any((select $1::text[])::text[]) and m.source_ref = any((select $2::text[])::text[])
order by m.state_code, m.source_ref`, "text[]", "text[]")

// statesOf is the statement name of the sorted Länder, as one array, of the
// junctions the select junctions yields (one Land code a row, state_code).
func statesOf(name, junctions string, types ...string) stmt {
	return named(name, `select coalesce(array_agg(distinct state_code order by state_code) filter (where state_code is not null), '{}')
from (`+junctions+`) j`, types...)
}

// The Länder of a scope. On a road those of its junctions (a motorway
// point's reference is A and digits, as written, its canonical form); near a
// point those of the junctions within $3 metres of ($2, $1), else that of the
// nearest junction; in a state the state itself, or for de every Land of
// the region register. A junction's Land is the platform's
// (contract_junction_land: the Land of the nearest municipality).
var (
	statesOnRoad = statesOf("api_states_road", `select contract_junction_land(t.geom) state_code from tmc_locations t
where t.road_number = (select $1::text) and `+motorwayJunctions, "text")
	statesNear = statesOf("api_states_near", `with here as (select st_setsrid(st_makepoint((select $2::float8), (select $1::float8)), 4326) g),
near as (select contract_junction_land(t.geom) state_code from tmc_locations t, here
         where st_dwithin(t.geom::geometry(Point,4326)::geography, here.g::geography, (select $3::float8)) and `+motorwayJunctions+`)
select state_code from near
union all
select (select contract_junction_land(t.geom) from tmc_locations t, here where `+motorwayJunctions+`
        order by t.geom::geometry(Point,4326) <-> here.g, t.cid || ':' || t.tabcd || ':' || t.lcd limit 1)
where not exists (select 1 from near)`, "float8", "float8", "float8")
	statesInState = named("api_states_state", `select case when (select $1::text) = 'de'
  then (select coalesce(array_agg(a.ags order by a.ags), '{}') from ags_regions a where a.level = 'land')
  else array[(select $1::text)] end`, "text")
)

// sourcesOf collects the rows of sourcesSelect: the freshness of every
// family, and into confirmations the newest confirmation of each that has a
// freshness row.
func sourcesOf(rows pgx.Rows, confirmations map[string]ampel.Confirmation) ([]provenance.Family, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (provenance.Family, error) {
		var (
			f          provenance.Family
			staleAfter *int64
			held       bool
			c          ampel.Confirmation
		)
		err := r.Scan(&f.Source.Family, &staleAfter, &f.DataAsOf, &held, &c.At, &c.IntervalS, &c.Paused)
		f.StaleAfter, f.NeverStale = threshold(staleAfter)
		if held {
			confirmations[f.Source.Family] = c
		}
		return f, err
	})
}

// coverageRow is one row of coverageSelect.
type coverageRow struct {
	State, Source, Role string
	CurrentOnly         bool
}

// coverageOf gathers the coverage rows of the states in scope by state.
func coverageOf(rows pgx.Rows) (map[string]ampel.Coverage, error) {
	read, err := pgx.CollectRows(rows, pgx.RowToStructByPos[coverageRow])
	out := map[string]ampel.Coverage{}
	for _, r := range read {
		c := out[r.State]
		if r.Role == "own" {
			c.Own = append(c.Own, r.Source)
			if r.CurrentOnly {
				c.CurrentOnly = append(c.CurrentOnly, r.Source)
			}
		} else {
			c.Carried = append(c.Carried, r.Source)
		}
		out[r.State] = c
	}
	return out, err
}

// lampWarnings is the statement name of the weather warnings that have not
// ended under cond, the scope's area condition over $3 onwards (of types):
// those in force are the lamp's, and the earliest start or end after now of
// any is when the scope changes. A warning is published (begun or not, not
// deleted), not withdrawn and not cancelled, of the families $1, and has not
// ended at $2.
func lampWarnings(name, cond string, types ...string) stmt {
	return named(name, `
select w.source_feed, w.level::int, w.valid_from <= (select $2::timestamptz),
  case when w.valid_from > (select $2::timestamptz) then w.valid_from else w.valid_to end
from weather_warnings w
where w.deleted_at is null and w.valid_from is not null and w.closed_at is null
  and coalesce(w.closed_reason, '') <> 'withdrawn'
  and w.source_feed = any((select $1::text[])::text[])
  and (w.valid_to is null or w.valid_to > (select $2::timestamptz))
  and `+cond+`
order by w.level::int desc, 'dwd:' || w.id`, append([]string{"text[]", "timestamptz"}, types...)...)
}

// The lamp's warnings by scope. On a road they are those on the location-code
// points of the road's junctions, as the platform's motorway page and its
// motorway status place a DWD warning on the points of the road, not on the
// districts the road crosses; at a point those whose own area reaches within
// the radius, as the platform's warnings at a point test the warning's area;
// in a state those whose cell key names the state in its first two digits.
// A road's points are read by the road's own reference (a motorway point's
// reference is A and digits, as written, so it is its canonical form), and
// each point finds the open warnings on it through their area's GiST.
var (
	warningsOnRoad = lampWarnings("api_lamp_warnings_road", `w.id in (select o.id from tmc_locations t
               join weather_warnings o on st_intersects(o.area::geometry, t.geom::geometry(Point,4326))
                 and o.closed_at is null and o.deleted_at is null
               where t.road_number = (select $3::text) and `+motorwayJunctions+`)`, "text")
	warningsNear = lampWarnings("api_lamp_warnings_near", `w.area is not null
  and st_dwithin(w.area, st_setsrid(st_makepoint((select $4::float8), (select $3::float8)), 4326)::geography, (select $5::float8))`,
		"float8", "float8", "float8")
	warningsInState = lampWarnings("api_lamp_warnings_state", `((select $3::text) = 'de'
  or (left(w.warncell_id, 1) in ('7', '8') and substr(w.warncell_id, 2, 2) = (select $3::text)))`, "text")
)

// scope is one read of the lamp's scope, run in the page's snapshot.
type scope struct {
	messages stmt
	args     []any
	// warnings reads the weather warnings of the scope (lampWarnings), its
	// area condition over $3 onwards of warningArgs.
	warnings    stmt
	warningArgs []any
	// states selects the codes of the states in scope, over stateArgs.
	states    stmt
	stateArgs []any
	// placed selects the families that placed the scope's situations in a
	// Land, over args; empty when the scope is no Land.
	placed stmt
	// covering selects, over coveringArgs, the families read that hold a
	// situation on a road of an unregistered road's class; empty for any
	// other scope.
	covering     stmt
	coveringArgs []any
}

// coveringSelect reads which of the given families hold a situation that
// has not ended on a road of a class, in name order. $1 families, $2 the
// stored class, $3 now.
var coveringSelect = opaqueNamed("api_road_covering", `select coalesce(array_agg(f order by f), '{}')
from unnest($1::text[]) f
where exists (select 1 from `+trafficEvents("")+` e
              where e.source_ref = f and e.withdrawn_at is null and e.road_class = $2
                and (e.valid_to is null or e.valid_to > $3)
                and `+eventPermitted+`)`, "text[]", "text", "timestamptz")

// counted is a listed situation and, by family, how many situations the
// list's filters keep before the limit.
type counted struct {
	situation roads.Situation
	counts    map[string]int
}

// scanCounted reads a listed situation and the counts by family.
func scanCounted(r pgx.CollectableRow) (counted, error) {
	var c counted
	s, err := scanSituationWith(r, &c.counts)
	c.situation = s
	return c, err
}

// scanSituation reads one situation.
func scanSituation(r pgx.CollectableRow) (roads.Situation, error) {
	return scanSituationWith(r)
}

// scanSituationWith reads one situation, then the extra columns into extra.
func scanSituationWith(r pgx.CollectableRow, extra ...any) (roads.Situation, error) {
	var (
		v                                      roads.Situation
		kind, severity, class, direction       string
		road                                   *string
		lat, lon                               *float64
		from, updated                          time.Time
		until                                  *time.Time
		headline, lang                         string
		lanesClosed, lanesTotal, delay, length *int
	)
	err := r.Scan(append([]any{&v.ID, &v.Family, &kind, &severity, &road, &class,
		&direction, &v.Towards, &lat, &lon, &from, &until,
		&lanesClosed, &lanesTotal, &delay, &length, &headline, &v.Description,
		&lang, &updated, &v.Subtitle, &v.Blocked, &v.AtKm, &v.DistanceM}, extra...)...)
	s := &v
	s.Kind, s.Severity, s.Class, s.Direction = roads.MapKind(kind), roads.MapSeverity(severity), roads.MapClass(class), roads.MapDirection(direction)
	if road != nil {
		c := roads.Canonical(*road)
		s.Road = &c
	}
	if lat != nil && lon != nil {
		s.Location = &geo.Point{Lat: *lat, Lon: *lon}
	}
	s.From, s.UpdatedAt = from.UTC(), updated.UTC()
	if until != nil {
		u := until.UTC()
		s.Until = &u
	}
	s.LanesClosed, s.LanesTotal, s.DelayMin, s.LengthM = lanesClosed, lanesTotal, delay, length
	s.Headline, s.TextLanguage = headline, lang
	return v, err
}

// scanOne scans the one row of rows (a statement run) into dest: no row is
// pgx.ErrNoRows, as a QueryRow scan answers.
func scanOne(rows pgx.Rows, _ error) func(dest ...any) error {
	return func(dest ...any) error {
		_, err := pgx.CollectExactlyOneRow(rows, func(r pgx.CollectableRow) (struct{}, error) { return struct{}{}, r.Scan(dest...) })
		return err
	}
}

// read runs the page query and the reads of the lamp's scope (its
// situations, the weather warnings in force there when a weather family is
// read, the freshness and newest confirmation of every family read, the
// states in scope and which roads families read cover their traffic
// messages) in one snapshot.
func (s *Store) read(ctx context.Context, list stmt, listArgs []any, limit int, fams roads.Families, now time.Time, sc scope) (roads.Page, error) {
	// The page and its messages are built inside the read and handed back
	// by ReadValue, so nothing the caller holds is written after a read that
	// ran past its deadline.
	type snapshot struct {
		page   roads.Page
		stored []storedMessage
	}
	snap, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (snapshot, error) {
		var (
			out    = roads.Page{Confirmations: map[string]ampel.Confirmation{}}
			stored []storedMessage
		)
		// The reads in order; the first that fails ends the snapshot.
		for _, step := range []func() error{
			func() (err error) {
				res, _ := run(ctx, tx, list, listArgs...)
				rows, err := pgx.CollectRows(res, scanCounted)
				// Every row carries the counts of the whole filtered set, the
				// families past the limit too; none listed is none counted.
				out.Items, out.Counts = make([]roads.Situation, len(rows)), map[string]int{}
				for i, r := range rows {
					out.Items[i], out.Counts = r.situation, r.counts
				}
				return err
			},
			func() error { return scanOne(run(ctx, tx, sc.messages, sc.args...))(&stored, &out.NextChange) },
			func() error {
				if len(fams.Weather) == 0 {
					return nil
				}
				// One read of the warnings that have not ended: those in
				// force are the lamp's, and the earliest start or end after
				// now of any is when the scope changes.
				res, _ := run(ctx, tx, sc.warnings, append([]any{fams.Weather, now}, sc.warningArgs...)...)
				// A read that found none is an empty list, never nil: nil
				// means the warnings were not read.
				out.Warnings = []roads.Warning{}
				var w roads.Warning
				var inForce bool
				var next *time.Time
				_, err := pgx.ForEachRow(res, []any{&w.Family, &w.Level, &inForce, &next}, func() error {
					if inForce {
						out.Warnings = append(out.Warnings, w)
					}
					if next != nil && (out.NextChange == nil || next.Before(*out.NextChange)) {
						out.NextChange = next
					}
					return nil
				})
				return err
			},
			func() (err error) {
				res, _ := run(ctx, tx, sourcesSelect, append(slices.Clone(fams.Roads), fams.Weather...))
				out.Sources, err = sourcesOf(res, out.Confirmations)
				return err
			},
			func() (err error) {
				res, _ := run(ctx, tx, sc.states, sc.stateArgs...)
				out.States, err = pgx.CollectExactlyOneRow(res, pgx.RowTo[[]string])
				return err
			},
			func() error {
				if sc.placed.Name == "" {
					return nil
				}
				return scanOne(run(ctx, tx, sc.placed, sc.args...))(&out.Placed)
			},
			func() error {
				if sc.covering.Name == "" {
					return nil
				}
				return scanOne(run(ctx, tx, sc.covering, sc.coveringArgs...))(&out.Covering)
			},
			func() (err error) {
				res, _ := run(ctx, tx, coverageSelect, out.States, fams.Roads)
				out.Coverage, err = coverageOf(res)
				return err
			},
		} {
			if err := step(); err != nil {
				return snapshot{}, err
			}
		}
		return snapshot{out, stored}, nil
	})
	if err != nil {
		return roads.Page{}, err
	}
	out := snap.page
	out.Messages = messagesOf(snap.stored)
	if len(out.Items) > limit {
		out.Items, out.HasMore = out.Items[:limit], true
	}
	return out, nil
}

// messagesOf maps the lamp's view of the situations from stored codes; nil
// when there is none.
func messagesOf(stored []storedMessage) []roads.Message {
	var out []roads.Message
	for _, m := range stored {
		out = append(out, roads.Message{Record: roads.MapRecord(m.Record), Kind: roads.MapKind(m.Kind),
			Headline: m.Headline, Subtitle: m.Subtitle, Description: m.Description, PlaceText: m.PlaceText,
			Blocked: m.Blocked, TrafficType: m.TrafficType, DelayS: m.DelayS, DelayMin: m.DelayMin})
	}
	return out
}

// The situation lists by scope (filterArgs $1 to $7, then the scope's):
// on a road $8 the road, $9 the direction, $10 the compass directions, $11
// the limit, $12 and $13 the window, $14 the offset, $15 whether the road
// may be unregistered; near a point $8 and $9 the position, $10 the radius,
// $11 the classes kept, $12 whether unmapped classes are kept, $13 every
// mapped class, $14 the limit, $15 and $16 the window, $17 the offset; in a
// Land $8 the Land, $9 to $11 the classes as near a point, $12 the limit,
// $13 and $14 the window, $15 the offset. A road's line is its shape
// (contract_road_shape), its parts in their stored order.
var (
	situationsOnRoad = opaqueNamed("api_road_situations", `with road as (
  select k.road road_ref from autobahn_roads k
  where upper(replace(k.road, ' ', '')) = $8 order by k.road limit 1),
parts as (
  select r.road_ref, d.path[1] n, d.geom g, st_length(d.geom::geography) len, l.length_km::numeric(6,1)::float8 km
  from road r
  cross join lateral contract_road_shape(r.road_ref) l(line, length_km, states)
  cross join lateral st_dump(l.line::geometry(MultiLineString,4326)) d),
line as (
  select road_ref, max(km) km, sum(len) total
  from parts group by road_ref),
answering as (
  select `+eventRow+`, line.km road_km, line.total road_total
  from `+trafficEventsOf("")+` e left join line on true
  where `+situationFilter(12)+`
    and ($15 or exists (select 1 from road))
    and `+fmt.Sprintf(canonicalRef, "e.road_ref")+` = $8
    and ($9::text = 'both' or e.direction = $9::text or not e.direction = any($10))),
placed as (
  select `+strings.ReplaceAll(eventRow, "e.", "a.")+`, a.road_km, a.road_total, at.share
  from answering a
  left join lateral (
    select ((select coalesce(sum(q.len), 0) from parts q where q.n < p.n)
            + st_linelocatepoint(p.g, a.location) * p.len) / nullif(a.road_total, 0) share
    from parts p
    where a.location is not null
    order by p.g <-> a.location, p.n
    limit 1) at on true)
select `+situationColumns+`,
       case when e.share is null or e.road_km is null then null
            else round((e.share * e.road_km)::numeric, 1)::float8 end km,
       null::int,
       (select jsonb_object_agg(c.source_ref, c.n) from (select source_ref, count(*)::int n from answering group by source_ref) c)
from placed e
order by case when e.event_kind = 'jam' then 0 when e.event_kind = 'closure' then 1
              when e.event_kind = 'roadworks' then 3 else 2 end,
         case e.traffic_type when 'QUEUING_TRAFFIC' then 0 when 'STATIONARY_TRAFFIC' then 1
              when 'HEAVY_TRAFFIC' then 2 when 'SLOW_TRAFFIC' then 3 else 4 end,
         e.delay_s desc nulls last, e.headline, e.origin_key, e.event_id
limit $11 offset $14`,
		"text[]", "timestamptz", "text[]", "bool", "text[]", "text[]", "int8", "text", "text", "text[]", "int8", "timestamptz", "timestamptz", "int8", "bool")
	situationsNear = opaqueNamed("api_area_situations", `
with here as (select st_setsrid(st_makepoint($9, $8), 4326)::geography g),
answering as (
  select `+eventRow+`, round(st_distance(e.location::geography, here.g))::int dist
  from `+trafficEventsOf("")+` e, here
  where `+situationFilter(15)+`
    and e.location is not null
    and st_dwithin(e.location::geography, here.g, $10)
    and (e.road_class = any($11) or ($12 and not e.road_class = any($13))))
select `+situationColumns+`, null::float8, e.dist,
       (select jsonb_object_agg(c.source_ref, c.n) from (select source_ref, count(*)::int n from answering group by source_ref) c)
from answering e
order by coalesce(array_position($6::text[], e.severity), 0) desc, e.dist, e.event_id
limit $14 offset $17`,
		"text[]", "timestamptz", "text[]", "bool", "text[]", "text[]", "int8", "float8", "float8", "float8", "text[]", "bool", "text[]", "int8", "timestamptz", "timestamptz", "int8")
	situationsInStateList = opaqueNamed("api_state_situations", `
with answering as (
  select `+eventRow+`
  from `+trafficEventsOf("$8")+` e
  where `+situationFilter(13)+`
    and ($8 = 'de' or e.state_code = $8)
    and (e.road_class = any($9) or ($10 and not e.road_class = any($11))))
select `+situationColumns+`, null::float8, null::int,
       (select jsonb_object_agg(c.source_ref, c.n) from (select source_ref, count(*)::int n from answering group by source_ref) c)
from answering e
order by coalesce(array_position($6::text[], e.severity), 0) desc, e.road_ref, e.event_id
limit $12 offset $15`,
		"text[]", "timestamptz", "text[]", "bool", "text[]", "text[]", "int8", "text", "text[]", "bool", "text[]", "int8", "timestamptz", "timestamptz", "int8")
)

// The lamp's view of the situations by scope (messagesSelect: $1 families,
// $2 now): on a road $3 the road, $4 the direction, $5 the compass
// directions; near a point $3 and $4 the position, $5 the radius; in a Land
// $3 the Land (de: every situation).
var (
	messagesOnRoad = opaqueNamed("api_road_messages", messagesSelect("")+`
  and `+fmt.Sprintf(canonicalRef, "e.road_ref")+` = $3
  and ($4::text = 'both' or e.direction = $4::text or not e.direction = any($5))`, "text[]", "timestamptz", "text", "text", "text[]")
	messagesNear = opaqueNamed("api_area_messages", messagesSelect("")+`
  and e.location is not null
  and st_dwithin(e.location::geography, st_setsrid(st_makepoint($4, $3), 4326)::geography, $5)`, "text[]", "timestamptz", "float8", "float8", "float8")
	messagesInState = opaqueNamed("api_state_messages", messagesSelect("$3")+`
  and ($3 = 'de' or e.state_code = $3)`, "text[]", "timestamptz", "text")
	// placedInState are the families that placed the Land's situations in
	// it (over the messages' arguments), none for de.
	placedInState = opaqueNamed("api_state_placed", `select coalesce(array_agg(distinct e.placement_source_ref order by e.placement_source_ref) filter (where e.placement_source_ref is not null), '{}')
from `+trafficEventsOf("$3")+` e
where $3 <> 'de' and e.state_code = $3 and e.withdrawn_at is null and e.source_ref = any($1)
  and `+eventPermitted+` and (e.valid_to is null or e.valid_to > $2)`, "text[]", "timestamptz", "text")
)

// situationByID is the published situation $1 of the families $2 that is
// active or ended after $3.
var situationByID = opaqueNamed("api_situation", `
select `+situationColumns+`, null::float8, null::int
from `+trafficEvents("")+` e
where e.event_id = $1 and e.withdrawn_at is null and e.source_ref = any($2)
  and `+eventPermitted+`
  and (e.valid_to is null or e.valid_to > $3)`, "text", "text[]", "timestamptz")

// SituationsOnRoad reads the active situations on one road in the order of
// travel (FR-ROAD-003). The chainage runs across every part of the road's
// line in their stored order: a situation is placed on the nearest part, at
// the length of the parts before it plus its position on that part, as a
// share of the road's length and then in the road's published kilometres.
// For both directions the order is ascending chainage; for one direction it
// starts where the carriageway's traffic comes from, decided by the line's
// own ends (a road whose last part ends further north than its first part
// starts is driven north with rising chainage). Those that cannot be placed
// (no position, or a road with no line) come last, then severity descending, then id; and it returns, for the
// lamp, the view of every situation in the scope (the road and direction),
// the weather warnings in force in an area the road's line crosses, and the
// states of the road's junctions.
// A situation's road reference is compared in its canonical form, as the
// road's is: A1 and A 1 are one road.
func (s *Store) SituationsOnRoad(ctx context.Context, q roads.CorridorQuery, fams roads.Families, now time.Time) (roads.Page, error) {
	start, end := q.Window(now)
	args := append(filterArgs(q.Filter, fams.Roads, now), q.Road, string(q.Direction), codes(roads.Compass()), q.Limit+1, start, end, q.Offset, q.Unregistered)
	var sc scope
	if q.Unregistered {
		sc.covering, sc.coveringArgs = coveringSelect, []any{fams.Roads, string(q.Class), now}
	}
	return s.read(ctx, situationsOnRoad, args, q.Limit, fams, now, scope{
		covering: sc.covering, coveringArgs: sc.coveringArgs,
		messages: messagesOnRoad,
		args:     []any{fams.Roads, now, q.Road, string(q.Direction), codes(roads.Compass())},
		// A warning is on the road when its own area holds one of the
		// road's junction points, as the platform's motorway page and its
		// motorway status place a DWD warning on the location-code points
		// of the road, not on the districts the road crosses.
		warnings:    warningsOnRoad,
		warningArgs: []any{q.Road},
		states:      statesOnRoad,
		stateArgs:   []any{q.Road},
	})
}

// SituationsNear reads the active situations within the radius of a
// position, by severity descending, then distance, then id, past q's
// offset; and, for the
// lamp, the view of every situation in the scope (the radius), the weather
// warnings in force in an area within the radius, and the states of the
// junctions within the radius, else of the junction nearest the position.
func (s *Store) SituationsNear(ctx context.Context, q roads.AreaQuery, fams roads.Families, now time.Time) (roads.Page, error) {
	if q.State != "" {
		return s.situationsInState(ctx, q, fams, now)
	}
	classes, other := q.KnownClasses()
	args := append(filterArgs(q.Filter, fams.Roads, now), q.At.Lat, q.At.Lon, q.RadiusM,
		codes(classes), other, codes(roads.Classes[:len(roads.Classes)-1]), q.Limit+1)
	start, end := q.Window(now)
	args = append(args, start, end, q.Offset)
	return s.read(ctx, situationsNear, args, q.Limit, fams, now, scope{
		messages: messagesNear,
		args:     []any{fams.Roads, now, q.At.Lat, q.At.Lon, q.RadiusM},
		// A warning is near when its own area reaches within the radius, as
		// the platform's warnings at a point test the warning's area.
		warnings:    warningsNear,
		warningArgs: []any{q.At.Lat, q.At.Lon, q.RadiusM},
		states:      statesNear,
		stateArgs:   []any{q.At.Lat, q.At.Lon, q.RadiusM},
	})
}

// situationsInState reads the active situations of one Land (the Land of
// the nearest Gemeinde to each situation's point, trafficEvents'
// state_code; de: every situation), by severity descending, then road and id, past q's offset; the lamp's scope is
// the Land: its situations, the weather warnings of its areas and the Land.
func (s *Store) situationsInState(ctx context.Context, q roads.AreaQuery, fams roads.Families, now time.Time) (roads.Page, error) {
	classes, other := q.KnownClasses()
	args := append(filterArgs(q.Filter, fams.Roads, now), q.State,
		codes(classes), other, codes(roads.Classes[:len(roads.Classes)-1]), q.Limit+1)
	start, end := q.Window(now)
	args = append(args, start, end, q.Offset)
	return s.read(ctx, situationsInStateList, args, q.Limit, fams, now, scope{
		messages: messagesInState,
		args:     []any{fams.Roads, now, q.State},
		// A Land's warnings are those of its warn cells: the DWD's Gemeinde
		// and coast cells are 8 or 7 and the Gemeinde key, so the Land is
		// the key's first two digits, as the platform's warnings in an area
		// read them.
		warnings:    warningsInState,
		warningArgs: []any{q.State},
		states:      statesInState,
		stateArgs:   []any{q.State},
		// The Land form places every situation of the scope by its nearest
		// municipality: the families that placed them are credited.
		placed: placedInState,
	})
}

// Situation reads one published situation of the given families that is
// active or ended after endedSince (roads.ended_retention_h).
func (s *Store) Situation(ctx context.Context, id string, families []string, endedSince time.Time) (roads.Situation, error) {
	rows, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roads.Situation, error) {
		res, _ := run(ctx, tx, situationByID, id, families, endedSince)
		return pgx.CollectRows(res, scanSituation)
	})
	if err == nil && len(rows) == 0 {
		err = roads.ErrUnknownSituation
	}
	if err != nil {
		return roads.Situation{}, err
	}
	return rows[0], nil
}
