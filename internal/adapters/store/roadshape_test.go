package store

import (
	"context"
	"errors"
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

// TestJunctionLands reads a road's junction Länder on first use, answers
// them within ttl without a read, reads again past ttl, keeps the last read
// answering while a read fails until staleFor, and then fails.
func TestJunctionLands(t *testing.T) {
	clock := time.Unix(0, 0)
	now := func() time.Time { return clock }
	land := "01"
	var reads int
	down := false
	read := func(context.Context) ([]junctionLand, error) {
		reads++
		if down {
			return nil, errors.New("storage down")
		}
		return []junctionLand{{id: "j", state: &land}}, nil
	}
	var j junctionLands
	ctx := context.Background()
	ask := func() (map[string]*string, error) {
		return j.get(ctx, "A1", time.Second, 3*time.Second, now, read)
	}
	for range 2 {
		if got, err := ask(); err != nil || *got["j"] != "01" {
			t.Fatalf("%v %v", got, err)
		}
	}
	if reads != 1 {
		t.Fatalf("%d reads within ttl, want 1", reads)
	}
	clock = clock.Add(time.Second)
	if _, err := ask(); err != nil || reads != 2 {
		t.Fatalf("past ttl: %d reads, %v", reads, err)
	}
	down = true
	clock = clock.Add(2 * time.Second)
	if got, err := ask(); err != nil || *got["j"] != "01" {
		t.Fatalf("a failed read within staleFor: %v %v", got, err)
	}
	clock = clock.Add(time.Second)
	if _, err := ask(); err == nil {
		t.Fatal("a read past staleFor answered while storage is down")
	}
}
