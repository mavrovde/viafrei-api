package store

import (
	"context"
	"fmt"
	"slices"
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

// Road reads one road by its canonical reference.
func (s *Store) Road(ctx context.Context, ref string) (roads.Road, error) {
	out, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roads.Road, error) {
		rows, _ := tx.Query(ctx, `
select road_ref, road_class, length_km::float8, states
from contract.road r
where upper(replace(road_ref, ' ', '')) = $1 and `+permitted("r")+`
order by road_ref
limit 1`, ref)
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
       e.text_lang, e.changed_at`

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

// eventRow is every column of contract.traffic_event a list read carries
// forward, never state_code: a Land is looked up only where it is asked for.
var eventRow = "e." + strings.Join(strings.Split("event_id, event_kind, severity, road_ref, road_class, direction, direction_label, location, valid_from, valid_to, lanes_closed, lanes_total, delay_min, affected_length_m, headline, description, text_lang, source_ref, origin_key, published_at, recorded_at, changed_at, withdrawn_at, record_class, subtitle, place_text, is_blocked, traffic_type, delay_s, source_refs, display_class, audience, confidentiality, origin_restriction", ", "), ", e.")

// eventPermitted is rowRule over the traffic events aliased e.
const eventPermitted = `e.display_class <> 'withhold' and (e.audience = 'public' or ` + ownKeyOn + `)`

// canonicalRef is a stored road reference compared in its canonical form,
// as Road compares it.
const canonicalRef = `upper(replace(%s, ' ', ''))`

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
const messagesSelect = `select ` + messagesAgg + `,
       ` + nextChange + `
` + messagesFrom

// messagesAgg is the lamp's view of the situations of messagesFrom active
// at $2, as messagesSelect aggregates it.
const messagesAgg = `jsonb_agg(distinct jsonb_build_object('r', e.record_class, 'k', e.event_kind, 'h', e.headline,
                                         's', e.subtitle, 't', e.description, 'p', e.place_text,
                                         'b', coalesce(e.is_blocked, false), 'y', e.traffic_type,
                                         'ds', e.delay_s, 'd', ` + roadDelayMin + `)) filter (where e.valid_from <= $2)`

// messagesFrom reads the situations of the families $1 that have not ended
// at $2.
const messagesFrom = `from contract.traffic_event e
cross join lateral (select e.valid_from, e.valid_to) x
where e.withdrawn_at is null
  and e.source_ref = any($1)
  and ` + eventPermitted + `
  and (e.valid_to is null or e.valid_to > $2)`

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

// warningsPending is the condition of a weather warning the lamp reads or
// will read: a published, not cancelled warning of the given families that
// has not ended at now. $1 families, $2 now. Those whose valid_from is at or
// before now are in force; the others start later. Each scope places it by
// the warning's own area or its warn cell, as the platform places a DWD
// warning, never by the region code it is filed under: the code is often
// null, and a join on it answered "no warning" where the area holds one.
const warningsPending = `from contract.weather_warning w
where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1)
  and w.display_class <> 'withhold' and (w.audience = 'public' or ` + ownKeyOn + `)
  and (w.valid_to is null or w.valid_to > $2)`

// sourcesSelect reads the freshness of the given families and the newest
// confirmation of those that have a freshness row. $1 families.
const sourcesSelect = `select s.source_ref, s.stale_after_s, f.data_as_of,
       f.source_ref is not null, f.confirmed_at, f.confirm_interval_s::float8, coalesce(f.updates_paused, false)
from contract.source s
left join contract.source_freshness f using (source_ref)
where s.source_ref = any($1)
order by s.source_ref`

// coverageSelect reads which of the given families cover the traffic
// messages of the given states. $1 states, $2 families.
const coverageSelect = `select state_code, source_ref, coverage_role, current_only
from contract.message_coverage
where state_code = any($1) and source_ref = any($2)
  and display_class <> 'withhold' and (audience = 'public' or ` + ownKeyOn + `)
order by state_code, source_ref`

// statesOf aggregates the distinct, known state codes of the junctions a
// query selects (column state_code) into a sorted array, empty when none.
func statesOf(junctions string) string {
	return `select coalesce(array_agg(distinct state_code order by state_code) filter (where state_code is not null), '{}')
from (` + junctions + `) j`
}

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

// scope is one read of the lamp's scope, run in the page's snapshot.
type scope struct {
	messages string
	args     []any
	// warnings is the area condition of the weather warnings, over $3
	// onwards of warningArgs.
	warnings    string
	warningArgs []any
	// states selects the codes of the states in scope, over stateArgs.
	states    string
	stateArgs []any
	// placed selects the families that placed the scope's situations in a
	// Land, over args; empty when the scope is no Land.
	placed string
	// covering selects, over coveringArgs, the families read that hold a
	// situation on a road of an unregistered road's class; empty for any
	// other scope.
	covering     string
	coveringArgs []any
}

// coveringSelect reads which of the given families hold a situation that
// has not ended on a road of a class, in name order. $1 families, $2 the
// stored class, $3 now.
const coveringSelect = `select coalesce(array_agg(f order by f), '{}')
from unnest($1::text[]) f
where exists (select 1 from contract.traffic_event e
              where e.source_ref = f and e.withdrawn_at is null and e.road_class = $2
                and (e.valid_to is null or e.valid_to > $3)
                and ` + eventPermitted + `)`

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
		&lang, &updated, &v.AtKm, &v.DistanceM}, extra...)...)
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

// read runs the page query and the reads of the lamp's scope (its
// situations, the weather warnings in force there when a weather family is
// read, the freshness and newest confirmation of every family read, the
// states in scope and which roads families read cover their traffic
// messages) in one snapshot.
func (s *Store) read(ctx context.Context, list string, listArgs []any, limit int, fams roads.Families, now time.Time, sc scope) (roads.Page, error) {
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
				res, _ := tx.Query(ctx, list, listArgs...)
				rows, err := pgx.CollectRows(res, scanCounted)
				// Every row carries the counts of the whole filtered set, the
				// families past the limit too; none listed is none counted.
				out.Items, out.Counts = make([]roads.Situation, len(rows)), map[string]int{}
				for i, r := range rows {
					out.Items[i], out.Counts = r.situation, r.counts
				}
				return err
			},
			func() error { return tx.QueryRow(ctx, sc.messages, sc.args...).Scan(&stored, &out.NextChange) },
			func() error {
				if len(fams.Weather) == 0 {
					return nil
				}
				// One read of the warnings that have not ended: those in
				// force are the lamp's, and the earliest start or end after
				// now of any is when the scope changes.
				res, _ := tx.Query(ctx, `select w.source_ref, w.warning_level, w.valid_from <= $2,
  case when w.valid_from > $2 then w.valid_from else w.valid_to end `+warningsPending+`
  and `+sc.warnings+`
order by w.warning_level desc, w.warning_id`, append([]any{fams.Weather, now}, sc.warningArgs...)...)
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
				res, _ := tx.Query(ctx, sourcesSelect, append(slices.Clone(fams.Roads), fams.Weather...))
				out.Sources, err = sourcesOf(res, out.Confirmations)
				return err
			},
			func() error { return tx.QueryRow(ctx, sc.states, sc.stateArgs...).Scan(&out.States) },
			func() error {
				if sc.placed == "" {
					return nil
				}
				return tx.QueryRow(ctx, sc.placed, sc.args...).Scan(&out.Placed)
			},
			func() error {
				if sc.covering == "" {
					return nil
				}
				return tx.QueryRow(ctx, sc.covering, sc.coveringArgs...).Scan(&out.Covering)
			},
			func() (err error) {
				res, _ := tx.Query(ctx, coverageSelect, out.States, fams.Roads)
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
	return s.read(ctx, `
with road as (
  select k.road_ref from contract.road k
  where `+fmt.Sprintf(canonicalRef, "k.road_ref")+` = $8 and `+permitted("k")+` order by k.road_ref limit 1),
parts as (
  select r.road_ref, d.path[1] n, d.geom g, st_length(d.geom::geography) len, r.length_km::float8 km
  from contract.road r, st_dump(r.geometry) d
  where r.road_ref = (select road_ref from road)),
line as (
  select road_ref, max(km) km, sum(len) total
  from parts group by road_ref),
answering as (
  select `+eventRow+`, line.km road_km, line.total road_total
  from contract.traffic_event e left join line on true
  where `+situationFilter(12)+`
    and ($15 or exists (select 1 from road))
    and `+fmt.Sprintf(canonicalRef, "e.road_ref")+` = $8
    and ($9::text = 'both' or e.direction = $9::text or not e.direction = any($10))),
placed as (
  select a.*, at.share
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
limit $11 offset $14`, args, q.Limit, fams, now, scope{
		covering: sc.covering, coveringArgs: sc.coveringArgs,
		messages: messagesSelect + `
  and ` + fmt.Sprintf(canonicalRef, "e.road_ref") + ` = $3
  and ($4::text = 'both' or e.direction = $4::text or not e.direction = any($5))`,
		args: []any{fams.Roads, now, q.Road, string(q.Direction), codes(roads.Compass())},
		// A warning is on the road when its own area holds one of the
		// road's junction points, as the platform's motorway page and its
		// motorway status place a DWD warning on the location-code points
		// of the road, not on the districts the road crosses.
		warnings: `exists (select 1 from contract.junction j
               where ` + fmt.Sprintf(canonicalRef, "j.road_ref") + ` = $3 and ` + permitted("j") + `
                 and st_intersects(w.area, j.location))`,
		warningArgs: []any{q.Road},
		states: statesOf(`select j.state_code from contract.junction j
where ` + fmt.Sprintf(canonicalRef, "j.road_ref") + ` = $1 and ` + permitted("j")),
		stateArgs: []any{q.Road},
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
	return s.read(ctx, `
with here as (select st_setsrid(st_makepoint($9, $8), 4326)::geography g),
answering as (
  select `+eventRow+`, round(st_distance(e.location::geography, here.g))::int dist
  from contract.traffic_event e, here
  where `+situationFilter(15)+`
    and e.location is not null
    and st_dwithin(e.location::geography, here.g, $10)
    and (e.road_class = any($11) or ($12 and not e.road_class = any($13))))
select `+situationColumns+`, null::float8, e.dist,
       (select jsonb_object_agg(c.source_ref, c.n) from (select source_ref, count(*)::int n from answering group by source_ref) c)
from answering e
order by coalesce(array_position($6::text[], e.severity), 0) desc, e.dist, e.event_id
limit $14 offset $17`, args, q.Limit, fams, now, scope{
		messages: messagesSelect + `
  and e.location is not null
  and st_dwithin(e.location::geography, st_setsrid(st_makepoint($4, $3), 4326)::geography, $5)`,
		args: []any{fams.Roads, now, q.At.Lat, q.At.Lon, q.RadiusM},
		// A warning is near when its own area reaches within the radius, as
		// the platform's warnings at a point test the warning's area.
		warnings:    `w.area is not null and st_dwithin(w.area::geography, st_setsrid(st_makepoint($4, $3), 4326)::geography, $5)`,
		warningArgs: []any{q.At.Lat, q.At.Lon, q.RadiusM},
		states: statesOf(`with here as (select st_setsrid(st_makepoint($2, $1), 4326) g),
near as (select j.state_code from contract.junction j, here
         where st_dwithin(j.location::geography, here.g::geography, $3) and ` + permitted("j") + `)
select state_code from near
union all
select (select j.state_code from contract.junction j, here where ` + permitted("j") + ` order by j.location <-> here.g, j.junction_id limit 1)
where not exists (select 1 from near)`),
		stateArgs: []any{q.At.Lat, q.At.Lon, q.RadiusM},
	})
}

// situationsInState reads the active situations of one Land (the Land of
// the nearest Gemeinde to each situation's point, contract.traffic_event's
// state_code; de: every situation), by severity descending, then road and id, past q's offset; the lamp's scope is
// the Land: its situations, the weather warnings of its areas and the Land.
func (s *Store) situationsInState(ctx context.Context, q roads.AreaQuery, fams roads.Families, now time.Time) (roads.Page, error) {
	classes, other := q.KnownClasses()
	args := append(filterArgs(q.Filter, fams.Roads, now), q.State,
		codes(classes), other, codes(roads.Classes[:len(roads.Classes)-1]), q.Limit+1)
	start, end := q.Window(now)
	args = append(args, start, end, q.Offset)
	return s.read(ctx, `
with answering as (
  select `+eventRow+`
  from contract.traffic_event e
  where `+situationFilter(13)+`
    and ($8 = 'de' or e.state_code = $8)
    and (e.road_class = any($9) or ($10 and not e.road_class = any($11))))
select `+situationColumns+`, null::float8, null::int,
       (select jsonb_object_agg(c.source_ref, c.n) from (select source_ref, count(*)::int n from answering group by source_ref) c)
from answering e
order by coalesce(array_position($6::text[], e.severity), 0) desc, e.road_ref, e.event_id
limit $12 offset $15`, args, q.Limit, fams, now, scope{
		messages: messagesSelect + `
  and ($3 = 'de' or e.state_code = $3)`,
		args: []any{fams.Roads, now, q.State},
		// A Land's warnings are those of its warn cells: the DWD's Gemeinde
		// and coast cells are 8 or 7 and the Gemeinde key, so the Land is
		// the key's first two digits, as the platform's warnings in an area
		// read them.
		warnings:    `($3 = 'de' or (left(w.warn_cell_id, 1) in ('7', '8') and substr(w.warn_cell_id, 2, 2) = $3))`,
		warningArgs: []any{q.State},
		states:      `select case when $1 = 'de' then (select coalesce(array_agg(r.region_code order by r.region_code), '{}') from contract.admin_region r where r.region_level = 'state') else array[$1::text] end`,
		stateArgs:   []any{q.State},
		// The Land form places every situation of the scope by its nearest
		// municipality: the families that placed them are credited.
		placed: `select coalesce(array_agg(distinct e.placement_source_ref order by e.placement_source_ref) filter (where e.placement_source_ref is not null), '{}')
from contract.traffic_event e
where $3 <> 'de' and e.state_code = $3 and e.withdrawn_at is null and e.source_ref = any($1)
  and ` + eventPermitted + ` and (e.valid_to is null or e.valid_to > $2)`,
	})
}

// Situation reads one published situation of the given families that is
// active or ended after endedSince (roads.ended_retention_h).
func (s *Store) Situation(ctx context.Context, id string, families []string, endedSince time.Time) (roads.Situation, error) {
	rows, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roads.Situation, error) {
		res, _ := tx.Query(ctx, `
select `+situationColumns+`, null::float8, null::int
from contract.traffic_event e
where e.event_id = $1 and e.withdrawn_at is null and e.source_ref = any($2)
  and `+eventPermitted+`
  and (e.valid_to is null or e.valid_to > $3)`, id, families, endedSince)
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
