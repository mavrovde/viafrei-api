package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// Hydrogen is the hydrogen-station port.
type Hydrogen interface {
	// HydrogenNear returns up to q.Limit+1 stations of the given families
	// within q.RadiusM of q.At with a refill solution for q.Vehicle,
	// nearest first, then by name.
	HydrogenNear(ctx context.Context, q energy.HydrogenQuery, families []string) ([]energy.HydrogenStation, error)
	// HydrogenNearest returns the one nearest such station at any
	// distance, or none.
	HydrogenNearest(ctx context.Context, q energy.HydrogenQuery, families []string) ([]energy.HydrogenStation, error)
}

// HydrogenStations is the answer to a hydrogen search: the stations inside
// the radius, or, when none is, the nearest one beyond it.
type HydrogenStations struct {
	Answer[energy.HydrogenStation]
	// Nearest is the nearest station beyond the radius when none is inside.
	Nearest *energy.HydrogenStation
}

// HydrogenStations answers "where can I refuel hydrogen near here" for key
// as the platform's hydrogen search answers it.
func (s *Service) HydrogenStations(ctx context.Context, key access.Record, q energy.HydrogenQuery) (HydrogenStations, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return HydrogenStations{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "energy"))
	if err != nil {
		return HydrogenStations{}, err
	}
	near, err := s.repos.Hydrogen.HydrogenNear(ctx, q, permitted)
	if err != nil {
		return HydrogenStations{}, err
	}
	more := len(near) > q.Limit
	if more {
		near = near[:q.Limit]
	}
	var nearest *energy.HydrogenStation
	if len(near) == 0 {
		beyond, err := s.repos.Hydrogen.HydrogenNearest(ctx, q, permitted)
		if err != nil {
			return HydrogenStations{}, err
		}
		if len(beyond) > 0 {
			nearest = &beyond[0]
		}
	}
	var contributed []string
	for _, h := range near {
		contributed = append(contributed, h.Family)
	}
	if nearest != nil {
		contributed = append(contributed, nearest.Family)
	}
	if err := gate.Check(contributed); err != nil {
		return HydrogenStations{}, err
	}
	fams, err := s.creditsOf(ctx, contributed)
	if err != nil {
		return HydrogenStations{}, err
	}
	return HydrogenStations{
		Answer:  Answer[energy.HydrogenStation]{Items: near, HasMore: more, Families: fams, Partial: partial, At: s.now()},
		Nearest: nearest,
	}, nil
}
