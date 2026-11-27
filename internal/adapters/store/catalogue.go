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
	list := motorwaysLiveLand
	if lands != nil {
		list = motorways
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
		// The situations in force are read once, grouped by road, not twice
		// per road, and the families counted come from the same read
		// (motorwaysOf).
		res, _ := run(ctx, tx, list, families, q.State, q.Road, q.Limit, now, inState)
		ms, c, err := collectMotorways(res, shapes)
		counted = c
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

// collectMotorways reads the rows of motorwaysOf: the roads, each with its
// length and Länder from shapes, and the families counted, which every row
// carries and a row without a road (no road kept) carries alone.
func collectMotorways(res pgx.Rows, shapes map[string]roadShape) ([]roads.Motorway, []string, error) {
	var counted []string
	ms := []roads.Motorway{}
	for res.Next() {
		var (
			road, name, family    *string
			junctions, situations *int
			m                     roads.Motorway
		)
		if err := res.Scan(&road, &name, &junctions, &situations, &family, &m.Sources, &m.Registers, &counted); err != nil {
			res.Close()
			return nil, nil, err
		}
		if road == nil {
			continue
		}
		m.Road, m.Name, m.Junctions, m.Situations, m.Family = *road, *name, *junctions, *situations, *family
		sh := shapes[m.Road]
		// Never nil, as the view's coalesce(states, '{}') answered.
		m.LengthKm, m.States = sh.lengthKm, append([]string{}, sh.states...)
		ms = append(ms, m)
	}
	if err := res.Err(); err != nil {
		return nil, nil, err
	}
	return ms, counted, nil
}

// motorwaysOf reads the roads of the register, by length of reference then
// reference, at most $4: each with its number of junctions, its situations
// in force at $5 of the families $1 (counted and their families, read once
// and grouped by road), its family and the registers of its junctions. $2 a
// Land (” every road): the roads $6 in it, and with liveLand those with a
// junction in it; $3 one road (” every road). Each row also carries the
// families of the situations in force on any road of the register, whatever
// the query keeps, from the same read of the situations: one row with no
// road carries them when no road is kept.
//
//nolint:misspell // materialized is the SQL keyword
func motorwaysOf(name string, liveLand bool) stmt {
	inLand := ``
	if liveLand {
		inLand = `
       or exists (select 1 from tmc_locations t where t.road_number = r.road and ` + motorwayJunctions + `
                  and contract_junction_land(t.geom) = $2::text)`
	}
	return opaqueNamed(name, `
with es as materialized (
  select e.road_ref, count(*)::int as n, array_agg(distinct e.source_ref order by e.source_ref) as sources
  from `+trafficEventsOf("")+` e
  where e.source_ref = any($1) and `+eventPermitted+`
    and e.withdrawn_at is null and e.valid_from <= $5 and (e.valid_to is null or e.valid_to > $5)
  group by e.road_ref),
counted as (
  select coalesce(array_agg(distinct f.f order by f.f), '{}') families
  from es join autobahn_roads r on r.road = es.road_ref cross join unnest(es.sources) f(f)),
m as (
  select r.road, jc.n junctions, coalesce(es.n, 0) situations, coalesce(es.sources, '{}') sources,
         case when jc.n > 0 then array['bast_lcl'] else '{}'::text[] end registers
  from autobahn_roads r
  cross join lateral (select count(*)::int n from tmc_locations t where t.road_number = r.road and `+motorwayJunctions+`) jc
  left join es on es.road_ref = r.road
  where ($2::text = '' or r.road = any($6::text[])`+inLand+`)
    and ($3::text = '' or r.road = $3::text)
  order by length(r.road), r.road
  limit $4)
select m.road, m.road, m.junctions, m.situations, case when m.road is not null then 'autobahn' end, m.sources, m.registers, c.families
from counted c
left join m on true
order by length(m.road), m.road`, "text[]", "text", "text", "int8", "timestamptz", "text[]")
}

var (
	// motorways are the roads while the junction Länder are current: the
	// roads in a Land are those of $6.
	motorways = motorwaysOf("api_motorways", false)
	// motorwaysLiveLand are the roads when they are not: a road with a
	// junction in the Land is in it too.
	motorwaysLiveLand = motorwaysOf("api_motorways_live_land", true)
)

// roadLampMessages is the lamp's view of the situations on each road of $3
// (canonical references), of the families $1 at $2 (messagesSelect).
var roadLampMessages = opaqueNamed("api_road_lamp_messages", `select `+fmt.Sprintf(canonicalRef, "e.road_ref")+`, `+messagesAgg+`
`+messagesFrom("")+`
  and `+fmt.Sprintf(canonicalRef, "e.road_ref")+` = any($3)
group by 1`, "text[]", "timestamptz", "text[]")

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

// landsOf is, for each road of refs with a junction in a Land, the Länder
// of its junctions, sorted, as roadStates reads them, from the junction
// Länder lands; false when lands is not current (nil) or lacks a road.
func landsOf(lands map[string]roadLands, refs []string) (map[string][]string, bool) {
	if lands == nil {
		return nil, false
	}
	out := make(map[string][]string, len(refs))
	for _, ref := range refs {
		l, ok := lands[ref]
		if !ok {
			return nil, false
		}
		if len(l.states) > 0 {
			out[ref] = slices.Sorted(slices.Values(l.states))
		}
	}
	return out, true
}

// RoadLamps reads, in one snapshot, what the lamp of each road (a stored
// road reference) states over the whole road in both directions, as
// SituationsOnRoad reads it for one road: the lamp's view of the
// situations on it, the weather warnings in force on its junctions, the
// freshness of every family read, the states of its junctions and their
// message coverage. A page carries no list of situations and no next
// change. The catalogue reads each road's lamp in a handful of statements,
// not a full conditions read per road.
func (s *Store) RoadLamps(ctx context.Context, refs []string, fams roads.Families, now time.Time) (map[string]roads.Page, error) {
	// The Länder of the roads' junctions, from the junction Länder while
	// they are current and hold every road; else read live, in the snapshot.
	warmStates, warm := landsOf(s.lands.current(s.nowFn(), s.roads.staleFor), refs)
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
				res, _ := run(ctx, tx, roadLampMessages, fams.Roads, now, refs)
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
				res, _ := run(ctx, tx, sourcesSelect, append(slices.Clone(fams.Roads), fams.Weather...))
				sources, err = sourcesOf(res, confirmations)
				return err
			},
			func() error {
				if warm {
					states = warmStates
					return nil
				}
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
