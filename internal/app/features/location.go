package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// Locations is the reverse-description port.
type Locations interface {
	// Location returns what of the families lies at q.At.
	Location(ctx context.Context, q places.LocationQuery, families []string) (places.Location, error)
}

// LocationAnswer is the answer to "where is this position".
type LocationAnswer struct {
	places.Location
	Meta Answer[struct{}]
}

// Location answers "where is this position" for key as the platform's
// description of a position does: independent facts, each nil when none is
// within its reach.
func (s *Service) Location(ctx context.Context, key access.Record, q places.LocationQuery) (LocationAnswer, error) {
	v, meta, err := listed(ctx, s, key, "places", func(permitted []string) (places.Location, []string, error) {
		v, err := s.repos.Locations.Location(ctx, q, permitted)
		var contributed []string
		if v.Address != nil {
			contributed = append(contributed, v.Address.Family)
		}
		for _, p := range []*places.Place{v.Municipality, v.District, v.State} {
			if p != nil {
				contributed = append(contributed, p.Family)
			}
		}
		return v, contributed, err
	})
	return LocationAnswer{Location: v, Meta: meta}, err
}
