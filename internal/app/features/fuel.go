package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// Fuel is the filling-station port.
type Fuel interface {
	// FuelNear returns the filling stations of the given families within
	// q's radius, nearest first, at most energy.MaxFuelScan, each with its newest
	// price per grade.
	FuelNear(ctx context.Context, q energy.FuelQuery, families []string) ([]energy.FuelStation, error)
}

// FuelStations is a fuel answer and what its open filter left out.
type FuelStations struct {
	Answer[energy.FuelStation]
	Matched        int
	DroppedClosed  int
	DroppedUnknown int
}

// FuelStations answers "where can I fill up near here" for key (FR-ENERGY),
// ranked as energy.RankFuel ranks.
func (s *Service) FuelStations(ctx context.Context, key access.Record, q energy.FuelQuery) (FuelStations, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return FuelStations{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "energy"))
	if err != nil {
		return FuelStations{}, err
	}
	near, err := s.repos.Fuel.FuelNear(ctx, q, permitted)
	if err != nil {
		return FuelStations{}, err
	}
	now := s.now()
	a := energy.RankFuel(near, q, now)
	var contributed []string
	for _, st := range a.Stations {
		contributed = append(contributed, st.Family)
	}
	if err := gate.Check(contributed); err != nil {
		return FuelStations{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return FuelStations{}, err
	}
	return FuelStations{
		Answer:  Answer[energy.FuelStation]{Items: a.Stations, HasMore: a.Matched > len(a.Stations), Families: fams, Partial: partial, At: now},
		Matched: a.Matched, DroppedClosed: a.DroppedClosed, DroppedUnknown: a.DroppedUnknown,
	}, nil
}
