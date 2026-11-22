package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// TestFeedingSnapshot answers the families of a dataset from the snapshot
// when it is on, without storage, in the order asked and once each; a
// dataset without views is an error; KeepFeeding returns at once when the
// snapshot is off; every dataset's views are read by one statement.
func TestFeedingSnapshot(t *testing.T) {
	off := &Store{nowFn: time.Now}
	done := make(chan struct{})
	go func() { off.KeepFeeding(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("KeepFeeding without a snapshot did not return")
	}

	s := (&Store{nowFn: time.Now}).WithFeedingSnapshot(time.Minute, time.Second)
	if ttl, staleFor := s.FeedingSnapshot(); ttl != time.Minute || staleFor != time.Minute {
		t.Fatalf("ttl %s staleFor %s", ttl, staleFor)
	}
	s.holders.items, s.holders.at = []feeding{
		{provenance.DatasetFuel, "tk"}, {provenance.DatasetFuel, "osm"}, {provenance.DatasetSharing, "nb"},
	}, time.Now()
	got, err := s.Feeding(context.Background(), provenance.DatasetFuel, []string{"afir", "osm", "tk", "osm", "nb"})
	if err != nil || !slices.Equal(got, []string{"osm", "tk"}) {
		t.Fatalf("fuel: %v %v", got, err)
	}
	if got, err := s.Feeding(context.Background(), provenance.DatasetZones, []string{"tk"}); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("zones: %v %v", got, err)
	}
	if _, err := s.Feeding(context.Background(), "parking", []string{"tk"}); err == nil {
		t.Fatal("a dataset without views answered")
	}
	for d, views := range datasetViews {
		for _, v := range views {
			if want := "select distinct '" + string(d) + "'::text, source_ref from contract." + v; !strings.Contains(feedingRead, want) {
				t.Errorf("the read lacks %q", want)
			}
		}
	}
}
