package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Lorry is the port of the lorry rows.
type Lorry interface {
	LorryRules(ctx context.Context, q roads.LorryQuery, families []string) ([]roads.LorryRule, error)
}

// LorryAnswer is the answer of where a lorry may go.
type LorryAnswer struct {
	Rules []roads.LorryRule
	Meta  Answer[struct{}]
}

// LorryRules answers "where may a lorry, or a long lorry, go" for key, from
// the roads families it may read.
func (s *Service) LorryRules(ctx context.Context, key access.Record, q roads.LorryQuery) (LorryAnswer, error) {
	v, meta, err := listed(ctx, s, key, "roads", func(permitted []string) ([]roads.LorryRule, []string, error) {
		rs, err := s.repos.Lorry.LorryRules(ctx, q, permitted)
		var fams []string
		for _, r := range rs {
			fams = append(fams, r.Family)
		}
		return rs, fams, err
	})
	return LorryAnswer{Rules: v, Meta: meta}, err
}
