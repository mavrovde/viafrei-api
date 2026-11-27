package features

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// TrainBoards reads a station's boards: the newest one the live refresher
// recorded, the one the stored-boards worker wrote, and the interchange
// under them.
type TrainBoards interface {
	// TrainBoard returns the newest board of the station recorded under
	// the families, nil when none is.
	TrainBoard(ctx context.Context, eva string, families []string) (*transit.TrainBoard, error)
	// StoredTrainBoard returns the board the stored-boards worker wrote
	// for the station of eva under the families, nil when none is.
	StoredTrainBoard(ctx context.Context, eva string, families []string) (*transit.StoredBoard, error)
	// TrainInterchange reads the lines, the U-Bahn and tram calls in
	// [from, to) around now and their Trip Updates of the timetable stops
	// of the station of evas.
	TrainInterchange(ctx context.Context, evas []string, from, to, now time.Time, families []string) (transit.InterchangeRead, error)
}

// ErrOwnKeyOnly refuses an ask by a key that is not one of ViaFrei's own.
var ErrOwnKeyOnly = errors.New("features: only ViaFrei's own keys may call this operation")

// TrainBoardQuery asks a station's board: its EVA number, how far ahead
// (minutes), how many departures and, when any, the train categories to
// keep (as the board writes them: ICE, IC, RE, RB, S).
type TrainBoardQuery struct {
	EVA         string
	DurationMin int
	Limit       int
	Categories  []string
}

// TrainBoardAnswer is a station's board while one is fresh: Board is the
// board served (a recorded one while fresh, else the stored one while
// fresh) with the departures of the window shown, Cap what the limit cut,
// and AsOf the newest read held, if any; Board is nil when none is fresh.
// Interchange is the U-Bahn and tram block, nil when the station has none.
type TrainBoardAnswer struct {
	Board       *transit.TrainBoard
	Cap         *transit.BoardCap
	AsOf        *time.Time
	Interchange *transit.Interchange
	Meta        Answer[struct{}]
}

// TrainBoard answers a station's board as the MCP server shows it, from
// the boards the live refresher recorded or the stored-boards worker
// wrote: DB Timetables alone, never a share-alike family; the interchange
// is the DELFI timetable's, with the Trip Updates where the key reads them
// (a board's families, boardFamilies).
func (s *Service) TrainBoard(ctx context.Context, key access.Record, q TrainBoardQuery) (TrainBoardAnswer, error) {
	now := s.now()
	var out TrainBoardAnswer
	_, meta, err := listedBoard(ctx, s, key, func(permitted []string) (struct{}, []string, error) {
		if !slices.Contains(permitted, transit.TrainBoardFamily) {
			return struct{}{}, nil, access.ErrNonePermitted
		}
		fams := []string{transit.TrainBoardFamily}
		b, asOf, err := s.freshTrainBoard(ctx, q.EVA, now)
		if err != nil {
			return struct{}{}, nil, err
		}
		out.AsOf = asOf
		contributed := []string{}
		evas := []string{q.EVA}
		if b != nil {
			contributed = fams
			if len(q.Categories) > 0 {
				// The categories are kept before the limit, so a board of
				// many trains still shows the asked ones (#298).
				b.Departures = slices.DeleteFunc(slices.Clone(b.Departures), func(d transit.TrainDeparture) bool {
					return d.Category == nil || !slices.Contains(q.Categories, *d.Category)
				})
			}
			b.Departures, out.Cap = transit.BoardWindow(*b, now, q.DurationMin, q.Limit)
			out.Board = b
			if len(b.EVAs) > 0 {
				evas = b.EVAs
			}
		}
		r, err := s.repos.TrainBoards.TrainInterchange(ctx, evas, now.Add(-transit.InterchangeBackMin*time.Minute),
			now.Add(time.Duration(q.DurationMin)*time.Minute), now, permitted)
		if err != nil {
			return struct{}{}, nil, err
		}
		if out.Interchange = transit.InterchangeOf(r, now, now); out.Interchange != nil {
			contributed = appendNew(contributed, transit.InterchangeFamily)
			for _, d := range out.Interchange.Departures {
				contributed = appendNew(contributed, d.Families...)
			}
		}
		return struct{}{}, contributed, nil
	})
	out.Meta = meta
	return out, err
}

// freshTrainBoard is the station's board while one is fresh: the board the
// live refresher recorded while fresh, else the one the stored-boards
// worker wrote while fresh, nil when neither is; asOf is the newest read
// held, if any.
func (s *Service) freshTrainBoard(ctx context.Context, eva string, now time.Time) (b *transit.TrainBoard, asOf *time.Time, err error) {
	fams := []string{transit.TrainBoardFamily}
	rec, err := s.repos.TrainBoards.TrainBoard(ctx, eva, fams)
	if err != nil {
		return nil, nil, err
	}
	stored, err := s.repos.TrainBoards.StoredTrainBoard(ctx, eva, fams)
	if err != nil {
		return nil, nil, err
	}
	for _, at := range []*time.Time{fetched(rec), readAt(stored)} {
		if at != nil && (asOf == nil || at.After(*asOf)) {
			asOf = at
		}
	}
	switch {
	case rec != nil && now.Sub(rec.FetchedAt) <= transit.TrainBoardFreshFor:
		b = rec
	case stored != nil && now.Sub(stored.ReadAt) <= transit.StoredBoardFreshFor:
		v := transit.TrainBoardOf(eva, *stored)
		b = &v
	}
	return b, asOf, nil
}

func fetched(b *transit.TrainBoard) *time.Time {
	if b == nil {
		return nil
	}
	return &b.FetchedAt
}

func readAt(b *transit.StoredBoard) *time.Time {
	if b == nil {
		return nil
	}
	at := b.ReadAt.UTC()
	return &at
}
