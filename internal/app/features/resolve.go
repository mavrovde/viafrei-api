package features

import (
	"context"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// Resolver is the port to the place resolver's reads.
type Resolver interface {
	// ResolveReads are the reads of the resolver over the given families.
	ResolveReads(families []string) places.ResolveReads
}

// ResolveAnswer is the place a text names, as the platform's resolver
// answers it, and the provenance of the sources it rests on.
type ResolveAnswer struct {
	Resolution places.Resolution
	// Lang is the language the resolution's texts speak.
	Lang string
	Meta Answer[struct{}]
}

// ResolvePlace resolves a free text to the one place it names, as the
// platform's resolver reads it (place.ts resolvePlace), over the places
// families the key may read, the stop families of transit and the roads
// families (the motorway junctions), as the place search reads them; a key
// that may read none of the latter two resolves without them.
func (s *Service) ResolvePlace(ctx context.Context, key access.Record, text string) (ResolveAnswer, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return ResolveAnswer{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "places"))
	if err != nil {
		return ResolveAnswer{}, err
	}
	families := slices.Clone(permitted)
	for _, domain := range []string{"transit", "roads"} {
		if more, _, err := gate.Answer(access.Answering(reg, domain)); err == nil {
			families = append(families, more...)
		}
	}
	lang := s.lang(ctx)
	r, err := places.Resolve(ctx, s.repos.Resolver.ResolveReads(families), text, lang)
	if err != nil {
		return ResolveAnswer{}, err
	}
	var contributed []string
	if r.Kind == places.ResolvedPoint {
		contributed = r.SourceIDs
	}
	if err := gate.Check(contributed); err != nil {
		return ResolveAnswer{}, err
	}
	fams, err := s.creditsOf(ctx, contributed)
	if err != nil {
		return ResolveAnswer{}, err
	}
	return ResolveAnswer{Resolution: r, Lang: lang, Meta: Answer[struct{}]{Families: fams, Partial: partial, At: s.now()}}, nil
}
