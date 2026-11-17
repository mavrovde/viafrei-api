package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// cellRegister: one weather family and one places family.
var cellRegister = []access.Terms{
	{Family: "w1", Domain: "weather", UseClasses: all, Version: "v1"},
	{Family: "pl", Domain: "places", UseClasses: all, Version: "v1"},
}

func cellFake(place places.Place, cells map[string]weather.Cell) *wxFake {
	return &wxFake{fake: &fake{reg: cellRegister, place: place}, cells: cells}
}

var cellKey = key("commercial", []access.Scope{"weather", "places"}, "w1", "pl")

func TestWeatherCellKeysAsThePlatform(t *testing.T) {
	name := "Stadt Kiel"
	warned := weather.Cell{Key: "801002000", Name: &name, Warnings: []weather.Warning{{ID: "a", Family: "w1", Level: 2}}}
	kiel := places.Place{ID: "p1", Name: "Kiel", Kind: "municipality", Location: geo.Point{Lat: 54.32, Lon: 10.13}, Family: "pl"}
	ctx := context.Background()

	// a position: the cell it lies in
	f := cellFake(places.Place{}, map[string]weather.Cell{"@": warned})
	at := geo.Point{Lat: 54.3, Lon: 10.1}
	a, err := wxService(f).WeatherCell(ctx, cellKey, "54.3,10.1", &at, 2)
	if err != nil || a.Key != "801002000" || len(a.Warnings) != 1 || a.Place != nil || f.gotCells[0].MinLevel != 2 || !slices.Equal(names(a.Meta.Families), []string{"w1"}) {
		t.Fatalf("position: %+v %v %+v", a, err, f.gotCells)
	}

	// a cell id: as given, no place asked
	f = cellFake(kiel, map[string]weather.Cell{"801002000": warned})
	if a, err = wxService(f).WeatherCell(ctx, cellKey, "801002000", nil, 3); err != nil || a.Key != "801002000" || f.gotCells[0].At != nil || f.gotFamilies[0] != "w1" {
		t.Fatalf("cell id: %+v %v", a, err)
	}

	// a place name: the place's point, attributed to the places family too
	f = cellFake(kiel, map[string]weather.Cell{"@": warned})
	if a, err = wxService(f).WeatherCell(ctx, cellKey, "Kiel", nil, 2); err != nil || a.Place == nil || a.Place.Name != "Kiel" || *f.gotCells[0].At != kiel.Location ||
		!slices.Equal(names(a.Meta.Families), []string{"pl", "w1"}) {
		t.Fatalf("place: %+v %v", a, err)
	}

	// a place no cell is held at, but a cell of that name: the name
	f = cellFake(kiel, map[string]weather.Cell{"Kiel": {Key: "Kiel", Name: &name}})
	if a, err = wxService(f).WeatherCell(ctx, cellKey, "Kiel", nil, 2); err != nil || a.Key != "Kiel" || len(f.gotCells) != 2 || f.gotCells[1].At != nil {
		t.Fatalf("place without cell, named cell: %+v %v %+v", a, err, f.gotCells)
	}

	// neither: no cell
	f = cellFake(kiel, nil)
	if _, err = wxService(f).WeatherCell(ctx, cellKey, "Kiel", nil, 2); !errors.Is(err, weather.ErrNoCell) {
		t.Fatalf("no cell: %v", err)
	}

	// an area: refused, unless a cell is held under its name
	land := places.Place{ID: "p2", Name: "Schleswig-Holstein", Kind: "state", Location: kiel.Location, Family: "pl"}
	f = cellFake(land, nil)
	if _, err = wxService(f).WeatherCell(ctx, cellKey, "Schleswig-Holstein", nil, 2); !errors.Is(err, features.ErrAreaNotCell) || f.gotCells[0].At != nil {
		t.Fatalf("area: %v", err)
	}
	f = cellFake(places.Place{ID: "p3", Name: "Kreis Plön", Kind: "district", Family: "pl"}, map[string]weather.Cell{"Kreis Plön": {Key: "Kreis Plön"}})
	if a, err = wxService(f).WeatherCell(ctx, cellKey, "Kreis Plön", nil, 2); err != nil || a.Key != "Kreis Plön" {
		t.Fatalf("area held as a cell name: %+v %v", a, err)
	}

	// no places scope: the name is only a cell name
	f = cellFake(kiel, map[string]weather.Cell{"Kiel": {Key: "Kiel"}})
	if a, err = wxService(f).WeatherCell(ctx, key("commercial", []access.Scope{"weather"}, "w1", "pl"), "Kiel", nil, 2); err != nil || a.Place != nil || f.gotCells[0].At != nil {
		t.Fatalf("no places scope: %+v %v", a, err)
	}
}

func TestWeatherCellFailsAsItsReads(t *testing.T) {
	ctx := context.Background()
	f := cellFake(places.Place{}, nil)
	f.plErr = errStore
	f.place = places.Place{ID: "x"}
	if _, err := wxService(f).WeatherCell(ctx, cellKey, "Kiel", nil, 2); !errors.Is(err, errStore) {
		t.Fatalf("places down: %v", err)
	}
	f = cellFake(places.Place{}, nil)
	f.regErr = errStore
	if _, err := wxService(f).WeatherCell(ctx, cellKey, "Kiel", nil, 2); !errors.Is(err, errStore) {
		t.Fatalf("register down: %v", err)
	}
	if _, err := wxService(f).WeatherCell(ctx, cellKey, "801002000", nil, 2); !errors.Is(err, errStore) {
		t.Fatalf("register down, cell id: %v", err)
	}
	f = cellFake(places.Place{}, map[string]weather.Cell{"801002000": {Key: "801002000"}})
	if _, err := wxService(f).WeatherCell(ctx, key("commercial", []access.Scope{"weather", "places"}, "pl"), "801002000", nil, 2); !errors.Is(err, access.ErrNonePermitted) {
		t.Fatalf("no weather family: %v", err)
	}
}
