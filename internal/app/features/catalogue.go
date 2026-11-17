package features

import (
	"context"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Catalogue is the port of the motorway catalogue.
type Catalogue interface {
	Motorways(ctx context.Context, q roads.CatalogueQuery, families []string, now time.Time) ([]roads.Motorway, error)
}

// MotorwaysAnswer is the motorway catalogue answer.
type MotorwaysAnswer struct {
	Motorways []roads.Motorway
	Meta      Answer[struct{}]
}

// Motorways answers the motorway catalogue for key, from the roads families
// it may read.
func (s *Service) Motorways(ctx context.Context, key access.Record, q roads.CatalogueQuery) (MotorwaysAnswer, error) {
	v, meta, err := listed(ctx, s, key, "roads", func(permitted []string) ([]roads.Motorway, []string, error) {
		// The catalogue is reference rows: the answer names the families
		// consulted for its situation counts.
		ms, err := s.repos.Catalogue.Motorways(ctx, q, permitted, s.now())
		return ms, nil, err
	})
	return MotorwaysAnswer{Motorways: v, Meta: meta}, err
}
