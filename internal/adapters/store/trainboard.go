package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// trainBoard reads the newest recorded board of the station $1, of the
// families $2: the view holds each station's newest fetch only.
var trainBoard = `select t.fetched_at, t.planned_departure_at, t.changed_departure_at, t.platform, t.changed_platform, t.category, t.train_number,
       t.line, t.destination, t.cancelled
from contract.train_board t
where t.station_eva = $1 and t.source_ref = any($2) and ` + permitted("t") + `
order by t.planned_departure_at, t.train_number nulls last`

// TrainBoard returns the station's newest recorded board, nil when none is
// (features.TrainBoards).
func (s *Store) TrainBoard(ctx context.Context, eva string, families []string) (*transit.TrainBoard, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (*transit.TrainBoard, error) {
		rows, _ := tx.Query(ctx, trainBoard, eva, families)
		b := &transit.TrainBoard{EVA: eva}
		ds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (transit.TrainDeparture, error) {
			var d transit.TrainDeparture
			err := r.Scan(&b.FetchedAt, &d.PlannedAt, &d.ChangedAt, &d.Platform, &d.ChangedPlatform, &d.Category, &d.Number, &d.Line, &d.Destination, &d.Cancelled)
			return d, err
		})
		if err != nil || len(ds) == 0 {
			return nil, err
		}
		b.FetchedAt = b.FetchedAt.UTC()
		for i := range ds {
			ds[i].PlannedAt = ds[i].PlannedAt.UTC()
			if ds[i].ChangedAt != nil {
				c := ds[i].ChangedAt.UTC()
				ds[i].ChangedAt = &c
			}
		}
		b.Departures = ds
		return b, nil
	})
}
