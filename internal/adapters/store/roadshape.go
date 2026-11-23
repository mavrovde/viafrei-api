package store

import (
	"context"
	"slices"
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

// KeepRoads reads the road shapes and every road's junction Länder at once
// and again every ttl until ctx ends, so no request walks the location code
// list or derives a junction's Land itself. A failed read is
// logged at warn and tried again at the next tick. A no-op without
// WithRoadSnapshot.
func (s *Store) KeepRoads(ctx context.Context) {
	if s.roads.ttl <= 0 {
		return
	}
	s.roads.keep(ctx, s.readRoadsAndLands, s.log, "store: the background road shape read failed; the snapshot answers until it is db.road_stale_for old")
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

// roadShapes reads every motorway of the Autobahn register with the shape
// the platform draws from the LCL's points of it (contract_road_shape): its
// line, its length and its Länder.
var roadShapes = named("api_road_shapes", `
select r.road, l.length_km::numeric(6,1)::float8, coalesce(l.states, '{}'), l.line::geometry(MultiLineString,4326)::text
from autobahn_roads r
left join lateral contract_road_shape(r.road) l(line, length_km, states) on true
order by r.road`)

// roadJunctionLands reads the LCL's German motorway points with a name of
// the road $1, each with its Land, the platform's (contract_junction_land:
// the Land of the nearest municipality), by id.
var roadJunctionLands = named("api_road_junction_lands", `
select t.cid || ':' || t.tabcd || ':' || t.lcd, contract_junction_land(t.geom)
from tmc_locations t
where t.cid = 58 and t.tabcd = 1 and t.tcd = 1 and t.road_number ~ '^A[1-9][0-9]{0,2}$'
  and t.geom is not null and t.name is not null and t.road_number = (select $1::text)
order by 1`, "text")

// readRoadShapes reads every road's shape, whatever a key may read: each
// read that uses a shape applies its own row rule to the road.
func (s *Store) readRoadShapes(ctx context.Context) ([]roadShape, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]roadShape, error) {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := run(ctx, tx, roadShapes)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (roadShape, error) {
			var v roadShape
			err := r.Scan(&v.ref, &v.lengthKm, &v.states, &v.line)
			return v, err
		})
	})
}

// roadLands are the Länder of one road's junctions: each junction's Land by
// its id (nil when it has none), and the set of them.
type roadLands struct {
	byJunction map[string]*string
	states     []string
}

// junctionLands is the last background read of every road's junction Länder
// and when it was read. The junction view derives a junction's Land per read
// from the nearest municipality (milliseconds a junction), so one road's
// exits and the Land filter of the list (a junction test per road) paid it
// on every request. It is read road by road, one bounded read a road, off
// the request path only (KeepRoads, WarmRoads); while it is empty or older
// than db.road_stale_for a request reads the Länder live, inside its own
// read. It holds one entry per road of the road register.
type junctionLands struct {
	mu    sync.Mutex
	roads map[string]roadLands
	at    time.Time
}

// current answers the read while it is younger than staleFor, else nil.
func (j *junctionLands) current(now time.Time, staleFor time.Duration) map[string]roadLands {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.roads == nil || now.Sub(j.at) >= staleFor {
		return nil
	}
	return j.roads
}

// set replaces the read.
func (j *junctionLands) set(roads map[string]roadLands, at time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.roads, j.at = roads, at
}

// WarmRoads reads the road shapes and then every road's junction Länder
// once, off the request path: KeepRoads' read, every ttl.
func (s *Store) WarmRoads(ctx context.Context) error {
	return s.readRoadsAndLands(ctx)
}

// readRoadsAndLands reads the road shapes, then the junction Länder of every
// road, one read a road; a failed read of either is the read's failure, and
// the last read answers until db.road_stale_for.
//
// Only the shape read is the snapshot's flight, which a request without a
// current snapshot waits for: one read, the one it would make itself. The
// junction read, road by road, comes after it, outside the flight, so a
// request on a cold pod is answered from the shapes and reads the Länder live
// inside its own read while the junction read fills, never waiting for it.
func (s *Store) readRoadsAndLands(ctx context.Context) error {
	// The shapes read now, or a request's read in flight.
	shapes, err := s.roads.renew(ctx, s.nowFn, s.readRoadShapes)
	if err != nil {
		return err
	}
	at := s.nowFn()
	out := make(map[string]roadLands, len(shapes))
	for _, r := range shapes {
		if s.landsRead != nil {
			s.landsRead()
		}
		l, err := s.readRoadLands(ctx, r.ref)
		if err != nil {
			return err
		}
		out[r.ref] = l
	}
	s.lands.set(out, at)
	return nil
}

// readRoadLands reads the Land of every junction of road, whatever a key
// may read: the reads that use them apply their own row rule.
func (s *Store) readRoadLands(ctx context.Context, road string) (roadLands, error) {
	type land struct {
		id    string
		state *string
	}
	ls, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]land, error) {
		rows, _ := run(ctx, tx, roadJunctionLands, road)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (land, error) {
			var v land
			err := r.Scan(&v.id, &v.state)
			return v, err
		})
	})
	out := roadLands{byJunction: make(map[string]*string, len(ls)), states: []string{}}
	for _, l := range ls {
		out.byJunction[l.id] = l.state
		if l.state != nil && !slices.Contains(out.states, *l.state) {
			out.states = append(out.states, *l.state)
		}
	}
	return out, err
}
