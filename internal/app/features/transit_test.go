package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// trRegister: two transit families, one consumer only.
var trRegister = []access.Terms{
	{Family: "t1", Domain: "transit", UseClasses: all, Version: "v1"},
	{Family: "t2", Domain: "transit", UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
}

type trFake struct {
	*fake
	stations    []transit.Station
	more        bool
	readErr     error
	gotQuery    transit.Query
	gotFamilies []string
	station     transit.Station
	stErr       error
	calls       []transit.Departure
	depErr      error
	gotBoard    transit.DepartureQuery
}

func (f *trFake) Station(_ context.Context, id string, fams []string) (transit.Station, error) {
	f.gotFamilies = fams
	return f.station, f.stErr
}

func (f *trFake) Departures(_ context.Context, q transit.DepartureQuery, _ []string) ([]transit.Departure, error) {
	f.gotBoard = q
	return f.calls, f.depErr
}

func (f *trFake) Stations(_ context.Context, q transit.Query, fams []string) ([]transit.Station, bool, error) {
	f.gotQuery, f.gotFamilies = q, fams
	return f.stations, f.more, f.readErr
}

func trService(f *trFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = trRegister
	return features.New(features.Repositories{Register: f, Transit: f},
		features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestStationsAreGatedAndAttributed(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001", "SEC-030")
	f := &trFake{stations: []transit.Station{{ID: "a", Family: "t1"}, {ID: "b", Family: "t1"}}, more: true}
	q := transit.Query{Text: "koeln", Limit: 8}
	a, err := trService(f).Stations(context.Background(), key("commercial", []access.Scope{"transit"}, "t1", "t2"), q)
	if err != nil {
		t.Fatal(err)
	}
	if f.gotQuery != q || !slices.Equal(f.gotFamilies, []string{"t1"}) {
		t.Errorf("asked %+v of %v", f.gotQuery, f.gotFamilies)
	}
	if !a.Partial || !a.HasMore || len(a.Items) != 2 || !a.At.Equal(now) || !slices.Equal(names(a.Families), []string{"t1"}) {
		t.Errorf("answer %+v", a)
	}
	f = &trFake{}
	a, err = trService(f).Stations(context.Background(), key("consumer", []access.Scope{"transit"}, "t1", "t2"), q)
	if err != nil || a.Partial || len(a.Items) != 0 || !slices.Equal(names(a.Families), []string{"t1", "t2"}) {
		t.Errorf("an empty answer names the consulted families: %+v %v", a, err)
	}
}

func TestStationsRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"transit"}, "t1")
	for name, c := range map[string]struct {
		f    *trFake
		k    access.Record
		want error
	}{
		"register":   {&trFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&trFake{}, key("commercial", []access.Scope{"transit"}, "t2"), access.ErrNonePermitted},
		"read":       {&trFake{readErr: errStore}, full, errStore},
		"post-check": {&trFake{stations: []transit.Station{{Family: "t2"}}}, full, features.ErrGateViolation},
		"provenance": {&trFake{fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, err := trService(c.f).Stations(context.Background(), c.k, transit.Query{}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestDeparturesAreTheBoardGatedAndAttributed(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002", "SEC-030")
	at := now.Add(time.Minute)
	f := &trFake{station: transit.Station{ID: "s", Family: "t1"}, calls: []transit.Departure{
		{JourneyID: "a", PlannedAt: at, Mode: "bus", RouteType: 3, Family: "t1", Families: []string{"t1"}},
		{JourneyID: "b", PlannedAt: at.Add(time.Minute), Mode: "bus", RouteType: 3, Family: "t1", Families: []string{"t1"}},
		{JourneyID: "c", StopPointID: "p2", PlannedAt: at, Mode: "tram", RouteType: 0, Family: "t1", Families: []string{"t1"}},
	}}
	q := transit.DepartureQuery{StationID: "s", From: now, Window: time.Hour, Modes: []transit.Mode{"bus"}, Limit: 1}
	b, err := trService(f).Departures(context.Background(), key("commercial", []access.Scope{"transit"}, "t1", "t2"), q)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFamilies, []string{"t1"}) || f.gotBoard.StationID != "s" {
		t.Errorf("asked %+v of %v", f.gotBoard, f.gotFamilies)
	}
	if b.Station.ID != "s" || len(b.Items) != 1 || b.Items[0].JourneyID != "a" || !b.HasMore || !b.Partial || b.Matched != 2 ||
		b.MatchedAll != 3 || b.Dropped[0] != 1 || !b.At.Equal(now) || !slices.Equal(names(b.Families), []string{"t1"}) {
		t.Errorf("board %+v", b)
	}
}

func TestDeparturesRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"transit"}, "t1")
	st := transit.Station{ID: "s", Family: "t1"}
	for name, c := range map[string]struct {
		f    *trFake
		k    access.Record
		want error
	}{
		"register":   {&trFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&trFake{}, key("commercial", []access.Scope{"transit"}, "t2"), access.ErrNonePermitted},
		"station":    {&trFake{stErr: transit.ErrNotFound}, full, transit.ErrNotFound},
		"read":       {&trFake{station: st, depErr: errStore}, full, errStore},
		"post-check": {&trFake{station: st, calls: []transit.Departure{{Families: []string{"t2"}}}}, full, features.ErrGateViolation},
		"provenance": {&trFake{fake: &fake{provErr: errStore}, station: st}, full, errStore},
	} {
		if _, err := trService(c.f).Departures(context.Background(), c.k, transit.DepartureQuery{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
