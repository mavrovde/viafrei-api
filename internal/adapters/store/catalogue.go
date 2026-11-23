package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Motorways reads the motorway catalogue (reference rows of no family; the
// situations counted are the given families'; each road names the families of
// the situations it counts): each road
// (of one Land, or one road) with its length, Länder, junction count and the
// count of its situations in force at now, by road number. One road also
// carries its junctions. It also returns the families of the situations in
// force at now on any road of the register, whatever the query keeps: the
// families whose counts the catalogue states, zero included, which its answer
// credits as the site's motorway index does.
//
// A road's length and Länder come from the road snapshot (roadShapes), not
// from the read contract's road view, which derives them per road and per
// read: the list would walk the location code list for every road on every
// request.
func (s *Store) Motorways(ctx context.Context, q roads.CatalogueQuery, families []string, now time.Time) ([]roads.Motorway, []string, error) {
	shapes, err := s.roadShapes(ctx)
	if err != nil {
		return nil, nil, err
	}
	// The junction Länder of every road, while the background read of them
	// is current; else the list derives them live, in its own read.
	lands := s.lands.current(s.nowFn(), s.roads.staleFor)
	// The roads in the asked Land: its Länder hold it, from the snapshot, or
	// a junction of it is in it, from the junction Länder or live.
	var inState []string
	for ref, sh := range shapes {
		if q.State != "" && (slices.Contains(sh.states, q.State) || slices.Contains(lands[ref].states, q.State)) {
			inState = append(inState, ref)
		}
	}
	junctionInState := `
       or exists (select 1 from contract.junction j where j.road_ref = r.road_ref and j.state_code = $2::text)`
	if lands != nil {
		junctionInState = ``
	}
	// One road's exits name each junction's Land: from the junction Länder,
	// else live.
	road, warm := lands[q.Road]
	exits := motorwayExits
	if warm {
		exits = motorwayExitsWarm
	}
	var counted []string
	ms, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roads.Motorway, error) {
		res, _ := tx.Query(ctx, `
select coalesce(array_agg(distinct e.source_ref order by e.source_ref), '{}')
from contract.traffic_event e
join contract.road r on r.road_ref = e.road_ref and `+permitted("r")+`
where e.source_ref = any($1) and `+eventPermitted+`
  and e.withdrawn_at is null and e.valid_from <= $2 and (e.valid_to is null or e.valid_to > $2)`, families, now)
		c, err := pgx.CollectExactlyOneRow(res, pgx.RowTo[[]string])
		if err != nil {
			return nil, err
		}
		counted = c
		// The situations in force are read once and grouped by road, not
		// twice per road: each read of contract.traffic_event plans and scans
		// the whole view.
		res, _ = tx.Query(ctx, `
with es as (
  select e.road_ref, count(*)::int as n, array_agg(distinct e.source_ref order by e.source_ref) as sources
  from contract.traffic_event e
  where e.source_ref = any($1) and `+eventPermitted+`
    and e.withdrawn_at is null and e.valid_from <= $5 and (e.valid_to is null or e.valid_to > $5)
  group by e.road_ref)
select r.road_ref, r.name,
       (select count(*) from contract.junction j where j.road_ref = r.road_ref)::int,
       coalesce(es.n, 0),
       r.source_ref,
       coalesce(es.sources, '{}'),
       array(select distinct j.source_ref from contract.junction j where j.road_ref = r.road_ref order by 1)
from contract.road r
left join es on es.road_ref = r.road_ref
where `+permitted("r")+`
  and ($2::text = '' or r.road_ref = any($6::text[])`+junctionInState+`)
  and ($3::text = '' or r.road_ref = $3::text)
order by length(r.road_ref), r.road_ref
limit $4`, families, q.State, q.Road, q.Limit, now, inState)
		ms, err := pgx.CollectRows(res, func(r pgx.CollectableRow) (roads.Motorway, error) {
			var m roads.Motorway
			err := r.Scan(&m.Road, &m.Name, &m.Junctions, &m.Situations, &m.Family, &m.Sources, &m.Registers)
			sh := shapes[m.Road]
			// Never nil, as the view's coalesce(states, '{}') answered.
			m.LengthKm, m.States = sh.lengthKm, append([]string{}, sh.states...)
			return m, err
		})
		if err != nil || q.Road == "" || len(ms) == 0 {
			return ms, err
		}
		res, _ = run(ctx, tx, exits, q.Road)
		ms[0].Exits, err = pgx.CollectRows(res, func(r pgx.CollectableRow) (roads.Junction, error) {
			var j roads.Junction
			err := r.Scan(&j.ID, &j.Name, &j.Kind, &j.Number, &j.State, &j.Lat, &j.Lon)
			if warm {
				j.State = road.byJunction[j.ID]
			}
			return j, err
		})
		return ms, err
	})
	return ms, counted, err
}

// motorwayJunctions are the LCL's German motorway points with a name, the
// junctions of a motorway page, aliased t.
const motorwayJunctions = `t.cid = 58 and t.tabcd = 1 and t.tcd = 1 and t.road_number ~ '^A[1-9][0-9]{0,2}$'
  and t.geom is not null and t.name is not null`

// exitsOf reads the junctions of the road $1, each with its Land (state),
// by number (its leading digits, then as written) and id.
func exitsOf(name, state string) stmt {
	return named(name, `
select t.cid || ':' || t.tabcd || ':' || t.lcd, t.name,
       case t.stcd when 1 then 'interchange' when 2 then 'triangle' else 'junction' end, t.junction_no, `+state+`,
       st_y(t.geom::geometry(Point,4326)), st_x(t.geom::geometry(Point,4326))
from tmc_locations t
where `+motorwayJunctions+` and t.road_number = (select $1::text)
order by nullif(regexp_replace(coalesce(t.junction_no, ''), '[^0-9].*$', ''), '')::int nulls last, t.junction_no, 1`, "text")
}

var (
	// motorwayExits are the road's junctions with the Land of each, the
	// platform's (contract_junction_land: the Land of the nearest
	// municipality).
	motorwayExits = exitsOf("api_motorway_exits", `contract_junction_land(t.geom)`)
	// motorwayExitsWarm are the road's junctions without their Land, which
	// the junction Länder read answers.
	motorwayExitsWarm = exitsOf("api_motorway_exits_warm", `null::text`)
)

// roadStates reads, for each road reference of $1 (upper case without
// spaces), the Länder its junctions lie in, sorted, of the roads with a
// junction. A motorway point's reference is A and digits, as written, so it
// is its canonical form, and the points are read by it.
var roadStates = named("api_road_states", `
select q.ref, coalesce(array_agg(distinct l.code order by l.code) filter (where l.code is not null), '{}')
from unnest((select $1::text[])::text[]) q(ref)
join tmc_locations t on t.road_number = q.ref and `+motorwayJunctions+`
cross join lateral (select contract_junction_land(t.geom) code) l
group by q.ref`, "text[]")

// roadLampWarnings reads, for each road reference of $3, the weather
// warnings of the families $1 in force at $2 whose own area holds one of the
// road's junction points, as SituationsOnRoad places them (lampWarnings),
// worst first, then by id.
var roadLampWarnings = named("api_road_lamp_warnings", `
select q.ref, w.source_feed, w.level::int
from unnest((select $3::text[])::text[]) q(ref)
cross join lateral (
  select w.source_feed, w.level, w.id from weather_warnings w
  where w.deleted_at is null and w.valid_from is not null and w.closed_at is null
    and coalesce(w.closed_reason, '') <> 'withdrawn'
    and w.source_feed = any((select $1::text[])::text[])
    and (w.valid_to is null or w.valid_to > (select $2::timestamptz)) and w.valid_from <= (select $2::timestamptz)
    and w.id in (select o.id from tmc_locations t
                 join weather_warnings o on st_intersects(o.area::geometry, t.geom::geometry(Point,4326))
                   and o.closed_at is null and o.deleted_at is null
                 where t.road_number = q.ref and `+motorwayJunctions+`)) w
order by q.ref, w.level desc, 'dwd:' || w.id`, "text[]", "timestamptz", "text[]")

// RoadLamps reads, in one snapshot, what the lamp of each road (a stored
// road reference) states over the whole road in both directions, as
// SituationsOnRoad reads it for one road: the lamp's view of the
// situations on it, the weather warnings in force on its junctions, the
// freshness of every family read, the states of its junctions and their
// message coverage. A page carries no list of situations and no next
// change. The catalogue reads each road's lamp in a handful of statements,
// not a full conditions read per road.
func (s *Store) RoadLamps(ctx context.Context, refs []string, fams roads.Families, now time.Time) (map[string]roads.Page, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (map[string]roads.Page, error) {
		messages, warnings := map[string][]storedMessage{}, map[string][]roads.Warning{}
		states := map[string][]string{}
		confirmations := map[string]ampel.Confirmation{}
		var (
			sources  []provenance.Family
			coverage map[string]ampel.Coverage
		)
		// The reads in order; the first that fails ends the snapshot.
		for _, step := range []func() error{
			func() error {
				res, _ := tx.Query(ctx, `select `+fmt.Sprintf(canonicalRef, "e.road_ref")+`, `+messagesAgg+`
`+messagesFrom+`
  and `+fmt.Sprintf(canonicalRef, "e.road_ref")+` = any($3)
group by 1`, fams.Roads, now, refs)
				var (
					ref    string
					stored []storedMessage
				)
				_, err := pgx.ForEachRow(res, []any{&ref, &stored}, func() error {
					messages[ref], stored = stored, nil
					return nil
				})
				return err
			},
			func() error {
				if len(fams.Weather) == 0 {
					return nil
				}
				// A warning is on a road when its own area holds one of the
				// road's junction points, as SituationsOnRoad places it.
				res, _ := run(ctx, tx, roadLampWarnings, fams.Weather, now, refs)
				var (
					ref string
					w   roads.Warning
				)
				_, err := pgx.ForEachRow(res, []any{&ref, &w.Family, &w.Level}, func() error {
					warnings[ref] = append(warnings[ref], w)
					return nil
				})
				return err
			},
			func() (err error) {
				res, _ := tx.Query(ctx, sourcesSelect, append(slices.Clone(fams.Roads), fams.Weather...))
				sources, err = sourcesOf(res, confirmations)
				return err
			},
			func() error {
				res, _ := run(ctx, tx, roadStates, refs)
				var (
					ref string
					in  []string
				)
				_, err := pgx.ForEachRow(res, []any{&ref, &in}, func() error {
					states[ref], in = in, nil
					return nil
				})
				return err
			},
			func() (err error) {
				var all []string
				for _, in := range states {
					all = append(all, in...)
				}
				slices.Sort(all)
				res, _ := run(ctx, tx, coverageSelect, slices.Compact(all), fams.Roads)
				coverage, err = coverageOf(res)
				return err
			},
		} {
			if err := step(); err != nil {
				return nil, err
			}
		}
		out := make(map[string]roads.Page, len(refs))
		for _, ref := range refs {
			p := roads.Page{Messages: messagesOf(messages[ref]), Sources: sources, Confirmations: confirmations,
				States: append([]string{}, states[ref]...), Coverage: map[string]ampel.Coverage{}}
			if len(fams.Weather) > 0 {
				p.Warnings = append([]roads.Warning{}, warnings[ref]...)
			}
			for _, st := range p.States {
				if c, ok := coverage[st]; ok {
					p.Coverage[st] = c
				}
			}
			out[ref] = p
		}
		return out, nil
	})
}
