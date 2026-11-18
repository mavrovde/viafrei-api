package store

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// roadShape is what the read contract derives for one road from the BASt
// location code list: its length, the Länder of its named junctions in road
// order, and its line as hex EWKB (nil when the list holds no chain of the
// road). The read contract computes them per road and per read (a chain walk
// over the list, milliseconds a road), so a read that selects them for every
// road, or once per row it filters, pays that walk each time; they change
// only when the list or the road register is reloaded.
type roadShape struct {
	ref      string
	lengthKm *float64
	states   []string
	line     *string
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
select r.road_ref, r.length_km::float8, coalesce(r.states, '{}'), r.geometry::text
from contract.road r
order by r.road_ref`)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (roadShape, error) {
			var v roadShape
			err := r.Scan(&v.ref, &v.lengthKm, &v.states, &v.line)
			return v, err
		})
	})
}

// junctionLand is a junction's Land (state_code), nil when it has none.
type junctionLand struct {
	id    string
	state *string
}

// junctionLands keeps the junction Länder of each road asked: the junction
// view derives a junction's Land per read from the nearest municipality
// (milliseconds a junction), so one road's exits paid that for every
// junction on every request. A road's Länder are read by the first request
// that asks the road and read again by the first past db.road_snapshot_ttl;
// a failed read leaves the last answering until db.road_stale_for.
type junctionLands struct {
	mu    sync.Mutex
	roads map[string]*snapshot[junctionLand]
}

// get answers road's junction Länder by junction id.
func (j *junctionLands) get(ctx context.Context, road string, ttl, staleFor time.Duration, now func() time.Time,
	read func(context.Context) ([]junctionLand, error),
) (map[string]*string, error) {
	j.mu.Lock()
	if j.roads == nil {
		j.roads = map[string]*snapshot[junctionLand]{}
	}
	sn, ok := j.roads[road]
	if !ok {
		sn = &snapshot[junctionLand]{ttl: ttl, staleFor: staleFor}
		j.roads[road] = sn
	}
	j.mu.Unlock()
	sn.mu.Lock()
	old := sn.items != nil && now().Sub(sn.at) >= sn.ttl
	sn.mu.Unlock()
	if old {
		// No background loop renews a road's junctions: the request past
		// ttl does; its failure leaves the last read answering.
		_ = sn.refresh(ctx, now, read)
	}
	items, err := sn.get(ctx, now, read)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*string, len(items))
	for _, l := range items {
		out[l.id] = l.state
	}
	return out, nil
}

// junctionStates answers the Land of every junction of road by its id, kept
// per road while the road snapshot is on, else read.
func (s *Store) junctionStates(ctx context.Context, road string) (map[string]*string, error) {
	read := func(ctx context.Context) ([]junctionLand, error) { return s.readJunctionLands(ctx, road) }
	ttl := s.roads.ttl
	if ttl <= 0 {
		// A snapshot no read outlives: every request reads.
		return (&junctionLands{}).get(ctx, road, 0, 0, s.nowFn, read)
	}
	return s.lands.get(ctx, road, ttl, s.roads.staleFor, s.nowFn, read)
}

// readJunctionLands reads the Land of every junction of road, whatever a key
// may read: the exits read applies its own row rule.
func (s *Store) readJunctionLands(ctx context.Context, road string) ([]junctionLand, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]junctionLand, error) {
		rows, _ := tx.Query(ctx, `
select j.junction_id, j.state_code from contract.junction j where j.road_ref = $1 order by j.junction_id`, road)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (junctionLand, error) {
			var v junctionLand
			err := r.Scan(&v.id, &v.state)
			return v, err
		})
	})
}
