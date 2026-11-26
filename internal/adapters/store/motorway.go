package store

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// jamHistory keeps the oldest hour of the jam history an hour, as the
// platform's gate does.
type jamHistory struct {
	mu     sync.Mutex
	at     time.Time
	oldest *time.Time
}

// jamHistoryTTL is how long the oldest hour is kept.
const jamHistoryTTL = time.Hour

// oldest is the jam history's oldest hour, read again once it is older than
// jamHistoryTTL (or now is before the read); a failed read is not kept.
func (h *jamHistory) oldestAt(ctx context.Context, tx pgx.Tx, now time.Time) (*time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.at.IsZero() && now.Sub(h.at) < jamHistoryTTL && !now.Before(h.at) {
		return h.oldest, nil
	}
	var oldest *time.Time
	err := runRow(ctx, tx, motorwayJamHistory).Scan(&oldest)
	h.at, h.oldest = now, oldest
	if err != nil {
		h.at = time.Time{}
	}
	return oldest, err
}

// Motorway reads q's road as the platform's motorway tool does; a road with
// no sweep in the last half hour answers a zero Tick.
func (s *Store) Motorway(ctx context.Context, q roads.MotorwayQuery) (roads.MotorwayRead, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (roads.MotorwayRead, error) {
		var out roads.MotorwayRead
		road := []string{q.Road}
		rows, _ := run(ctx, tx, motorwayLatest, road, q.Now)
		ticks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (time.Time, error) {
			var name string
			var t time.Time
			return t, r.Scan(&name, &t)
		})
		if len(ticks) == 0 {
			return out, err
		}
		out.Tick = ticks[0]
		at := []time.Time{out.Tick}
		rows, _ = run(ctx, tx, motorwayCounts, road, at, q.Now)
		counts, cerr := pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.MotorwayCountRow, error) {
			var v roads.MotorwayCountRow
			var name string
			return v, r.Scan(&name, &v.Kind, &v.N, &v.Jams, &v.Announced, &v.AnnouncedFrom)
		})
		rows, _ = run(ctx, tx, motorwayEvents, road, at, roads.MotorwayKinds, roads.MotorwayLampRows+1, 0, q.Now)
		lamp, eerr := pgx.CollectRows(rows, scanMotorwayEvent)
		out.Counts, out.Lamp = counts, lamp
		out.Events = slices.Clone(lamp[:min(q.Limit, len(lamp))])
		merr := readMeasured(ctx, tx, road, q, &out)
		perr := readPlansNow(ctx, tx, road, q, &out)
		return out, errors.Join(cerr, eerr, merr, perr, s.readJams(ctx, tx, q.Now, &out))
	})
}

func scanMotorwayEvent(r pgx.CollectableRow) (roads.MotorwayEventRow, error) {
	var v roads.MotorwayEventRow
	err := r.Scan(&v.Road, &v.Kind, &v.ID, &v.Title, &v.Subtitle, &v.TrafficType, &v.DelayS, &v.Extent, &v.Lat, &v.Lon,
		&v.StartText, &v.StartsAt, &v.Description, &v.Blocked, &v.AverageKmh, &v.Segments, &v.FiledRoad, &v.SiteIDs, &v.SiteExtents, &v.RouteRecommendations)
	return v, err
}

// readMeasured reads the measured state when the key may read it.
func readMeasured(ctx context.Context, tx pgx.Tx, road []string, q roads.MotorwayQuery, out *roads.MotorwayRead) error {
	if !q.Measured {
		return nil
	}
	rows, _ := run(ctx, tx, motorwayLos, road, q.Now.Add(-roads.MotorwayMeasuredFreshMin*time.Minute), q.Lanes)
	ms, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.Measured, error) {
		var v roads.Measured
		var name string
		return v, r.Scan(&name, &v.Sites, &v.Standstill, &v.Slow, &v.Heavy, &v.Free, &v.LaneSites, &v.MeasuredAt, &v.SlowNear)
	})
	for i := range ms[:min(1, len(ms))] {
		out.Measured = &ms[i]
	}
	return err
}

// readPlansNow reads the plan's works in force when the key may read a plan.
func readPlansNow(ctx context.Context, tx pgx.Tx, road []string, q roads.MotorwayQuery, out *roads.MotorwayRead) error {
	if len(q.Plans) == 0 {
		return nil
	}
	rows, _ := run(ctx, tx, motorwayPlans, q.Plans, road, q.Now, roads.MotorwayPlansMax)
	var err error
	out.Plans, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.PlanNow, error) {
		var v roads.PlanNow
		var source string
		return v, r.Scan(&v.Family, &v.ID, &source, &v.Road, &v.Kind, &v.Name, &v.Section, &v.Cause, &v.From, &v.To, &v.OnRoad)
	})
	return err
}

// readJams asks the listed jams' sweeps of the last half hour and, when the
// held history reaches the minimum, their history at this hour.
func (s *Store) readJams(ctx context.Context, tx pgx.Tx, now time.Time, out *roads.MotorwayRead) error {
	jams := roads.Jams(out.Events)
	if len(jams) == 0 {
		return nil
	}
	filed, stems, titles, subs := make([]string, len(jams)), make([]string, len(jams)), make([]*string, len(jams)), make([]*string, len(jams))
	for i, j := range jams {
		filed[i], stems[i], titles[i], subs[i] = j.Filed(), roads.Stem(j.ID), j.Title, j.Subtitle
	}
	rows, _ := run(ctx, tx, motorwayJamTicks, filed, stems, titles, subs, now)
	var terr error
	out.JamTicks, terr = pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.JamTickRow, error) {
		var v roads.JamTickRow
		return v, r.Scan(&v.Group, &v.At, &v.Extents, &v.DelayS)
	})
	oldest, herr := s.jamHist.oldestAt(ctx, tx, now)
	if out.HistoryReady = roads.JamHistoryReady(oldest, now); !out.HistoryReady {
		return errors.Join(terr, herr)
	}
	rows, _ = run(ctx, tx, motorwayJamUsual, filed, titles, subs, now, roads.JamUsualWeeksBack, roads.JamUsualFrom(now))
	var uerr error
	out.JamUsual, uerr = pgx.CollectRows(rows, func(r pgx.CollectableRow) (roads.JamUsualRow, error) {
		var v roads.JamUsualRow
		return v, r.Scan(&v.I, &v.WeeksObserved, &v.WeeksWithJam, &v.HowCases, &v.HowFrom, &v.HowTo)
	})
	return errors.Join(terr, herr, uerr)
}
