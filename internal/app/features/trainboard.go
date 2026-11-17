package features

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// TrainBoards reads the newest stored board of a station.
type TrainBoards interface {
	// TrainBoard returns the newest board of the station recorded under
	// the families, nil when none is.
	TrainBoard(ctx context.Context, eva string, families []string) (*transit.TrainBoard, error)
}

// ErrOwnKeyOnly refuses an ask by a key that is not one of ViaFrei's own.
var ErrOwnKeyOnly = errors.New("features: only ViaFrei's own keys may call this operation")

// TrainBoardAnswer is a station's newest board while it is fresh: Board is
// nil when none is stored or the newest is older than
// transit.TrainBoardFreshFor, and AsOf then states the newest fetch held,
// if any.
type TrainBoardAnswer struct {
	Board *transit.TrainBoard
	AsOf  *time.Time
	Meta  Answer[struct{}]
}

// TrainBoard answers a station's board as the MCP server shows it, from
// the boards the live refresher stored: DB Timetables alone, never a share-alike family.
func (s *Service) TrainBoard(ctx context.Context, key access.Record, eva string) (TrainBoardAnswer, error) {
	now := s.now()
	var asOf *time.Time
	b, meta, err := listedOwn(ctx, s, key, "transit", func(permitted []string) (*transit.TrainBoard, []string, error) {
		if !slices.Contains(permitted, transit.TrainBoardFamily) {
			return nil, nil, access.ErrNonePermitted
		}
		b, err := s.repos.TrainBoards.TrainBoard(ctx, eva, []string{transit.TrainBoardFamily})
		if err != nil || b == nil {
			return nil, nil, err
		}
		at := b.FetchedAt
		asOf = &at
		if now.Sub(b.FetchedAt) > transit.TrainBoardFreshFor {
			return nil, nil, nil
		}
		return b, []string{transit.TrainBoardFamily}, nil
	})
	return TrainBoardAnswer{Board: b, AsOf: asOf, Meta: meta}, err
}
