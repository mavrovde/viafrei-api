package features

import (
	"context"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// Affected is the port of the affected-stations board.
type Affected interface {
	// AffectedStations returns up to limit stations of the given families
	// ranked by what affects them at now, a station with nothing left out.
	AffectedStations(ctx context.Context, families []string, now time.Time, limit int) ([]transit.Affected, error)
}

// AffectedStations answers which stations are most affected now for key (the
// site's station board), from the transit families it may read and, when it
// has the scope accessibility, the lift families; without it the lifts are
// not counted and the answer is partial.
func (s *Service) AffectedStations(ctx context.Context, key access.Record, limit int) ([]transit.Affected, Answer[struct{}], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return nil, Answer[struct{}]{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "transit"))
	if err != nil {
		return nil, Answer[struct{}]{}, err
	}
	lifts, liftsPartial, err := gate.Answer(access.Answering(reg, "accessibility"))
	if err != nil || !key.Plan.Has("accessibility") {
		lifts, liftsPartial = nil, true
	}
	permitted, partial = append(permitted, lifts...), partial || liftsPartial
	v, err := s.repos.Affected.AffectedStations(ctx, permitted, s.now(), limit)
	if err != nil {
		return nil, Answer[struct{}]{}, err
	}
	var contributed []string
	for _, a := range v {
		contributed = append(contributed, a.Families...)
	}
	slices.Sort(contributed)
	contributed = slices.Compact(contributed)
	if err := gate.Check(contributed); err != nil {
		return nil, Answer[struct{}]{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return nil, Answer[struct{}]{}, err
	}
	return v, Answer[struct{}]{Families: fams, Partial: partial, At: s.now()}, nil
}
