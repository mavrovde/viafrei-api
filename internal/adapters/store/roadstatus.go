package store

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// RoadStatus reads q's road as the platform's road status by road number
// does: the road operator's sweep and, when it swept the road in the last
// half hour, its closures and works; the plan, message and works sources'
// entries in the window; when each listed source was last checked; and, on
// a motorway, the switched strategies of the current strategy sources.
func (s *Store) RoadStatus(ctx context.Context, q roads.RoadStatusQuery) (roads.RoadStatusRead, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (roads.RoadStatusRead, error) {
		var out roads.RoadStatusRead
		road := []string{q.Road}
		var errs []error
		if slices.Contains(q.Open, "autobahn") {
			rows, _ := run(ctx, tx, roadStatusTicks, road, q.Now)
			ticks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (time.Time, error) {
				var name string
				var t time.Time
				return t, r.Scan(&name, &t)
			})
			errs = append(errs, err)
			if len(ticks) > 0 {
				out.Tick = ticks[0]
				rows, _ = run(ctx, tx, roadStatusAutobahn, road, ticks, []string{"closure", "roadworks"}, q.Open, q.Limit, nil, nil, nil, q.WindowEnd, q.Now, true)
				out.Live, err = pgx.CollectRows(rows, scanRoadStatusLive)
				errs = append(errs, err)
			}
		}
		live := []string{}
		if !out.Tick.IsZero() {
			live = road
		}
		rows, _ := run(ctx, tx, roadStatusPlanned, road, q.Now, q.WindowEnd, q.Open, q.Limit, nil, nil, nil, live,
			roads.RoadStatusClosesTextFamilies, roads.RoadStatusMessageFamilies, roads.RoadStatusPlanFamilies, roads.RoadStatusProgrammeFamilies)
		var err error
		out.Planned, err = pgx.CollectRows(rows, scanRoadStatusPlanned)
		errs = append(errs, err, readChecked(ctx, tx, &out), readStrategies(ctx, tx, q, &out))
		return out, errors.Join(errs...)
	})
}

func scanRoadStatusLive(r pgx.CollectableRow) (roads.RoadStatusLiveRow, error) {
	var v roads.RoadStatusLiveRow
	var rank int64
	return v, r.Scan(&v.Family, &v.Road, &v.Kind, &v.ID, &v.Title, &v.Subtitle, &v.Extent, &v.Lat, &v.Lon, &v.Blocked, &v.Start, &v.Description,
		&v.ObservedAt, &v.DistanceM, &v.Segments, &v.Total, &v.Closed, &v.Near, &v.NearClose, &v.Families, &rank)
}

func scanRoadStatusPlanned(r pgx.CollectableRow) (roads.RoadStatusPlannedRow, error) {
	var v roads.RoadStatusPlannedRow
	return v, r.Scan(&v.Family, &v.PublicationID, &v.Road, &v.EventType, &v.Publisher, &v.From, &v.To, &v.DistanceFromM, &v.DistanceToM,
		&v.Start, &v.End, &v.LastSeenAt, &v.DistanceM, &v.ClosesText, &v.TypeCloses, &v.ClosureFrom, &v.ClosureTo, &v.WindowStarts, &v.WindowEnds,
		&v.WindowCount, &v.Windowed, &v.WindowNow, &v.PublishedAt, &v.SiteName, &v.SectionName, &v.Cause, &v.Total, &v.Planned, &v.Current,
		&v.Closed, &v.Programme, &v.Near, &v.NC, &v.Families, &v.CurrentFamilies, &v.AlsoFamilies, &v.PlaceText, &v.DetailText, &v.DetourVia,
		&v.DetourVehicles)
}

// readChecked reads the last good run of every source a listed row is of.
func readChecked(ctx context.Context, tx pgx.Tx, out *roads.RoadStatusRead) error {
	fams := []string{}
	for _, r := range out.Planned {
		if !slices.Contains(fams, r.Family) {
			fams = append(fams, r.Family)
		}
	}
	out.Checked = map[string]time.Time{}
	if len(fams) == 0 {
		return nil
	}
	rows, _ := run(ctx, tx, roadStatusChecked, fams)
	_, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct{}, error) {
		var f string
		var at *time.Time
		err := r.Scan(&f, &at)
		if at != nil {
			out.Checked[f] = *at
		}
		return struct{}{}, err
	})
	return err
}

// readStrategies reads the strategy sources' checks and the current ones'
// switched strategies on the road.
func readStrategies(ctx context.Context, tx pgx.Tx, q roads.RoadStatusQuery, out *roads.RoadStatusRead) error {
	if len(q.Strategies) == 0 {
		return nil
	}
	rows, _ := run(ctx, tx, roadStatusChecks, q.Strategies)
	checks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.SourceCheck, error) {
		var v roads.SourceCheck
		return v, r.Scan(&v.Family, &v.CadenceS, &v.CheckedAt, &v.Paused)
	})
	current, stale := roads.CurrentStrategyFamilies(q.Strategies, checks, q.Now)
	out.StrategiesStale = stale
	if err != nil || len(current) == 0 {
		return err
	}
	rows, _ = run(ctx, tx, roadStatusStrategy, current, []string{q.Road}, q.Now, q.Now.Add(roads.StrategyPlanDays*24*time.Hour),
		roads.StrategyPlanMax, roads.StrategyFamilies)
	out.Strategies, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.StrategyRow, error) {
		var v roads.StrategyRow
		var onRoad, windows *int
		var lat, lon *float64
		var facts any
		return v, r.Scan(&v.Family, &v.SourceID, &v.PublicationID, &v.Road, &v.EventType, &v.From, &v.To, &v.Name, &v.Section, &v.Via, &v.Words,
			&v.CauseValues, &v.CommentValues, &v.WindowFrom, &v.WindowTo, &v.NowInForce, &onRoad, &lat, &lon, &facts, &windows)
	})
	return err
}
