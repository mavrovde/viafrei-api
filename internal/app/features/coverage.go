package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// Coverage is the port of what the gazetteer and the addresses hold.
type Coverage interface {
	// PlacesCoverage returns what the given families hold: places per kind,
	// the Länder of the region layer and the Länder with addresses.
	PlacesCoverage(ctx context.Context, families []string) (places.Coverage, error)
}

// PlacesCoverage answers what the gazetteer and the addresses hold for key
// (the platform's gazetteer and addresses resources), from the places
// families it may read.
func (s *Service) PlacesCoverage(ctx context.Context, key access.Record) (places.Coverage, Answer[struct{}], error) {
	return listed(ctx, s, key, "places", func(permitted []string) (places.Coverage, []string, error) {
		c, err := s.repos.Coverage.PlacesCoverage(ctx, permitted)
		return c, c.Families(), err
	})
}
