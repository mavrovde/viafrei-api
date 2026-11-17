package accessstore

import (
	"context"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// appendBoardSQL appends one row per departure of a board: the arrays are
// the departures' columns side by side. The table fixes the source, its
// attribution and its licence; the role may insert and read, nothing else.
const appendBoardSQL = `
insert into domains.train_board_departures (eva, observed_at, planned_at, changed_at, platform, changed_platform, category, train_number, line, destination, cancelled)
select $1, $2, d.planned_at, d.changed_at, d.platform, d.changed_platform, d.category, d.train_number, d.line, d.destination, d.cancelled
from unnest($3::timestamptz[], $4::timestamptz[], $5::text[], $6::text[], $7::text[], $8::text[], $9::text[], $10::text[], $11::boolean[])
  as d(planned_at, changed_at, platform, changed_platform, category, train_number, line, destination, cancelled)`

// AppendTrainBoard appends the departures of b, a board the MCP server
// fetched live, in one statement (features.TrainBoardWriter). A board with
// no departure appends nothing.
func (s *Store) AppendTrainBoard(ctx context.Context, b transit.TrainBoard) error {
	n := len(b.Departures)
	if n == 0 {
		return nil
	}
	planned, changed := make([]time.Time, n), make([]*time.Time, n)
	texts := make([][]*string, 6)
	for i := range texts {
		texts[i] = make([]*string, n)
	}
	cancelled := make([]bool, n)
	for i, d := range b.Departures {
		planned[i], changed[i], cancelled[i] = d.PlannedAt, d.ChangedAt, d.Cancelled
		for j, v := range []*string{d.Platform, d.ChangedPlatform, d.Category, d.Number, d.Line, d.Destination} {
			texts[j][i] = v
		}
	}
	_, err := s.pool.Exec(ctx, appendBoardSQL, b.EVA, b.FetchedAt, planned, changed, texts[0], texts[1], texts[2], texts[3], texts[4], texts[5], cancelled)
	return err
}
