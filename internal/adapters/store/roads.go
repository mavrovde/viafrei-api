package store

import (
	"context"
	"fmt"
	"slices"
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
	var out []roads.Road
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, _ := tx.Query(ctx, `
select road_ref, road_class, length_km::float8, states
from contract.road
where upper(replace(road_ref, ' ', '')) = $1
order by road_ref
limit 1`, ref)
		var err error
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.Road, error) {
			var (
				v          roads.Road
				ref, class string
			)
			err := r.Scan(&ref, &class, &v.LengthKm, &v.States)
			v.Ref, v.Class = roads.Canonical(ref), roads.MapClass(class)
			return v, err
		})
		return err
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
const situationColumns = `e.event_id, e.source_ref, e.event_kind, e.severity, e.road_ref, e.road_class,
       e.direction, e.direction_label, st_y(e.location), st_x(e.location), e.valid_from, e.valid_to,
       e.lanes_closed, e.lanes_total, e.delay_min, e.affected_length_m, e.headline, e.description,
       e.text_lang, e.changed_at`

// scopeFilter is the condition of the lamp's scope: an active, published
// situation of the given families. $1 families, $2 now. The lamp reads every
// situation in the scope (the road and direction, or the radius), whatever
// the list's filters keep.
const scopeFilter = `e.withdrawn_at is null
  and e.source_ref = any($1)
  and e.valid_from <= $2 and (e.valid_to is null or e.valid_to > $2)`

// situationFilter is the condition every list read applies: the scope's,
// then the asked kinds and the severity floor. $3 kinds kept, $4 whether
// unmapped kinds are kept, $5 every mapped kind, $6 the ordered severities,
// $7 the floor.
const situationFilter = scopeFilter + `
  and (e.event_kind = any($3) or ($4 and not e.event_kind = any($5)))
  and coalesce(array_position($6::text[], e.severity), 0) >= $7`

// canonicalRef is a stored road reference compared in its canonical form,
// as Road compares it.
const canonicalRef = `upper(replace(%s, ' ', ''))`

// filterArgs are the arguments $1 to $7 of situationFilter.
func filterArgs(f roads.Filter, families []string, now time.Time) []any {
	kinds, other := f.KnownKinds()
	return []any{families, now, codes(kinds), other, codes(roads.Kinds[:len(roads.Kinds)-1]), codes(roads.Ordered()), f.MinSeverity.Floor()}
}

// messagesSelect aggregates the lamp's view of every situation in the scope
// as a JSON array of {"r": record, "k": kind, "h": headline, "s": subtitle,
// "t": description, "p": place words, "b": blocked, "y": traffic type,
// "ds": delay in seconds, "d": delay in minutes}; null when the scope holds
// none.
const messagesSelect = `select jsonb_agg(distinct jsonb_build_object('r', e.record_class, 'k', e.event_kind, 'h', e.headline,
                                         's', e.subtitle, 't', e.description, 'p', e.place_text,
                                         'b', coalesce(e.is_blocked, false), 'y', e.traffic_type,
                                         'ds', e.delay_s, 'd', e.delay_min))
from contract.traffic_event e
where ` + scopeFilter

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

// warningsScope is the condition of a weather warning the lamp reads: a
// published, not cancelled warning of the given families in force at now,
// placed in an area. $1 families, $2 now.
const warningsScope = `from contract.weather_warning w
join contract.admin_region r on r.region_code = w.region_code
where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1)
  and w.valid_from <= $2 and (w.valid_to is null or w.valid_to > $2)`

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
order by state_code, source_ref`

// statesOf aggregates the distinct, known state codes of the junctions a
// query selects (column state_code) into a sorted array, empty when none.
func statesOf(junctions string) string {
	return `select coalesce(array_agg(distinct state_code order by state_code) filter (where state_code is not null), '{}')
from (` + junctions + `) j`
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
}

func scanSituation(r pgx.CollectableRow) (roads.Situation, error) {
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
	err := r.Scan(&v.ID, &v.Family, &kind, &severity, &road, &class,
		&direction, &v.Towards, &lat, &lon, &from, &until,
		&lanesClosed, &lanesTotal, &delay, &length, &headline, &v.Description,
		&lang, &updated, &v.AtKm, &v.DistanceM)
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
	var (
		out    = roads.Page{Confirmations: map[string]ampel.Confirmation{}}
		stored []storedMessage
	)
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The reads in order; the first that fails ends the snapshot.
		for _, step := range []func() error{
			func() (err error) {
				res, _ := tx.Query(ctx, list, listArgs...)
				out.Items, err = pgx.CollectRows(res, scanSituation)
				return err
			},
			func() error { return tx.QueryRow(ctx, sc.messages, sc.args...).Scan(&stored) },
			func() error {
				if len(fams.Weather) == 0 {
					return nil
				}
				res, _ := tx.Query(ctx, `select w.source_ref, w.warning_level `+warningsScope+`
  and `+sc.warnings+`
order by w.warning_level desc, w.warning_id`, append([]any{fams.Weather, now}, sc.warningArgs...)...)
				found, err := pgx.CollectRows(res, func(r pgx.CollectableRow) (roads.Warning, error) {
					var w roads.Warning
					return w, r.Scan(&w.Family, &w.Level)
				})
				// A read that found none is an empty list, never nil: nil
				// means the warnings were not read.
				out.Warnings = append([]roads.Warning{}, found...)
				return err
			},
			func() (err error) {
				res, _ := tx.Query(ctx, sourcesSelect, append(slices.Clone(fams.Roads), fams.Weather...))
				out.Sources, err = pgx.CollectRows(res, func(r pgx.CollectableRow) (provenance.Family, error) {
					var (
						f          provenance.Family
						staleAfter int64
						held       bool
						c          ampel.Confirmation
					)
					err := r.Scan(&f.Source.Family, &staleAfter, &f.DataAsOf, &held, &c.At, &c.IntervalS, &c.Paused)
					f.StaleAfter = time.Duration(staleAfter) * time.Second
					if held {
						out.Confirmations[f.Source.Family] = c
					}
					return f, err
				})
				return err
			},
			func() error { return tx.QueryRow(ctx, sc.states, sc.stateArgs...).Scan(&out.States) },
			func() (err error) {
				res, _ := tx.Query(ctx, coverageSelect, out.States, fams.Roads)
				out.Coverage, err = coverageOf(res)
				return err
			},
		} {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return roads.Page{}, err
	}
	for _, m := range stored {
		out.Messages = append(out.Messages, roads.Message{Record: roads.MapRecord(m.Record), Kind: roads.MapKind(m.Kind),
			Headline: m.Headline, Subtitle: m.Subtitle, Description: m.Description, PlaceText: m.PlaceText,
			Blocked: m.Blocked, TrafficType: m.TrafficType, DelayS: m.DelayS, DelayMin: m.DelayMin})
	}
	if len(out.Items) > limit {
		out.Items, out.HasMore = out.Items[:limit], true
	}
	return out, nil
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
// come last, then severity descending, then id; and it returns, for the
// lamp, the view of every situation in the scope (the road and direction),
// the weather warnings in force in an area the road's line crosses, and the
// states of the road's junctions.
// A situation's road reference is compared in its canonical form, as the
// road's is: A1 and A 1 are one road.
func (s *Store) SituationsOnRoad(ctx context.Context, q roads.CorridorQuery, fams roads.Families, now time.Time) (roads.Page, error) {
	args := append(filterArgs(q.Filter, fams.Roads, now), q.Road, string(q.Direction), codes(roads.Compass()), q.Limit+1)
	return s.read(ctx, `
with parts as (
  select r.road_ref, d.path[1] n, d.geom g, st_length(d.geom::geography) len, r.length_km::float8 km
  from contract.road r, st_dump(r.geometry) d
  where r.road_ref = (select road_ref from contract.road
                      where `+fmt.Sprintf(canonicalRef, "road_ref")+` = $8 order by road_ref limit 1)),
line as (
  select road_ref, max(km) km, sum(len) total,
         st_startpoint((array_agg(g order by n))[1]) first_point,
         st_endpoint((array_agg(g order by n desc))[1]) last_point
  from parts group by road_ref),
travel as (
  select case $9::text
           when 'north' then st_y(last_point) < st_y(first_point)
           when 'south' then st_y(last_point) > st_y(first_point)
           when 'east'  then st_x(last_point) < st_x(first_point)
           when 'west'  then st_x(last_point) > st_x(first_point)
           else false end falling
  from line),
answering as (
  select e.*, line.km road_km, line.total road_total
  from contract.traffic_event e, line
  where `+situationFilter+`
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
       null::int
from placed e
order by case when coalesce((select falling from travel), false) then -1 else 1 end * e.share nulls last,
         coalesce(array_position($6::text[], e.severity), 0) desc, e.event_id
limit $11`, args, q.Limit, fams, now, scope{
		messages: messagesSelect + `
  and ` + fmt.Sprintf(canonicalRef, "e.road_ref") + ` = $3
  and ($4::text = 'both' or e.direction = $4::text or not e.direction = any($5))`,
		args: []any{fams.Roads, now, q.Road, string(q.Direction), codes(roads.Compass())},
		warnings: `st_intersects(r.boundary, (select g.geometry from contract.road g
                                  where ` + fmt.Sprintf(canonicalRef, "g.road_ref") + ` = $3 order by g.road_ref limit 1))`,
		warningArgs: []any{q.Road},
		states: statesOf(`select state_code from contract.junction
where ` + fmt.Sprintf(canonicalRef, "road_ref") + ` = $1`),
		stateArgs: []any{q.Road},
	})
}

// SituationsNear reads the active situations within the radius of a
// position, by severity descending, then distance, then id; and, for the
// lamp, the view of every situation in the scope (the radius), the weather
// warnings in force in an area within the radius, and the states of the
// junctions within the radius, else of the junction nearest the position.
func (s *Store) SituationsNear(ctx context.Context, q roads.AreaQuery, fams roads.Families, now time.Time) (roads.Page, error) {
	classes, other := q.KnownClasses()
	args := append(filterArgs(q.Filter, fams.Roads, now), q.At.Lat, q.At.Lon, q.RadiusM,
		codes(classes), other, codes(roads.Classes[:len(roads.Classes)-1]), q.Limit+1)
	return s.read(ctx, `
with here as (select st_setsrid(st_makepoint($9, $8), 4326)::geography g),
answering as (
  select e.*, round(st_distance(e.location::geography, here.g))::int dist
  from contract.traffic_event e, here
  where `+situationFilter+`
    and e.location is not null
    and st_dwithin(e.location::geography, here.g, $10)
    and (e.road_class = any($11) or ($12 and not e.road_class = any($13))))
select `+situationColumns+`, null::float8, e.dist
from answering e
order by coalesce(array_position($6::text[], e.severity), 0) desc, e.dist, e.event_id
limit $14`, args, q.Limit, fams, now, scope{
		messages: messagesSelect + `
  and e.location is not null
  and st_dwithin(e.location::geography, st_setsrid(st_makepoint($4, $3), 4326)::geography, $5)`,
		args:        []any{fams.Roads, now, q.At.Lat, q.At.Lon, q.RadiusM},
		warnings:    `st_dwithin(r.boundary::geography, st_setsrid(st_makepoint($4, $3), 4326)::geography, $5)`,
		warningArgs: []any{q.At.Lat, q.At.Lon, q.RadiusM},
		states: statesOf(`with here as (select st_setsrid(st_makepoint($2, $1), 4326) g),
near as (select j.state_code from contract.junction j, here
         where st_dwithin(j.location::geography, here.g::geography, $3))
select state_code from near
union all
select (select j.state_code from contract.junction j, here order by j.location <-> here.g, j.junction_id limit 1)
where not exists (select 1 from near)`),
		stateArgs: []any{q.At.Lat, q.At.Lon, q.RadiusM},
	})
}

// Situation reads one published situation of the given families that is
// active or ended after endedSince (roads.ended_retention_h).
func (s *Store) Situation(ctx context.Context, id string, families []string, endedSince time.Time) (roads.Situation, error) {
	var rows []roads.Situation
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		res, _ := tx.Query(ctx, `
select `+situationColumns+`, null::float8, null::int
from contract.traffic_event e
where e.event_id = $1 and e.withdrawn_at is null and e.source_ref = any($2)
  and (e.valid_to is null or e.valid_to > $3)`, id, families, endedSince)
		var err error
		rows, err = pgx.CollectRows(res, scanSituation)
		return err
	})
	if err == nil && len(rows) == 0 {
		err = roads.ErrUnknownSituation
	}
	if err != nil {
		return roads.Situation{}, err
	}
	return rows[0], nil
}
