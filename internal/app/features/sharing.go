package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// Sharing is the bike-sharing port.
type Sharing interface {
	// SharingStations returns up to transit.SharingStationRead stations of
	// the families in q's circle, nearest first, with their latest count.
	SharingStations(ctx context.Context, q transit.SharingQuery, families []string) ([]transit.SharingStation, error)
	// SharingVehicles returns up to transit.SharingVehicleRead free-floating
	// vehicles in q's circle, nearest first.
	SharingVehicles(ctx context.Context, q transit.SharingQuery, families []string) ([]transit.SharingVehicle, error)
	// SharingNearest returns the nearest station within
	// transit.SharingNearestM, or none.
	SharingNearest(ctx context.Context, q transit.SharingQuery, families []string) ([]transit.SharingStation, error)
}

// SharingAnswer is the answer to a bike-sharing question.
type SharingAnswer struct {
	// Stations are the stations listed: the available ones when only those
	// were asked for, at most the limit.
	Stations []transit.SharingStation
	// InRadius is every station in the circle, available or not.
	InRadius int
	// Vehicles are the free-floating vehicles whose system's status is
	// fresh, nearest first.
	Vehicles []transit.SharingVehicle
	// VehiclesCapped is set when the vehicle read hit its cap: the count is
	// a minimum.
	VehiclesCapped bool
	// Nearest is the nearest station within 100 km when none is in the
	// circle.
	Nearest *transit.SharingStation
	Meta    Answer[struct{}]
}

// Sharing answers "where can I get a shared bike near here" for key as the
// platform's sharing search answers it, at the answer's instant.
func (s *Service) Sharing(ctx context.Context, key access.Record, q transit.SharingQuery) (SharingAnswer, error) {
	q.Now = s.now()
	v, meta, err := listed(ctx, s, key, "transit", func(permitted []string) (SharingAnswer, []string, error) {
		var a SharingAnswer
		stations, err := s.repos.Sharing.SharingStations(ctx, q, permitted)
		if err != nil {
			return a, nil, err
		}
		vehicles, err := s.repos.Sharing.SharingVehicles(ctx, q, permitted)
		if err != nil {
			return a, nil, err
		}
		a.InRadius, a.VehiclesCapped = len(stations), len(vehicles) >= transit.SharingVehicleRead
		for _, st := range stations {
			if len(a.Stations) < q.Limit && (!q.OnlyAvailable || st.Available(q.Now)) {
				a.Stations = append(a.Stations, st)
			}
		}
		for _, v := range vehicles {
			if transit.SharingFresh(v.VehiclesSeenAt, q.Now) {
				a.Vehicles = append(a.Vehicles, v)
			}
		}
		if len(stations) == 0 {
			nearest, err := s.repos.Sharing.SharingNearest(ctx, q, permitted)
			if err != nil {
				return a, nil, err
			}
			if len(nearest) > 0 {
				a.Nearest = &nearest[0]
			}
		}
		var contributed []string
		for _, st := range a.Stations {
			contributed = append(contributed, st.Family)
		}
		for _, v := range a.Vehicles {
			contributed = append(contributed, v.Family)
		}
		if a.Nearest != nil {
			contributed = append(contributed, a.Nearest.Family)
		}
		return a, contributed, nil
	})
	v.Meta = meta
	return v, err
}
