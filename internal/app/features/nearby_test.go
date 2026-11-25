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
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var nbRegister = []access.Terms{
	{Family: "e1", Domain: "energy", UseClasses: all, Version: "v1"},
	{Family: "p1", Domain: "parking", UseClasses: all, Version: "v1"},
	{Family: "t1", Domain: "transit", UseClasses: all, Version: "v1"},
	{Family: "pl", Domain: "places", UseClasses: all, Version: "v1"},
	{Family: "px", Domain: "places", UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
	{Family: "r1", Domain: "roads", UseClasses: all, Version: "v1"},
	{Family: "rx", Domain: "roads", UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
}

type nbFake struct {
	*fake
	junctions  []places.Junction
	gotJFams   []string
	needs      map[string][]places.NeedPlace
	jErr, nErr error
	gotNeeds   places.NearbyQuery
	gotFuel    energy.FuelQuery
}

func (f *nbFake) OSMFuelNear(context.Context, energy.FuelQuery, []string, int) ([]energy.OSMFuelStation, error) {
	return nil, nil
}

func (f *nbFake) FuelNear(_ context.Context, q energy.FuelQuery, _ []string) ([]energy.FuelStation, error) {
	f.gotFuel = q
	return nil, nil
}

func (f *nbFake) FuelCoverage(context.Context, energy.FuelQuery, []string, time.Time) (energy.FuelCoverage, error) {
	return energy.FuelCoverage{}, nil
}

func (f *nbFake) Stations(context.Context, transit.Query, []string) ([]transit.Station, bool, error) {
	return []transit.Station{{ID: "s1", Family: "t1"}}, false, nil
}

func (f *nbFake) Station(context.Context, string, []string) (transit.Station, error) {
	return transit.Station{}, nil
}

func (f *nbFake) TimetableReach(context.Context) (transit.Reach, error) {
	return transit.Reach{}, nil
}

func (f *nbFake) Departures(context.Context, transit.DepartureQuery, []string) ([]transit.Departure, error) {
	return nil, nil
}

func (f *nbFake) Platforms(context.Context, []string, []string) ([]transit.PlatformRow, error) {
	return nil, nil
}

func (f *nbFake) StopBoards(context.Context, transit.StopBoardsQuery, []string) ([]transit.StopBoard, error) {
	return nil, nil
}

func (f *nbFake) Facilities(context.Context, string, []string, time.Time) ([]transit.Facility, error) {
	return nil, nil
}

func (f *nbFake) Junctions(_ context.Context, q places.NearbyQuery, fams []string) (places.Junctions, error) {
	f.gotJFams = fams
	return places.Junctions{From: places.JunctionsFromOSM, Rows: f.junctions}, f.jErr
}

func (f *nbFake) NeedPlaces(_ context.Context, q places.NearbyQuery, _ []string) (map[string][]places.NeedPlace, error) {
	f.gotNeeds = q
	return f.needs, f.nErr
}

func nbService(f *nbFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = nbRegister
	return features.New(features.Repositories{Register: f, Charging: f, Parking: f, Fuel: f, Transit: f, Nearby: f},
		features.Settings{Language: "en", PartDeadline: time.Second}, func() time.Time { return now })
}

var nbAll = []access.Scope{"energy", "parking", "transit", "places", "roads"}

func nbQuery(cats ...string) features.NearbyQuery {
	return features.NearbyQuery{At: places.NearbyQuery{RadiusM: 5000}, Categories: cats, FuelWindow: time.Hour}
}

func TestNearbyEveryCategory(t *testing.T) {
	req.Covers(t, "FR-PLACE-001", "SEC-030")
	f := &nbFake{junctions: []places.Junction{{ID: "j1", Name: "Köln-Ost", Family: "pl", RoadBy: "r1"}},
		needs: map[string][]places.NeedPlace{"food": {{ID: "o1", Name: "Café", DistanceM: 300, Family: "pl"}}}}
	k := key("commercial", nbAll, "e1", "p1", "t1", "pl", "r1")
	a, err := nbService(f).Nearby(context.Background(), k, nbQuery("fuel", "charging", "parking", "station", "junction", "food", "police"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Fuel == nil || a.Charging == nil || a.Parking == nil || a.Stations == nil || len(a.Junctions.Items) != 1 || len(a.Parts) != 6 {
		t.Fatalf("answer %+v", a)
	}
	// The junctions are read of the roads and the places families; the
	// node's and its road's are credited.
	if !slices.Contains(f.gotJFams, "pl") || !slices.Contains(f.gotJFams, "r1") || a.JunctionsFrom != places.JunctionsFromOSM ||
		!slices.Equal(names(a.Junctions.Families), []string{"pl", "r1"}) {
		t.Errorf("junctions read of %v from %q, credited %v", f.gotJFams, a.JunctionsFrom, names(a.Junctions.Families))
	}
	if f.gotFuel.Fuel != "e5" || f.gotFuel.Sort != "price" || f.gotFuel.Limit != 3 || f.gotFuel.Window != time.Hour {
		t.Errorf("fuel asked %+v", f.gotFuel)
	}
	if p := a.Needs["food"]; len(p.Places) != 1 || p.RadiusM != 5000 || len(a.Needs["police"].Places) != 0 || len(f.gotNeeds.Needs) != 2 {
		t.Errorf("needs %+v asked %+v", a.Needs, f.gotNeeds)
	}
	for _, p := range a.Parts {
		if p.Status != features.StatusOK {
			t.Errorf("part %+v", p)
		}
	}
	if !a.Meta.At.Equal(now) || len(a.Meta.Families) == 0 {
		t.Errorf("meta %+v", a.Meta)
	}
}

func TestNearbyPartsStandAlone(t *testing.T) {
	req.Covers(t, "FR-PLACE-001", "SEC-030")
	k := key("commercial", []access.Scope{"places", "roads"}, "pl", "r1")
	a, err := nbService(&nbFake{nErr: errStore}).Nearby(context.Background(), k, nbQuery("station", "junction", "food"))
	if err != nil {
		t.Fatal(err)
	}
	want := []features.Status{features.StatusNotPermitted, features.StatusOK, features.StatusUnavailable}
	for i, p := range a.Parts {
		if p.Status != want[i] {
			t.Errorf("part %d: %+v", i, p)
		}
	}
	if a.Stations != nil || a.Needs != nil {
		t.Errorf("a part not ok is absent: %+v", a)
	}
	k = key("commercial", []access.Scope{"transit"}, "t1")
	if _, err := nbService(&nbFake{}).Nearby(context.Background(), k, nbQuery("fuel", "food")); !errors.Is(err, features.ErrNoPart) {
		t.Errorf("no part ok: %v", err)
	}
	if _, err := nbService(&nbFake{jErr: errStore}).Nearby(context.Background(), key("commercial", nbAll, "r1"), nbQuery("junction")); !errors.Is(err, features.ErrNoPart) {
		t.Errorf("a junction read failing: %v", err)
	}
}

func TestNearbyRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	k := key("commercial", nbAll, "e1", "p1", "t1", "pl", "px", "r1", "rx")
	bad := []places.Junction{{Family: "rx"}}
	if _, err := nbService(&nbFake{junctions: bad}).Nearby(context.Background(), k, nbQuery("junction")); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("junction post-check: %v", err)
	}
	needs := map[string][]places.NeedPlace{"food": {{Family: "px"}}}
	if _, err := nbService(&nbFake{needs: needs}).Nearby(context.Background(), k, nbQuery("food")); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("needs post-check: %v", err)
	}
	f := &nbFake{fake: &fake{provErr: errStore, provErrOnCall: 2}}
	if _, err := nbService(f).Nearby(context.Background(), k, nbQuery("station")); !errors.Is(err, errStore) {
		t.Errorf("provenance: %v", err)
	}
}

func (f *nbFake) FuelStation(context.Context, string, []string, time.Time, time.Duration) (energy.FuelStationPage, error) {
	return energy.FuelStationPage{}, energy.ErrNotFound
}
