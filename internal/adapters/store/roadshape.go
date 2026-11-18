package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// roadShape is what the read contract derives for one road from the BASt
// location code list: its length, the Länder of its named junctions in road
// order, and its line as hex EWKB (nil when the list holds no chain of the
// road), and the Länder its junctions are in (the junction view derives a
// junction's Land per read too, from the nearest municipality). The read contract computes them per road and per read (a chain walk
// over the list, milliseconds a road), so a read that selects them for every
// road, or once per row it filters, pays that walk each time; they change
// only when the list or the road register is reloaded.
type roadShape struct {
	ref      string
	lengthKm *float64
	states   []string
	line     *string
	// junctionStates are the distinct Länder of the road's junctions.
	junctionStates []string
}

// roadSnapshot is the last read of every road's shape and when it was read.
type roadSnapshot = snapshot[roadShape]

// WithRoadSnapshot answers road shapes from a read no older than staleFor,
// which KeepRoads renews every ttl; ttl 0 reads storage every time
// (db.road_snapshot_ttl and db.road_stale_for).
func (s *Store) WithRoadSnapshot(ttl, staleFor time.Duration) *Store {
	s.roads.ttl, s.roads.staleFor = ttl, max(ttl, staleFor)
	return s
}

// RoadSnapshot reports how often the road shapes are read again and how long
// a read answers at most: the bound on a reloaded location code list or road
// register reaching answers.
func (s *Store) RoadSnapshot() (ttl, staleFor time.Duration) {
	return s.roads.ttl, s.roads.staleFor
}

// KeepRoads reads the road shapes at once and again every ttl until ctx
// ends, so no request walks the location code list itself. A failed read is
// logged at warn and tried again at the next tick. A no-op without
// WithRoadSnapshot.
func (s *Store) KeepRoads(ctx context.Context) {
	if s.roads.ttl <= 0 {
		return
	}
	s.roads.keep(ctx, s.nowFn, s.readRoadShapes, s.log, "store: the background road shape read failed; the snapshot answers until it is db.road_stale_for old")
}

// roadShapes answers every road's shape by its reference, from the snapshot
// when it is on.
func (s *Store) roadShapes(ctx context.Context) (map[string]roadShape, error) {
	var (
		items []roadShape
		err   error
	)
	if s.roads.ttl <= 0 {
		items, err = s.readRoadShapes(ctx)
	} else {
		items, err = s.roads.get(ctx, s.nowFn, s.readRoadShapes)
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]roadShape, len(items))
	for _, r := range items {
		out[r.ref] = r
	}
	return out, nil
}

// readRoadShapes reads every road's shape, whatever a key may read: each
// read that uses a shape applies its own row rule to the road.
func (s *Store) readRoadShapes(ctx context.Context) ([]roadShape, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roadShape, error) {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := tx.Query(ctx, `
select r.road_ref, r.length_km::float8, coalesce(r.states, '{}'), r.geometry::text,
       array(select distinct j.state_code from contract.junction j where j.road_ref = r.road_ref and j.state_code is not null)
from contract.road r
order by r.road_ref`)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (roadShape, error) {
			var v roadShape
			err := r.Scan(&v.ref, &v.lengthKm, &v.states, &v.line, &v.junctionStates)
			return v, err
		})
	})
}
