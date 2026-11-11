package store

import (
	"context"
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
// order scanSituation reads them, followed by its three question-dependent
// columns: the position along the road, the distance and the severities of
// every situation that answers the question.
const situationColumns = `e.event_id, e.source_ref, e.event_kind, e.severity, e.road_ref, e.road_class,
       e.direction, e.direction_label, st_y(e.location), st_x(e.location), e.valid_from, e.valid_to,
       e.lanes_closed, e.lanes_total, e.delay_min, e.affected_length_m, e.headline, e.description,
       e.text_lang, e.changed_at`

// situationFilter is the condition every list read applies: an active,
// published situation of the given families, of the asked kinds, at or above
// the asked severity floor. $1 families, $2 now, $3 kinds kept, $4 whether
// unmapped kinds are kept, $5 every mapped kind, $6 the ordered severities,
// $7 the floor.
const situationFilter = `e.withdrawn_at is null
  and e.source_ref = any($1)
  and e.valid_from <= $2 and (e.valid_to is null or e.valid_to > $2)
  and (e.event_kind = any($3) or ($4 and not e.event_kind = any($5)))
  and coalesce(array_position($6::text[], e.severity), 0) >= $7`

// filterArgs are the arguments $1 to $7 of situationFilter.
func filterArgs(f roads.Filter, families []string, now time.Time) []any {
	kinds, other := f.KnownKinds()
	return []any{families, now, codes(kinds), other, codes(roads.Kinds[:len(roads.Kinds)-1]), codes(roads.Ordered()), f.MinSeverity.Floor()}
}

// situationRow is one situation read with its question-dependent columns.
type situationRow struct {
	sit        roads.Situation
	severities []string
}

func scanSituation(r pgx.CollectableRow) (situationRow, error) {
	var (
		v                                      situationRow
		kind, severity, class, direction       string
		road                                   *string
		lat, lon                               *float64
		from, updated                          time.Time
		until                                  *time.Time
		headline, lang                         string
		lanesClosed, lanesTotal, delay, length *int
	)
	err := r.Scan(&v.sit.ID, &v.sit.Family, &kind, &severity, &road, &class,
		&direction, &v.sit.Towards, &lat, &lon, &from, &until,
		&lanesClosed, &lanesTotal, &delay, &length, &headline, &v.sit.Description,
		&lang, &updated, &v.sit.AtKm, &v.sit.DistanceM, &v.severities)
	s := &v.sit
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

// page cuts the rows to the limit and maps the severities of every
// situation that answers the question.
func page(rows []situationRow, limit int) ([]roads.Situation, []roads.Severity, bool) {
	var severities []roads.Severity
	if len(rows) > 0 {
		for _, s := range rows[0].severities {
			severities = append(severities, roads.MapSeverity(s))
		}
	}
	out := make([]roads.Situation, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.sit)
	}
	if len(out) > limit {
		return out[:limit], severities, true
	}
	return out, severities, false
}

// SituationsOnRoad reads the active situations on one road in the order of
// travel (FR-ROAD-003): by ascending chainage for both directions, and for
// one direction from where the carriageway's traffic comes from, decided by
// the road line's own ends (a road whose line ends further north is driven
// north with rising chainage). Those that cannot be placed come last, then
// severity descending, then id; and it returns the severities of every
// situation that answers the question, for the lamp.
func (s *Store) SituationsOnRoad(ctx context.Context, q roads.CorridorQuery, families []string, now time.Time) ([]roads.Situation, []roads.Severity, bool, error) {
	var rows []situationRow
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		args := append(filterArgs(q.Filter, families, now), q.Road, string(q.Direction), codes(roads.Compass()), q.Limit+1)
		res, _ := tx.Query(ctx, `
with line as (
  select r.road_ref, st_geometryn(r.geometry, 1) g, r.length_km::float8 km
  from contract.road r
  where upper(replace(r.road_ref, ' ', '')) = $8),
travel as (
  select case $9::text
           when 'north' then st_y(st_endpoint(g)) < st_y(st_startpoint(g))
           when 'south' then st_y(st_endpoint(g)) > st_y(st_startpoint(g))
           when 'east'  then st_x(st_endpoint(g)) < st_x(st_startpoint(g))
           when 'west'  then st_x(st_endpoint(g)) > st_x(st_startpoint(g))
           else false end falling
  from line),
answering as (
  select e.*, line.g road_line, line.km road_km
  from contract.traffic_event e
  join line on line.road_ref = e.road_ref
  where `+situationFilter+`
    and ($9::text = 'both' or e.direction = $9::text or not e.direction = any($10)))
select `+situationColumns+`,
       case when e.location is null or e.road_line is null or e.road_km is null then null
            else round((st_linelocatepoint(e.road_line, e.location) * e.road_km)::numeric, 1)::float8 end km,
       null::int,
       (select array_agg(distinct a.severity) from answering a)
from answering e
order by case when coalesce((select falling from travel), false) then -1 else 1 end
           * (st_linelocatepoint(e.road_line, e.location)) nulls last,
         coalesce(array_position($6::text[], e.severity), 0) desc, e.event_id
limit $11`, args...)
		var err error
		rows, err = pgx.CollectRows(res, scanSituation)
		return err
	})
	sits, sevs, more := page(rows, q.Limit)
	return sits, sevs, more, err
}

// SituationsNear reads the active situations within the radius of a
// position, by severity descending, then distance, then id; and the
// severities of every situation that answers the question, for the lamp.
func (s *Store) SituationsNear(ctx context.Context, q roads.AreaQuery, families []string, now time.Time) ([]roads.Situation, []roads.Severity, bool, error) {
	var rows []situationRow
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		classes, other := q.KnownClasses()
		args := append(filterArgs(q.Filter, families, now), q.At.Lat, q.At.Lon, q.RadiusM,
			codes(classes), other, codes(roads.Classes[:len(roads.Classes)-1]), q.Limit+1)
		res, _ := tx.Query(ctx, `
with here as (select st_setsrid(st_makepoint($9, $8), 4326)::geography g),
answering as (
  select e.*, round(st_distance(e.location::geography, here.g))::int dist
  from contract.traffic_event e, here
  where `+situationFilter+`
    and e.location is not null
    and st_dwithin(e.location::geography, here.g, $10)
    and (e.road_class = any($11) or ($12 and not e.road_class = any($13))))
select `+situationColumns+`, null::float8, e.dist,
       (select array_agg(distinct a.severity) from answering a)
from answering e
order by coalesce(array_position($6::text[], e.severity), 0) desc, e.dist, e.event_id
limit $14`, args...)
		var err error
		rows, err = pgx.CollectRows(res, scanSituation)
		return err
	})
	sits, sevs, more := page(rows, q.Limit)
	return sits, sevs, more, err
}

// Situation reads one published situation of the given families, active or
// ended.
func (s *Store) Situation(ctx context.Context, id string, families []string) (roads.Situation, error) {
	var rows []situationRow
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		res, _ := tx.Query(ctx, `
select `+situationColumns+`, null::float8, null::int, null::text[]
from contract.traffic_event e
where e.event_id = $1 and e.withdrawn_at is null and e.source_ref = any($2)`, id, families)
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
	return rows[0].sit, nil
}
