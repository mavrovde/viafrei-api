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
		// An answer credits the families whose rows it states, as the
		// platform's answers do: those of the situations its roads count.
		// A family that stated nothing (a lorry or zone register, say) is
		// not credited, and its condition is not carried; with no situation,
		// the families consulted are named.
		ms, err := s.repos.Catalogue.Motorways(ctx, q, permitted, s.now())
		return ms, motorwayFamilies(ms), err
	})
	return MotorwaysAnswer{Motorways: v, Meta: meta}, err
}

// motorwayFamilies are the families of the listed roads' situations, each
// once, in order of first appearance; empty when none is counted.
func motorwayFamilies(ms []roads.Motorway) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range ms {
		for _, f := range m.Sources {
			if f != "" && !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}
