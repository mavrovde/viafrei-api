package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// feeding is one family that holds rows in one dataset.
type feeding struct {
	dataset provenance.Dataset
	family  string
}

// feedingSnapshot is the last read of which families hold rows in which
// dataset, and when it was read.
type feedingSnapshot = snapshot[feeding]

// datasetViews are the contract views of each dataset: a family holds rows of the
// dataset when it holds a row in one of them. The read contract names no
// family's views (the source register states a domain, wider than what one
// answer reads), so they are read from the rows themselves, off the request
// path.
var datasetViews = map[provenance.Dataset][]string{
	provenance.DatasetFuel:         {"fuel_station"},
	provenance.DatasetZones:        {"low_emission_zone"},
	provenance.DatasetLorry:        {"lorry_rule"},
	provenance.DatasetSpeedCameras: {"speed_camera"},
	provenance.DatasetWarnings:     {"weather_warning"},
	provenance.DatasetAlerts:       {"service_alert"},
	provenance.DatasetSharing:      {"sharing_station", "sharing_vehicle"},
	provenance.DatasetRoadEvents:   {"traffic_event"},
}

// feedingRead reads, for every dataset, the families holding a row in one of
// its views, one distinct scan a view.
var feedingRead = func() string {
	sets := make([]string, 0, len(datasetViews))
	for d := range datasetViews {
		sets = append(sets, string(d))
	}
	slices.Sort(sets)
	parts := make([]string, 0, len(sets))
	for _, d := range sets {
		for _, v := range datasetViews[provenance.Dataset(d)] {
			parts = append(parts, fmt.Sprintf("select distinct '%s'::text, source_ref from contract.%s", d, v))
		}
	}
	return strings.Join(parts, "\nunion\n") + "\norder by 1, 2"
}()

// WithFeedingSnapshot answers Feeding from a read no older than staleFor,
// which KeepFeeding renews every ttl; ttl 0 reads storage every time
// (db.feeding_snapshot_ttl and db.feeding_stale_for).
func (s *Store) WithFeedingSnapshot(ttl, staleFor time.Duration) *Store {
	s.holders.ttl, s.holders.staleFor = ttl, max(ttl, staleFor)
	return s
}

// FeedingSnapshot reports how often the families of each dataset are read
// again and how long a read answers at most.
func (s *Store) FeedingSnapshot() (ttl, staleFor time.Duration) {
	return s.holders.ttl, s.holders.staleFor
}

// KeepFeeding reads the families of each dataset at once and again every
// ttl until ctx ends, so no request scans a view for them. A failed read is
// logged at warn and tried again at the next tick. A no-op without
// WithFeedingSnapshot.
func (s *Store) KeepFeeding(ctx context.Context) {
	if s.holders.ttl <= 0 {
		return
	}
	s.holders.keep(ctx, func(ctx context.Context) error { return s.holders.refresh(ctx, s.nowFn, s.readFeeding) }, s.log,
		"store: the background read of the families of each dataset failed; the snapshot answers until it is db.feeding_stale_for old")
}

// Feeding answers which of families hold rows in dataset, in the order
// given: the families an answer of that dataset consulted, which an empty
// answer credits, as the platform's tools credit their own sources and not
// every source of the domain.
func (s *Store) Feeding(ctx context.Context, dataset provenance.Dataset, families []string) ([]string, error) {
	if _, ok := datasetViews[dataset]; !ok {
		return nil, fmt.Errorf("store: no views for the dataset %q", dataset)
	}
	var (
		items []feeding
		err   error
	)
	if s.holders.ttl <= 0 {
		items, err = s.readFeeding(ctx)
	} else {
		items, err = s.holders.get(ctx, s.nowFn, s.readFeeding)
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, f := range families {
		if slices.Contains(items, feeding{dataset: dataset, family: f}) && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out, nil
}

// readFeeding reads which families hold rows in each dataset, whatever a key
// may read: each answer intersects them with the families it was permitted.
func (s *Store) readFeeding(ctx context.Context) ([]feeding, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]feeding, error) {
		// A failed query surfaces through CollectRows.
		rows, _ := tx.Query(ctx, feedingRead)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (feeding, error) {
			var v feeding
			err := r.Scan(&v.dataset, &v.family)
			return v, err
		})
	})
}
