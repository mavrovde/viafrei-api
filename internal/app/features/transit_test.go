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

// trRegister: two accessibility families, two transit families, one
// consumer only.
var trRegister = []access.Terms{
	{Family: "a1", Domain: "accessibility", UseClasses: all, Version: "v1"},
	{Family: "a2", Domain: "accessibility", UseClasses: all, Version: "v1"},
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
	stopBoards  []transit.StopBoard
	stopErr     error
	gotStops    transit.StopBoardsQuery
	gotStopFams []string
	station     transit.Station
	stErr       error
	calls       []transit.Departure
	depErr      error
	gotBoard    transit.DepartureQuery
	facilities  []transit.Facility
	facErr      error
	gotFacFams  []string
	reach       transit.Reach
	reachErr    error
}

func (f *trFake) TimetableReach(context.Context) (transit.Reach, error) {
	return f.reach, f.reachErr
}

func (f *trFake) Facilities(_ context.Context, _ string, fams []string, _ time.Time) ([]transit.Facility, error) {
	f.gotFacFams = fams
	return f.facilities, f.facErr
}

func (f *trFake) Station(_ context.Context, id string, fams []string) (transit.Station, error) {
	f.gotFamilies = fams
	return f.station, f.stErr
}

func (f *trFake) StopBoards(_ context.Context, q transit.StopBoardsQuery, fams []string) ([]transit.StopBoard, error) {
	f.gotStops, f.gotStopFams = q, fams
	return f.stopBoards, f.stopErr
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

func TestANameSeveralStationsAnswerCreditsTheirPlacement(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	p, g := "p1", "g1"
	two := []transit.Station{{ID: "a", Family: "t1", PlacedBy: &p}, {ID: "b", Family: "t1"}}
	consumer := key("commercial", []access.Scope{"transit"}, "t1")
	for _, c := range []struct {
		name     string
		stations []transit.Station
		q        transit.Query
		want     []string
	}{
		{"a name two stations answer", two, transit.Query{Text: "nam", Limit: 5}, []string{"p1", "t1"}},
		{"a name one station answers", two[:1], transit.Query{Text: "nam", Limit: 5}, []string{"t1"}},
		{"a position", two, transit.Query{Limit: 5}, []string{"t1"}},
		// One station found in the town the text names: its district
		// placed it there.
		{"one station in the town", []transit.Station{{ID: "a", Family: "t1", PlacedBy: &p, InTown: true}}, transit.Query{Text: "town nam", Limit: 5}, []string{"p1", "t1"}},
		// A town a Gemeinde of the gazetteer named: the gazetteer is credited.
		{"a town the gazetteer named", []transit.Station{{ID: "a", Family: "t1", PlacedBy: &p, InTown: true, TownBy: &g}}, transit.Query{Text: "town nam", Limit: 5}, []string{"g1", "p1", "t1"}},
	} {
		a, err := trService(&trFake{stations: c.stations}).Stations(context.Background(), consumer, c.q)
		if err != nil || !slices.Equal(names(a.Families), c.want) {
			t.Errorf("%s: %v %v", c.name, names(a.Families), err)
		}
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
	f := &trFake{station: transit.Station{ID: "s", Family: "t1"}, reach: transit.Reach{Until: &at}, calls: []transit.Departure{
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
		b.MatchedAll != 3 || b.Dropped[0] != 1 || !b.At.Equal(now) || b.Reach.Until != &at || !slices.Equal(names(b.Families), []string{"t1"}) {
		t.Errorf("board %+v", b)
	}
}

// A board answers the Trip Updates' family whatever domain the register
// files it under (its calls carry that family beside the timetable's), still
// under the licence gate.
func TestABoardAnswersTheTripUpdatesFamily(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	at := now.Add(time.Minute)
	f := &trFake{station: transit.Station{ID: "s", Family: "t1", StopIDs: []string{"s", "s:1"}}, calls: []transit.Departure{
		{JourneyID: "a", StopPointID: "s:1", PlannedAt: at, Mode: "bus", RouteType: 3, Family: "t1", Families: []string{"t1", transit.TripUpdatesFamily}},
	}}
	svc := trService(f)
	f.reg = append(slices.Clone(trRegister), access.Terms{Family: transit.TripUpdatesFamily, Domain: "roads", UseClasses: all, Version: "v1"})
	b, err := svc.Departures(context.Background(), key("commercial", []access.Scope{"transit"}, "t1", transit.TripUpdatesFamily),
		transit.DepartureQuery{StationID: "s", From: now, Window: time.Hour, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFamilies, []string{"t1", transit.TripUpdatesFamily}) || len(b.Items) != 1 {
		t.Errorf("asked %+v of %v: %+v", f.gotBoard, f.gotFamilies, b)
	}
	// Filed under transit, it is answered once.
	f.reg = append(slices.Clone(trRegister), access.Terms{Family: transit.TripUpdatesFamily, Domain: "transit", UseClasses: all, Version: "v1"})
	if _, err := svc.Departures(context.Background(), key("commercial", []access.Scope{"transit"}, "t1", transit.TripUpdatesFamily),
		transit.DepartureQuery{StationID: "s", From: now, Window: time.Hour, Limit: 5}); err != nil || !slices.Equal(f.gotFamilies, []string{"t1", transit.TripUpdatesFamily}) {
		t.Errorf("filed under transit: %v %v", f.gotFamilies, err)
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
		"reach":      {&trFake{station: st, reachErr: errStore}, full, errStore},
		"post-check": {&trFake{station: st, calls: []transit.Departure{{Families: []string{"t2"}}}}, full, features.ErrGateViolation},
		"provenance": {&trFake{fake: &fake{provErr: errStore}, station: st}, full, errStore},
	} {
		if _, err := trService(c.f).Departures(context.Background(), c.k, transit.DepartureQuery{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

// The stops around a position are read of the board's families, gated on
// every stop's family and every departure's, and credited.
func TestStopBoardsAreGatedAndAttributed(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002", "SEC-030")
	f := &trFake{stopBoards: []transit.StopBoard{{Stop: transit.NearStop{StopNear: transit.StopNear{Root: "1:a", Family: "t1"}},
		Board: transit.BoardAnswer{Rows: []transit.Departure{{Families: []string{"t1"}}}}}}}
	q := transit.StopBoardsQuery{RadiusM: 5000, Limit: 8, PerStop: 4}
	a, err := trService(f).StopBoards(context.Background(), key("commercial", []access.Scope{"transit"}, "t1", "t2"), q)
	if err != nil || f.gotStops.Limit != 8 || !slices.Equal(f.gotStopFams, []string{"t1"}) || len(a.Items) != 1 || !a.Partial ||
		!a.At.Equal(now) || !slices.Equal(names(a.Families), []string{"t1"}) {
		t.Errorf("%+v %v of %v: %v", a, f.gotStops, f.gotStopFams, err)
	}
	full := key("commercial", []access.Scope{"transit"}, "t1")
	for name, c := range map[string]struct {
		f    *trFake
		k    access.Record
		want error
	}{
		"register":   {&trFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&trFake{}, key("commercial", []access.Scope{"transit"}, "t2"), access.ErrNonePermitted},
		"read":       {&trFake{stopErr: errStore}, full, errStore},
		"post-check": {&trFake{stopBoards: []transit.StopBoard{{Stop: transit.NearStop{StopNear: transit.StopNear{Family: "t2"}}}}}, full, features.ErrGateViolation},
		"provenance": {&trFake{fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, err := trService(c.f).StopBoards(context.Background(), c.k, q); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestFacilitiesAreListedGatedAndAttributed(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-003", "SEC-030")
	f := &trFake{station: transit.Station{ID: "s", Family: "t1"}, facilities: []transit.Facility{
		{ID: "x", State: "active", Family: "a1"}, {ID: "y", State: "inactive", Family: "a1", Listing: "a2"},
	}}
	a, err := trService(f).Facilities(context.Background(), key("commercial", []access.Scope{"transit"}, "t1", "t2", "a1", "a2"), transit.FacilityQuery{StationID: "s", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	// the unit's listing family is credited beside its state's
	if !slices.Equal(f.gotFacFams, []string{"a1", "a2"}) || len(a.Items) != 1 || a.Items[0].ID != "y" || !a.HasMore || !a.Partial ||
		a.Active != 1 || a.Inactive != 1 || !slices.Equal(names(a.Families), []string{"a1", "a2", "t1"}) {
		t.Errorf("answer %+v", a)
	}
	full := key("commercial", []access.Scope{"transit"}, "t1", "a1")
	st := transit.Station{ID: "s", Family: "t1"}
	for name, c := range map[string]struct {
		f    *trFake
		k    access.Record
		want error
	}{
		"register":   {&trFake{fake: &fake{regErr: errStore}}, full, errStore},
		"no transit": {&trFake{}, key("commercial", []access.Scope{"transit"}, "a1"), access.ErrNonePermitted},
		"no access":  {&trFake{}, key("commercial", []access.Scope{"transit"}, "t1"), access.ErrNonePermitted},
		"station":    {&trFake{stErr: transit.ErrNotFound}, full, transit.ErrNotFound},
		"read":       {&trFake{station: st, facErr: errStore}, full, errStore},
		"post-check": {&trFake{station: st, facilities: []transit.Facility{{Family: "t2"}}}, full, features.ErrGateViolation},
		"provenance": {&trFake{fake: &fake{provErr: errStore}, station: st}, full, errStore},
	} {
		if _, err := trService(c.f).Facilities(context.Background(), c.k, transit.FacilityQuery{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

// A railway station is named as DB's register names it where the key may
// read the register, which is then credited; else, and off railway, the
// stop's name stands.
func TestARailwayStationIsNamedAsTheRegisterNamesIt(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	reg, other, nm := "t1", "t2", "Frankfurt (Main) Hbf"
	k := key("commercial", []access.Scope{"transit"}, "t1")
	for _, c := range []struct {
		name    string
		by      *string
		railway bool
		want    string
	}{
		{"railway", &reg, true, nm},
		{"register not readable", &other, true, "Frankfurt Hbf"},
		{"no register family", nil, true, "Frankfurt Hbf"},
		{"not railway", &reg, false, "Frankfurt Hbf"},
	} {
		f := &trFake{stations: []transit.Station{{ID: "a", Name: "Frankfurt Hbf", Family: "t1", RegisterName: &nm, RegisterBy: c.by}}}
		a, err := trService(f).Stations(context.Background(), k, transit.Query{Railway: c.railway, Limit: 5})
		if err != nil || a.Items[0].Name != c.want {
			t.Errorf("%s: %v %v", c.name, a.Items, err)
		}
	}
}
