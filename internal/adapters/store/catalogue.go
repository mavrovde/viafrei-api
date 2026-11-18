package store

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

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
	// The roads in the asked Land: its Länder hold it, or a junction of it
	// is in it, from the snapshot.
	var inState []string
	for ref, sh := range shapes {
		if q.State != "" && (slices.Contains(sh.states, q.State) || slices.Contains(sh.junctionStates, q.State)) {
			inState = append(inState, ref)
		}
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
		res, _ = tx.Query(ctx, `
select r.road_ref, r.name,
       (select count(*) from contract.junction j where j.road_ref = r.road_ref)::int,
       (select count(*) from contract.traffic_event e
         where e.road_ref = r.road_ref and e.source_ref = any($1) and `+eventPermitted+`
           and e.withdrawn_at is null and e.valid_from <= $5 and (e.valid_to is null or e.valid_to > $5))::int,
       r.source_ref,
       array(select distinct e.source_ref from contract.traffic_event e
          where e.road_ref = r.road_ref and e.source_ref = any($1) and `+eventPermitted+`
            and e.withdrawn_at is null and e.valid_from <= $5 and (e.valid_to is null or e.valid_to > $5)
          order by 1),
       array(select distinct j.source_ref from contract.junction j where j.road_ref = r.road_ref order by 1)
from contract.road r
where `+permitted("r")+`
  and ($2::text = '' or r.road_ref = any($6::text[]))
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
		res, _ = tx.Query(ctx, `
select j.junction_id, j.name, j.junction_kind, j.number, j.state_code, st_y(j.location), st_x(j.location)
from contract.junction j
where j.road_ref = $1 and `+permitted("j")+`
order by nullif(regexp_replace(coalesce(j.number, ''), '[^0-9].*$', ''), '')::int nulls last, j.number, j.junction_id`, q.Road)
		ms[0].Exits, err = pgx.CollectRows(res, func(r pgx.CollectableRow) (roads.Junction, error) {
			var j roads.Junction
			err := r.Scan(&j.ID, &j.Name, &j.Kind, &j.Number, &j.State, &j.Lat, &j.Lon)
			return j, err
		})
		return ms, err
	})
	return ms, counted, err
}
