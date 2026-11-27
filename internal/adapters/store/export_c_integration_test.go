//go:build integration

package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// StationRowsOf is how many rows the station search's statement of q's shape
// answers for the limit q.Limit itself (Stations asks one more, to tell
// whether more answer), so a test can hold the statement to its limit.
func (s *Store) StationRowsOf(ctx context.Context, q transit.Query, families []string) (int, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (int, error) {
		var lat, lon *float64
		if q.At != nil {
			lat, lon = &q.At.Lat, &q.At.Lon
		}
		text := transit.Fold(q.Text)
		rows, _ := run(ctx, tx, stationsPage[shapeOf(q, text)], families, lat, lon, text, q.RadiusM, q.Limit, q.Code, q.Railway, q.Area, stationSpellingsOf(q.Text))
		n := 0
		for rows.Next() {
			n++
		}
		return n, rows.Err()
	})
}
