package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
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

// signalsSelect aggregates the lamp's view of every situation in the scope
// as a JSON array of {"k": kind, "s": severity, "d": delay}; null when the
// scope holds none.
const signalsSelect = `select jsonb_agg(distinct jsonb_build_object('k', e.event_kind, 's', e.severity, 'd', e.delay_min))
from contract.traffic_event e
where ` + scopeFilter

// storedSignal is one element of signalsColumn, in stored codes.
type storedSignal struct {
	Kind     string `json:"k"`
	Severity string `json:"s"`
	DelayMin *int   `json:"d"`
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

// page cuts the rows to the limit and maps the signals of every situation
// in the scope through the domain's codes.
func page(rows []roads.Situation, stored []storedSignal, limit int) ([]roads.Situation, []roads.Signal, bool) {
	var signals []roads.Signal
	for _, s := range stored {
		signals = append(signals, roads.Signal{Kind: roads.MapKind(s.Kind), Severity: roads.MapSeverity(s.Severity), DelayMin: s.DelayMin})
	}
	if len(rows) > limit {
		return rows[:limit], signals, true
	}
	return rows, signals, false
}

// read runs the page query and the scope's signals query in one snapshot.
func (s *Store) read(ctx context.Context, list string, listArgs []any, signals string, signalArgs []any) ([]roads.Situation, []storedSignal, error) {
	var (
		rows   []roads.Situation
		stored []storedSignal
	)
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		res, _ := tx.Query(ctx, list, listArgs...)
		var err error
		if rows, err = pgx.CollectRows(res, scanSituation); err != nil {
			return err
		}
		return tx.QueryRow(ctx, signals, signalArgs...).Scan(&stored)
	})
	return rows, stored, err
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
// come last, then severity descending, then id; and it returns the signals
// of every situation in the scope (the road and direction), for the lamp.
// A situation's road reference is compared in its canonical form, as the
// road's is: A1 and A 1 are one road.
func (s *Store) SituationsOnRoad(ctx context.Context, q roads.CorridorQuery, families []string, now time.Time) ([]roads.Situation, []roads.Signal, bool, error) {
	args := append(filterArgs(q.Filter, families, now), q.Road, string(q.Direction), codes(roads.Compass()), q.Limit+1)
	rows, stored, err := s.read(ctx, `
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
limit $11`, args,
		signalsSelect+`
  and `+fmt.Sprintf(canonicalRef, "e.road_ref")+` = $3
  and ($4::text = 'both' or e.direction = $4::text or not e.direction = any($5))`,
		[]any{families, now, q.Road, string(q.Direction), codes(roads.Compass())})
	if err != nil {
		return nil, nil, false, err
	}
	sits, sigs, more := page(rows, stored, q.Limit)
	return sits, sigs, more, nil
}

// SituationsNear reads the active situations within the radius of a
// position, by severity descending, then distance, then id; and the
// signals of every situation in the scope (the radius), for the lamp.
func (s *Store) SituationsNear(ctx context.Context, q roads.AreaQuery, families []string, now time.Time) ([]roads.Situation, []roads.Signal, bool, error) {
	classes, other := q.KnownClasses()
	args := append(filterArgs(q.Filter, families, now), q.At.Lat, q.At.Lon, q.RadiusM,
		codes(classes), other, codes(roads.Classes[:len(roads.Classes)-1]), q.Limit+1)
	rows, stored, err := s.read(ctx, `
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
limit $14`, args,
		signalsSelect+`
  and e.location is not null
  and st_dwithin(e.location::geography, st_setsrid(st_makepoint($4, $3), 4326)::geography, $5)`,
		[]any{families, now, q.At.Lat, q.At.Lon, q.RadiusM})
	if err != nil {
		return nil, nil, false, err
	}
	sits, sigs, more := page(rows, stored, q.Limit)
	return sits, sigs, more, nil
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
