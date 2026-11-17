package conformance_test

import (
	"context"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// laterFeatures answers every operation of 0.3.0's inventory mapping with an
// empty answer: the conformance fakes embed it, their tests read the
// operations they name.
type laterFeatures struct{}

func (laterFeatures) SpeedCameras(context.Context, access.Record, roads.SpeedQuery) (features.SpeedAnswer, error) {
	return features.SpeedAnswer{}, nil
}

func (laterFeatures) Sharing(context.Context, access.Record, transit.SharingQuery) (features.SharingAnswer, error) {
	return features.SharingAnswer{}, nil
}

func (laterFeatures) RecordTrainBoard(context.Context, access.Record, transit.TrainBoard) error {
	return nil
}

func (laterFeatures) FuelRefresh(context.Context, access.Record, energy.RefreshAsk) (energy.RefreshAnswer, error) {
	return energy.RefreshAnswer{State: energy.RefreshNoneDue}, nil
}

func (laterFeatures) TrainBoard(context.Context, access.Record, string) (features.TrainBoardAnswer, error) {
	return features.TrainBoardAnswer{}, nil
}

func (laterFeatures) ParkingBays(context.Context, access.Record, parking.BayQuery) (features.BaysAnswer, error) {
	return features.BaysAnswer{}, nil
}

func (laterFeatures) Addresses(context.Context, access.Record, places.AddressQuery) (features.AddressAnswer, error) {
	return features.AddressAnswer{}, nil
}

func (laterFeatures) Pois(context.Context, access.Record, places.PoiQuery) (features.PoiAnswer, error) {
	return features.PoiAnswer{}, nil
}

func (laterFeatures) Location(context.Context, access.Record, places.LocationQuery) (features.LocationAnswer, error) {
	return features.LocationAnswer{}, nil
}

func (laterFeatures) Disruptions(context.Context, access.Record, transit.DisruptionQuery) (features.DisruptionAnswer, error) {
	return features.DisruptionAnswer{}, nil
}

func (laterFeatures) Nearby(context.Context, access.Record, features.NearbyQuery) (features.NearbyAnswer, error) {
	return features.NearbyAnswer{}, nil
}

func (laterFeatures) LowEmissionZones(context.Context, access.Record, rules.ZoneQuery) (features.ZonesAnswer, error) {
	return features.ZonesAnswer{}, nil
}

func (laterFeatures) LorryRules(context.Context, access.Record, roads.LorryQuery) (features.LorryAnswer, error) {
	return features.LorryAnswer{}, nil
}

func (laterFeatures) Sources(context.Context, access.Record, string) ([]provenance.Family, features.Answer[struct{}], error) {
	return nil, features.Answer[struct{}]{}, nil
}

func (laterFeatures) Motorways(context.Context, access.Record, roads.CatalogueQuery) (features.MotorwaysAnswer, error) {
	return features.MotorwaysAnswer{}, nil
}

func (laterFeatures) FuelStation(context.Context, access.Record, string, time.Duration) (energy.FuelStationPage, features.Answer[struct{}], error) {
	return energy.FuelStationPage{}, features.Answer[struct{}]{}, nil
}

func (laterFeatures) ChargingSite(context.Context, access.Record, string, time.Duration) (features.Answer[energy.Site], error) {
	return features.Answer[energy.Site]{}, nil
}

func (laterFeatures) National(context.Context, access.Record, time.Duration) (features.NationalAnswer, error) {
	return features.NationalAnswer{}, nil
}

func (laterFeatures) AffectedStations(context.Context, access.Record, int) ([]transit.Affected, features.Answer[struct{}], error) {
	return nil, features.Answer[struct{}]{}, nil
}

func (laterFeatures) PlacesCoverage(context.Context, access.Record) (places.Coverage, features.Answer[struct{}], error) {
	return places.Coverage{}, features.Answer[struct{}]{}, nil
}

func (laterFeatures) WeatherCell(context.Context, access.Record, string, *geo.Point, int) (features.CellAnswer, error) {
	return features.CellAnswer{}, nil
}

func (laterFeatures) StateWarnings(context.Context, access.Record, string) ([]weather.StateWarning, features.Answer[struct{}], error) {
	return nil, features.Answer[struct{}]{}, nil
}

func (laterFeatures) StateAlerts(context.Context, access.Record, string, int) (features.StateAlertsAnswer, error) {
	return features.StateAlertsAnswer{}, nil
}
