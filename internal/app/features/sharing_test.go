package features_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type sharingFake struct {
	*fake
	stations, nearest               []transit.SharingStation
	vehicles                        []transit.SharingVehicle
	stationErr, vehicleErr, nearErr error
}

func (f *sharingFake) SharingStations(context.Context, transit.SharingQuery, []string) ([]transit.SharingStation, error) {
	return f.stations, f.stationErr
}

func (f *sharingFake) SharingVehicles(context.Context, transit.SharingQuery, []string) ([]transit.SharingVehicle, error) {
	return f.vehicles, f.vehicleErr
}

func (f *sharingFake) SharingNearest(context.Context, transit.SharingQuery, []string) ([]transit.SharingStation, error) {
	return f.nearest, f.nearErr
}

func sharingService(f *sharingFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf("transit")
	return features.New(features.Repositories{Register: f, Sharing: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func ptr[T any](v T) *T { return &v }

func TestSharingListsTheAvailableStationsAndTheFreshVehicles(t *testing.T) {
	req.Covers(t, "SEC-030")
	k := key("commercial", []access.Scope{"transit"}, "f1")
	seen, old := now.Add(-time.Minute), now.Add(-time.Hour)
	f := &sharingFake{
		stations: []transit.SharingStation{
			{ID: "a", StatusSeenAt: &seen, VehiclesAvailable: ptr(0), Family: "f1"},
			{ID: "b", StatusSeenAt: &seen, VehiclesAvailable: ptr(3), Family: "f1"},
			{ID: "c", StatusSeenAt: &seen, VehiclesAvailable: ptr(2), IsRenting: ptr(true), Family: "f1"},
			{ID: "d", StatusSeenAt: &old, VehiclesAvailable: ptr(2), Family: "f1"},
		},
		vehicles: []transit.SharingVehicle{{VehiclesSeenAt: &seen, Propulsion: ptr("electric"), Family: "f1"}, {VehiclesSeenAt: &old, Family: "f1"}, {Family: "f1"}},
	}
	a, err := sharingService(f).Sharing(context.Background(), k, transit.SharingQuery{Limit: 1, OnlyAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Stations) != 1 || a.Stations[0].ID != "b" || a.InRadius != 4 || len(a.Vehicles) != 1 || !a.Vehicles[0].Electric() || a.Nearest != nil || a.VehiclesCapped {
		t.Errorf("available %+v", a)
	}
	if a, _ = sharingService(f).Sharing(context.Background(), k, transit.SharingQuery{Limit: 8}); len(a.Stations) != 4 || a.Stations[3].Current(now) {
		t.Errorf("all %+v", a)
	}
	f = &sharingFake{nearest: []transit.SharingStation{{ID: "z", Family: "f1"}}, vehicles: make([]transit.SharingVehicle, transit.SharingVehicleRead)}
	if a, err = sharingService(f).Sharing(context.Background(), k, transit.SharingQuery{Limit: 8}); err != nil || a.Nearest == nil || !a.VehiclesCapped {
		t.Errorf("nearest %+v %v", a, err)
	}
	if a, err = sharingService(&sharingFake{}).Sharing(context.Background(), k, transit.SharingQuery{Limit: 8}); err != nil || a.Nearest != nil {
		t.Errorf("none %+v %v", a, err)
	}
}

func TestSharingRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"transit"}, "f1")
	for name, c := range map[string]struct {
		f    *sharingFake
		want error
	}{
		"stations":   {&sharingFake{stationErr: errStore}, errStore},
		"vehicles":   {&sharingFake{vehicleErr: errStore}, errStore},
		"nearest":    {&sharingFake{nearErr: errStore}, errStore},
		"post-check": {&sharingFake{nearest: []transit.SharingStation{{Family: "f2"}}}, features.ErrGateViolation},
	} {
		if _, err := sharingService(c.f).Sharing(context.Background(), full, transit.SharingQuery{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
