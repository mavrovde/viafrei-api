package features

import (
	"context"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// Sources answers which sources the key may read, of one domain or of every
// domain: each with its attribution, licence and freshness (the platform's
// attribution, coverage and source-status resources).
func (s *Service) Sources(ctx context.Context, key access.Record, domain string) ([]provenance.Family, Answer[struct{}], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return nil, Answer[struct{}]{}, err
	}
	var domains []string
	for _, t := range reg {
		if (domain == "" || t.Domain == domain) && !slices.Contains(domains, t.Domain) {
			domains = append(domains, t.Domain)
		}
	}
	var permitted []string
	partial := false
	for _, d := range domains {
		p, part, err := gate.Answer(access.Answering(reg, d))
		if err != nil {
			partial = true
			continue
		}
		partial = partial || part
		permitted = append(permitted, p...)
	}
	slices.Sort(permitted)
	if len(permitted) == 0 {
		return nil, Answer[struct{}]{At: s.now(), Partial: partial}, nil
	}
	fams, err := s.provenanceOf(ctx, permitted, permitted)
	if err != nil {
		return nil, Answer[struct{}]{}, err
	}
	return fams, Answer[struct{}]{Families: fams, Partial: partial, At: s.now()}, nil
}
