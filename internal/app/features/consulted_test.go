package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// An empty answer credits, with their notes, the permitted families that
// hold rows of the dataset it read, not every family of the domain: an empty fuel
// answer credits no charging family, as the platform's fuel search credits
// its own sources; one that found a station credits that station's family.
// A failed read of the feeding families is the answer's error.
func TestAnEmptyAnswerCreditsOnlyTheFamiliesOfItsDataset(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	ctx := context.Background()
	energyKey := key("consumer", []access.Scope{"energy"}, "f1", "f2")
	f := &fuFake{fake: &fake{feeding: map[provenance.Dataset][]string{provenance.DatasetFuel: {"f2"}}}}
	a, err := fuService(f).FuelStations(ctx, energyKey, energy.FuelQuery{Limit: 5})
	if err != nil || !slices.Equal(names(a.Families), []string{"f2"}) || !slices.Equal(f.gotFeeding, []provenance.Dataset{provenance.DatasetFuel}) {
		t.Errorf("empty fuel answer: credited %v, asked %v, %v", names(a.Families), f.gotFeeding, err)
	}
	f = &fuFake{fake: &fake{feeding: map[provenance.Dataset][]string{}}, stations: []energy.FuelStation{{ID: "a", Family: "f1"}}}
	if a, err = fuService(f).FuelStations(ctx, energyKey, energy.FuelQuery{Limit: 5}); err != nil || !slices.Equal(names(a.Families), []string{"f1"}) || f.gotFeeding != nil {
		t.Errorf("a station found: credited %v, asked %v, %v", names(a.Families), f.gotFeeding, err)
	}
	f = &fuFake{fake: &fake{feedErr: errStore}}
	if _, err = fuService(f).FuelStations(ctx, energyKey, energy.FuelQuery{Limit: 5}); !errors.Is(err, errStore) {
		t.Errorf("fuel, feeding read failed: %v", err)
	}

	// listed: an empty lorry answer credits the lorry families only.
	l := &lorryRepo{fake: &fake{feeding: map[provenance.Dataset][]string{provenance.DatasetLorry: {}}}}
	s := features.New(features.Repositories{Register: l, Lorry: l}, features.Settings{Language: "en"}, func() time.Time { return now })
	got, err := s.LorryRules(ctx, key("commercial", []access.Scope{"roads"}, "rd"), roads.LorryQuery{Vehicle: "lorry"})
	if err != nil || len(got.Meta.Families) != 0 || !slices.Equal(l.gotFeeding, []provenance.Dataset{provenance.DatasetLorry}) {
		t.Errorf("empty lorry answer: credited %v, asked %v, %v", names(got.Meta.Families), l.gotFeeding, err)
	}
	l.feedErr = errStore
	if _, err := s.LorryRules(ctx, key("commercial", []access.Scope{"roads"}, "rd"), roads.LorryQuery{Vehicle: "lorry"}); !errors.Is(err, errStore) {
		t.Errorf("lorry, feeding read failed: %v", err)
	}
}

// The road lamp credits the weather families whose warnings it reads, not
// those of road weather, whose note ("Glättemeldeanlagen") belongs to an
// answer that states their readings; with no situation found it credits
// the roads families that hold rows of the road events. A failed read of either is
// the answer's error.
func TestTheLampCreditsOnlyTheWarningFamilies(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	ctx := context.Background()
	consumer := key("consumer", weatherScope, "r1", "r2", "w1", "w2")
	holdings := map[provenance.Dataset][]string{provenance.DatasetWarnings: {"w1"}, provenance.DatasetRoadEvents: {"r2"}}
	f := &roadFake{fake: &fake{feeding: holdings}, warns: []roads.Warning{{Family: "w1", Level: 2}}}
	got, err := roadService(f).RoadConditions(ctx, consumer, roads.AreaQuery{State: "de"})
	if err != nil || !slices.Equal(names(got.Families), []string{"r2", "w1"}) || !slices.Equal(f.gotFams.Weather, []string{"w1", "w2"}) {
		t.Errorf("credited %v, read %v, %v", names(got.Families), f.gotFams, err)
	}
	if !slices.Equal(f.gotFeeding, []provenance.Dataset{provenance.DatasetRoadEvents, provenance.DatasetWarnings}) {
		t.Errorf("asked %v", f.gotFeeding)
	}
	for _, d := range []provenance.Dataset{provenance.DatasetRoadEvents, provenance.DatasetWarnings} {
		f := &roadFake{fake: &fake{feeding: holdings}}
		f.feedErr, f.feedErrOn = errStore, d
		if _, err := roadService(f).RoadConditions(ctx, consumer, roads.AreaQuery{State: "de"}); !errors.Is(err, errStore) {
			t.Errorf("%s read failed: %v", d, err)
		}
	}
}

// A weather answer that found no warning credits the families that hold rows of the
// warnings, not those of road weather (its note "Glättemeldeanlagen"), and
// its sight is theirs.
func TestAWeatherAnswerCreditsOnlyTheWarningFamilies(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	f := &wxFake{fake: &fake{feeding: map[provenance.Dataset][]string{provenance.DatasetWarnings: {"w2"}}}}
	a, err := wxService(f).WeatherRisk(context.Background(), key("consumer", []access.Scope{"weather"}, "w1", "w2"), weather.Query{})
	if err != nil || !slices.Equal(names(a.Families), []string{"w2"}) {
		t.Errorf("credited %v, %v", names(a.Families), err)
	}
}

// A roads answer carries the store's counts per family as they are.
func TestARoadsAnswerCarriesTheCountsPerFamily(t *testing.T) {
	req.Covers(t, "FR-ROAD-006")
	f := &roadFake{counts: map[string]int{"r1": 4}}
	got, err := roadService(f).RoadConditions(context.Background(), key("consumer", weatherScope, "r1"), roads.AreaQuery{State: "de"})
	if err != nil || len(got.Counts) != 1 || got.Counts["r1"] != 4 {
		t.Errorf("counts %v, %v", got.Counts, err)
	}
}
