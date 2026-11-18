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
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var fuRegister = []access.Terms{
	{Family: "f1", Domain: "energy", UseClasses: all, Version: "v1"},
	{Family: "f2", Domain: "energy", UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
}

type fuFake struct {
	*fake
	stations []energy.FuelStation
	readErr  error
	gotFams  []string
}

func (f *fuFake) FuelNear(_ context.Context, _ energy.FuelQuery, fams []string) ([]energy.FuelStation, error) {
	f.gotFams = fams
	return f.stations, f.readErr
}

func fuService(f *fuFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = fuRegister
	return features.New(features.Repositories{Register: f, Fuel: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestFuelStationsAreRankedGatedAndAttributed(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001", "SEC-030")
	// The hours decide: a keeps Saturday hours (closed on this Sunday) though
	// it reports open; b and c are open around the clock.
	yes := true
	allDay, saturday := energy.Hours{WholeDay: &yes}, energy.Hours{Times: []energy.OpeningTime{{Text: "Sa", Start: "08:00", End: "12:00"}}}
	f := &fuFake{stations: []energy.FuelStation{
		{ID: "a", DistanceM: 1, Open: "open", Hours: saturday, Family: "f1"},
		{ID: "b", DistanceM: 2, Open: "closed", Hours: allDay, Family: "f1"},
		{ID: "c", DistanceM: 3, Hours: allDay, Family: "f1"},
	}}
	a, err := fuService(f).FuelStations(context.Background(), key("commercial", []access.Scope{"energy"}, "f1", "f2"), energy.FuelQuery{OpenNow: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFams, []string{"f1"}) || len(a.Items) != 1 || a.Items[0].ID != "b" || !a.HasMore || a.Matched != 2 ||
		a.DroppedClosed != 1 || !a.Partial || !a.At.Equal(now) || !slices.Equal(names(a.Families), []string{"f1"}) {
		t.Errorf("answer %+v", a)
	}
}

func TestFuelStationsRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"energy"}, "f1")
	for name, c := range map[string]struct {
		f    *fuFake
		k    access.Record
		want error
	}{
		"register":   {&fuFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&fuFake{}, key("commercial", []access.Scope{"energy"}, "f2"), access.ErrNonePermitted},
		"read":       {&fuFake{readErr: errStore}, full, errStore},
		"post-check": {&fuFake{stations: []energy.FuelStation{{Family: "f2"}}}, full, features.ErrGateViolation},
		"provenance": {&fuFake{fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, err := fuService(c.f).FuelStations(context.Background(), c.k, energy.FuelQuery{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
