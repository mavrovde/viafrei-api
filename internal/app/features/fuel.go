package features

import (
	"context"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// Fuel is the filling-station port.
type Fuel interface {
	// FuelNear returns the filling stations of the given families within
	// q's radius, nearest first, at most energy.MaxFuelScan, each with its newest
	// price per grade.
	FuelNear(ctx context.Context, q energy.FuelQuery, families []string) ([]energy.FuelStation, error)
	// FuelStation returns the filling station with this id of the given
	// families, with its newest price per grade reported in the held window
	// up to now, or energy.ErrNotFound.
	FuelStation(ctx context.Context, id string, families []string, now time.Time, held time.Duration) (energy.FuelStationPage, error)
}

// FuelStations is a fuel answer and what its open filter left out.
type FuelStations struct {
	Answer[energy.FuelStation]
	Matched        int
	DroppedClosed  int
	DroppedUnknown int
	// DroppedNotWholeDay counts the stations whole_day left out; OpenAt is
	// the instant open_at named.
	DroppedNotWholeDay int
	OpenAt             *time.Time
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
	if len(contributed) == 0 {
		// An empty answer credits the families of filling stations, not
		// every energy family (charging, hydrogen), as the platform's fuel
		// search credits its own sources.
		if permitted, err = s.consultedIn(ctx, provenance.DatasetFuel, permitted); err != nil {
			return FuelStations{}, err
		}
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return FuelStations{}, err
	}
	return FuelStations{
		Answer:  Answer[energy.FuelStation]{Items: a.Stations, HasMore: a.Matched > len(a.Stations), Families: fams, Partial: partial, At: now},
		Matched: a.Matched, DroppedClosed: a.DroppedClosed, DroppedUnknown: a.DroppedUnknown,
		DroppedNotWholeDay: a.DroppedNotWholeDay, OpenAt: a.OpenAt,
	}, nil
}

// FuelStation answers one filling station by its id for key (the site's
// station page and its price block). A family the key may not read makes it
// not found, never a partial answer.
func (s *Service) FuelStation(ctx context.Context, key access.Record, id string, held time.Duration) (energy.FuelStationPage, Answer[struct{}], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return energy.FuelStationPage{}, Answer[struct{}]{}, err
	}
	permitted, _, err := gate.Answer(access.Answering(reg, "energy"))
	if err != nil {
		return energy.FuelStationPage{}, Answer[struct{}]{}, err
	}
	now := s.now()
	st, err := s.repos.Fuel.FuelStation(ctx, id, permitted, now, held)
	if err != nil {
		return energy.FuelStationPage{}, Answer[struct{}]{}, err
	}
	if err := gate.Check([]string{st.Family}); err != nil {
		return energy.FuelStationPage{}, Answer[struct{}]{}, err
	}
	fams, err := s.repos.Register.Provenance(ctx, []string{st.Family}, s.lang(ctx))
	if err != nil {
		return energy.FuelStationPage{}, Answer[struct{}]{}, err
	}
	return st, Answer[struct{}]{Families: fams, At: now}, nil
}

// ChargingSite answers one charging site by its id for key, with its points
// and their last reported availability (the site's own page). A family the
// key may not read makes it not found.
func (s *Service) ChargingSite(ctx context.Context, key access.Record, id string, statusMaxAge time.Duration) (Answer[energy.Site], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	a, err := s.charging(ctx, reg, gate, energy.Query{SiteID: id, Access: energy.FilterAll, Limit: 1, Filter: energy.Filter{StatusMaxAge: statusMaxAge}})
	if err == nil && len(a.Items) == 0 {
		err = energy.ErrNotFound
	}
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	a.HasMore, a.Partial = false, false
	return a, nil
}
