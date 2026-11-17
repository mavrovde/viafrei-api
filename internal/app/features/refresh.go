package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// FuelRefresher forwards a refresh ask to the service that holds the
// provider key, its guards and its write path (G8).
type FuelRefresher interface {
	// Refresh asks for one refresh and relays its outcome within the
	// refresher's deadline; energy.ErrRefreshArea when the refresher does
	// not know the area.
	Refresh(ctx context.Context, a energy.RefreshAsk) (energy.RefreshAnswer, error)
}

// WithFuelRefresher sets the refresher fuel refresh asks are forwarded to.
func (s *Service) WithFuelRefresher(r FuelRefresher) *Service {
	s.refresher = r
	return s
}

// FuelRefresh forwards a person's refresh ask for key, as the platform's
// site asks it today. Only a key of audience own may ask; an instance with
// no refresher refuses with energy.ReasonNotConfigured.
func (s *Service) FuelRefresh(ctx context.Context, key access.Record, a energy.RefreshAsk) (energy.RefreshAnswer, error) {
	if key.Audience != access.AudienceOwn {
		return energy.RefreshAnswer{}, ErrOwnKeyOnly
	}
	if err := a.Validate(); err != nil {
		return energy.RefreshAnswer{}, err
	}
	if s.refresher == nil {
		return energy.RefreshAnswer{State: energy.RefreshRefused, Reason: energy.ReasonNotConfigured}, nil
	}
	return s.refresher.Refresh(ctx, a)
}
