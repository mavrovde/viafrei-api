package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// listed answers one read of one domain for key: the gate, the read over the
// permitted families, the post-check of the families that contributed, and
// their provenance, or, when none did, that of the permitted families that
// hold rows of dataset (consultedIn), as the platform's tool credits its own
// sources. An empty dataset credits every permitted family of the domain:
// the answers whose views the snapshot does not read (provenance.Dataset).
func listed[T any](ctx context.Context, s *Service, key access.Record, domain string, dataset provenance.Dataset,
	read func(permitted []string) (T, []string, error)) (T, Answer[struct{}], error) {
	return listedCredits(ctx, s, key, domain, dataset, false, read)
}

// listedOwn is listed for a search that credits only the families whose
// rows it states, so an empty answer credits none (creditsOf), as the
// platform's search of the same domain does.
func listedOwn[T any](ctx context.Context, s *Service, key access.Record, domain string,
	read func(permitted []string) (T, []string, error)) (T, Answer[struct{}], error) {
	return listedCredits(ctx, s, key, domain, "", true, read)
}

func listedCredits[T any](ctx context.Context, s *Service, key access.Record, domain string, dataset provenance.Dataset, own bool,
	read func(permitted []string) (T, []string, error)) (T, Answer[struct{}], error) {
	var zero T
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return zero, Answer[struct{}]{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, domain))
	if err != nil {
		return zero, Answer[struct{}]{}, err
	}
	v, contributed, err := read(permitted)
	if err != nil {
		return zero, Answer[struct{}]{}, err
	}
	if err := gate.Check(contributed); err != nil {
		return zero, Answer[struct{}]{}, err
	}
	var fams []provenance.Family
	if own {
		fams, err = s.creditsOf(ctx, contributed)
	} else {
		consulted := permitted
		if len(contributed) == 0 && dataset != "" {
			if consulted, err = s.consultedIn(ctx, dataset, permitted); err != nil {
				return zero, Answer[struct{}]{}, err
			}
		}
		fams, err = s.provenanceOf(ctx, contributed, consulted)
	}
	if err != nil {
		return zero, Answer[struct{}]{}, err
	}
	return v, Answer[struct{}]{Families: fams, Partial: partial, At: s.now()}, nil
}
