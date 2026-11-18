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
