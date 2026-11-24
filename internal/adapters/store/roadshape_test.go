package store

import (
	"context"
	"testing"
	"time"
)

// TestRoadSnapshotStore answers road shapes from the snapshot when it is on,
// without storage, and KeepRoads returns at once when it is off.
func TestRoadSnapshotStore(t *testing.T) {
	off := &Store{nowFn: time.Now}
	done := make(chan struct{})
	go func() { off.KeepRoads(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("KeepRoads without a snapshot did not return")
	}

	s := (&Store{nowFn: time.Now}).WithRoadSnapshot(time.Minute, time.Second)
	if ttl, staleFor := s.RoadSnapshot(); ttl != time.Minute || staleFor != time.Minute {
		t.Fatalf("ttl %s staleFor %s", ttl, staleFor)
	}
	s.roads.items, s.roads.at = []roadShape{{ref: "A1", states: []string{"DE-HH"}}}, time.Now()
	got, err := s.roadShapes(context.Background())
	if err != nil || len(got) != 1 || got["A1"].states[0] != "DE-HH" {
		t.Fatalf("road shapes with the snapshot on: %v %v", got, err)
	}
}

// TestJunctionLands answers the junction Länder while they are younger
// than staleFor, and none before the first read or past it.
func TestJunctionLands(t *testing.T) {
	var j junctionLands
	at := time.Unix(0, 0)
	if j.current(at, time.Second) != nil {
		t.Fatal("Länder before any read")
	}
	j.set(map[string]roadLands{"A1": {states: []string{"01"}}}, at)
	if got := j.current(at.Add(time.Second-1), time.Second); got["A1"].states[0] != "01" {
		t.Fatalf("within staleFor: %v", got)
	}
	if j.current(at.Add(time.Second), time.Second) != nil {
		t.Fatal("Länder past staleFor")
	}
}

// TestRoadShapesMissingRoad: a road the snapshot does not hold yet (added
// since its read) has no length, no Länder and no line until the next read.
func TestRoadShapesMissingRoad(t *testing.T) {
	s := (&Store{nowFn: time.Now}).WithRoadSnapshot(time.Minute, time.Minute)
	s.roads.items, s.roads.at = []roadShape{{ref: "A1"}}, time.Now()
	got, err := s.roadShapes(context.Background())
	if sh, ok := got["A2"]; err != nil || ok || sh.lengthKm != nil || sh.states != nil || sh.line != nil {
		t.Fatalf("a missing road: %+v %v %v", sh, ok, err)
	}
}

// TestKeepRoadsEndsWithItsContext: with the snapshot on, KeepRoads reads
// once and returns when its context has ended.
func TestKeepRoadsEndsWithItsContext(t *testing.T) {
	s := (&Store{nowFn: time.Now}).WithRoadSnapshot(time.Minute, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { s.KeepRoads(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("KeepRoads did not return after its context ended")
	}
}
