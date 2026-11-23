package store

import (
	"context"
	"fmt"
	"maps"
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

// datasetRows are, for each dataset, the families of its rows as the read
// contract's views of the dataset held them (the views it replaces named in
// each): a family holds rows of the dataset when one of its rows is one the
// dataset's reads may answer, whatever key reads. The read contract names no
// family's datasets (the source register states a domain, wider than what one
// answer reads), so they are read from the rows themselves, off the request
// path. Each row is (dataset, family, the text the family's source rule is
// matched with, or null where the view applied no rule).
var datasetRows = map[provenance.Dataset]string{
	// fuel_station: the stations not deleted with a point; the rule matches the
	// family or ''.
	provenance.DatasetFuel: `select coalesce(f.source_feed, f.source), coalesce(f.source_feed, f.source, '')
  from fuel_stations f where f.deleted_at is null and f.geom is not null`,
	// low_emission_zone: the live zones (the platform's, not closed).
	provenance.DatasetZones: `select z.source_feed, z.source_feed from lez_zones_live z`,
	// lorry_rule: the open rows of the five lorry tables.
	provenance.DatasetLorry: `select r.source_feed, r.source_feed from ` + lorryRows,
	// speed_camera: the open cameras of the official lists and OpenStreetMap.
	provenance.DatasetSpeedCameras: `select c.source_ref, c.source_ref from ` + speedOfficialRows + ` c
  union all
  select c.source_ref, c.source_ref from ` + speedOSMRows + ` c`,
	// weather_warning: the warnings not deleted that state a start; no rule.
	provenance.DatasetWarnings: `select w.source_feed, null::text from weather_warnings w where w.deleted_at is null and w.valid_from is not null`,
	// service_alert: the alerts not closed.
	provenance.DatasetAlerts: `select a.source_feed, a.source_feed from delfi_service_alerts a where a.closed_at is null`,
	// sharing_station and sharing_vehicle: the stations not closed and the
	// vehicles, with a point, of the admitted systems.
	provenance.DatasetSharing: `select s.source_feed, s.source_feed from gbfs_stations s join gbfs_systems y using (system_id)
  where s.closed_at is null and y.admitted and s.geom is not null
  union all
  select v.source_feed, v.source_feed from gbfs_vehicles v join gbfs_systems y using (system_id)
  where y.admitted and v.geom is not null`,
	// traffic_event: the Autobahn messages of each road and kind's newest
	// sweep of the last day (trafficEvents) and the traffic messages not
	// deleted. A family is read, not a situation's Land or text.
	provenance.DatasetRoadEvents: `select e.source, e.source from autobahn_events e
  join autobahn_sweeps s on s.road = e.road and s.kind = e.kind and s.observed_at = e.observed_at
  where s.observed_at > now() - interval '1 day' and s.kind = any(array['warning', 'roadworks', 'closure', 'closure_entry_exit'])
  union all
  select e.source_feed, e.source_feed from road_events e where e.deleted_at is null`,
}

// feedingRead reads, for every dataset, its families (datasetRows) whose
// source rule, the first by priority, does not exclude them: the rule decided
// once a family, after the rows are reduced to their distinct families
// (materialised, so the planner does not test the rule on every row).
var feedingRead = func() stmt {
	sets := slices.Sorted(maps.Keys(datasetRows))
	parts := make([]string, 0, len(sets))
	for _, d := range sets {
		parts = append(parts, `select '`+string(d)+`'::text d, r.family, r.ruled from (
`+datasetRows[d]+`) r(family, ruled)`)
	}
	//nolint:misspell // materialized is the SQL keyword
	return named("api_feeding", `with r as materialized (select distinct r.d, r.family, r.ruled from (
`+strings.Join(parts, "\nunion all\n")+`) r)
select distinct r.d, r.family from r
where not coalesce((select k.excluded from contract_source_rules k where r.ruled ~ k.pattern order by k.priority limit 1), false)
order by 1, 2`)
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
	if _, ok := datasetRows[dataset]; !ok {
		return nil, fmt.Errorf("store: no rows for the dataset %q", dataset)
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
		rows, _ := run(ctx, tx, feedingRead)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (feeding, error) {
			var v feeding
			err := r.Scan(&v.dataset, &v.family)
			return v, err
		})
	})
}
