package features

import (
	"context"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// Bays reads the on-street bays a municipality publishes.
type Bays interface {
	// Bays returns the bays q asks for, of the families.
	Bays(ctx context.Context, q parking.BayQuery, families []string) (parking.Bays, error)
}

// BaysAnswer is a bay read with its provenance.
type BaysAnswer struct {
	parking.Bays
	Meta Answer[struct{}]
}

// ParkingBays answers the on-street bays of a kind near a position, as the
// platform's parking search answers kind=disabled_badge: a state counts
// only while fresh, a sensor-less site is never free, and the families of
// the listed bays are credited.
func (s *Service) ParkingBays(ctx context.Context, key access.Record, q parking.BayQuery) (BaysAnswer, error) {
	q.Now = s.now()
	v, meta, err := listedOwn(ctx, s, key, "parking", func(permitted []string) (parking.Bays, []string, error) {
		b, err := s.repos.Bays.Bays(ctx, q, permitted)
		if err != nil {
			return b, nil, err
		}
		var fams []string
		for _, x := range b.Items {
			fams = append(fams, x.Family)
		}
		slices.Sort(fams)
		return b, slices.Compact(fams), nil
	})
	return BaysAnswer{Bays: v, Meta: meta}, err
}
