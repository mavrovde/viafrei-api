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
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// cpTerms are an operator source, the register source and a status source.
var cpTerms = []access.Terms{
	{Family: "op", Domain: "energy", UseClasses: all, Version: "v1"},
	{Family: "lsr", Domain: "energy", UseClasses: all, Version: "v1"},
	{Family: "st", Domain: "energy", UseClasses: all, Version: "v1"},
}

var (
	cpCentre = geo.Point{Lat: 52.52, Lon: 13.405}
	twin100  = 100
	cpReg    = energy.SourceRegister{
		Sites:  []energy.SiteSource{{Key: "op", Rank: 1}, {Key: "lsr", Rank: 9, TwinRadiusM: &twin100}},
		Status: []energy.StatusSource{{Key: "st", StaleAfter: 15 * time.Minute, StatementMaxAge: time.Hour}},
	}
)

type cpFake struct {
	*fake
	recs                                               []energy.PlaceRecord
	atTown                                             []int
	hits                                               []energy.AddressHit
	elsewhere                                          map[string]energy.Elsewhere
	regErr, recErr, liveErr, townErr, addrErr, elseErr error
	gotFams                                            []string
	gotRead                                            energy.PlaceRead
	gotTown, gotElsewhere                              []energy.PlaceRecord
}

func (f *cpFake) ChargingRegister(_ context.Context, fams []string) (energy.SourceRegister, error) {
	f.gotFams = fams
	return cpReg, f.regErr
}

func (f *cpFake) ChargingPlaceRecords(_ context.Context, q energy.PlaceRead, _ energy.SourceRegister, _ time.Time) ([]energy.PlaceRecord, error) {
	f.gotRead = q
	return f.recs, f.recErr
}

func (f *cpFake) ChargingLiveness(context.Context, energy.SourceRegister, time.Time) ([]energy.SourceLiveness, error) {
	return nil, f.liveErr
}

func (f *cpFake) ChargingAtTownPoint(_ context.Context, recs []energy.PlaceRecord) ([]int, error) {
	f.gotTown = recs
	return f.atTown, f.townErr
}

func (f *cpFake) ChargingAddresses(context.Context, energy.PlaceRecord) ([]energy.AddressHit, error) {
	return f.hits, f.addrErr
}

func (f *cpFake) ChargingRegisterElsewhere(_ context.Context, recs []energy.PlaceRecord, _ energy.SourceRegister, _ time.Time) (map[string]energy.Elsewhere, error) {
	f.gotElsewhere = recs
	return f.elsewhere, f.elseErr
}

func cpService(f *cpFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = cpTerms
	return features.New(features.Repositories{Register: f, Charging: f, ChargingPlaces: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func cpStr(s string) *string { return &s }

// cpRecords are an operator's site with a free point of the status source, a
// register row filed at its town's point, and a register row elsewhere.
func cpRecords() []energy.PlaceRecord {
	observed := now.Add(-time.Minute)
	return []energy.PlaceRecord{
		{Source: "op", SiteID: "a", Name: cpStr("Q-Park"), Location: cpCentre, DistanceM: 10,
			Points: []energy.PlacePoint{{PointKey: "p1", StatusSource: cpStr("st"), Status: cpStr("available"), ObservedAt: &observed}}},
		{Source: "lsr", SiteID: "r", Street: cpStr("Hauptstraße"), HouseNumber: cpStr("1"), City: cpStr("Berlin"), Location: cpCentre,
			DistanceM: 20, SourceVersion: cpStr("2026-09-01")},
		{Source: "lsr", SiteID: "q", Name: cpStr("Ladepark Süd"), Location: geo.Point{Lat: 52.53, Lon: 13.405}, DistanceM: 1100},
	}
}

func TestChargingPlacesFoldsReadsAndCredits(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001", "SEC-030")
	k := key("commercial", []access.Scope{"energy"}, "op", "lsr", "st")
	moved := geo.Point{Lat: 52.538, Lon: 13.405}
	f := &cpFake{recs: cpRecords(), atTown: []int{0}, hits: []energy.AddressHit{{Street: "Hauptstraße", HouseNumber: "1", Location: moved,
		LiteralScore: 1, ByPostcode: true}}, elsewhere: map[string]energy.Elsewhere{}}
	a, err := cpService(f).ChargingPlaces(context.Background(), k, features.PlaceQuery{At: cpCentre, RadiusM: 5000, Limit: 2, MinKw: 50, Connector: "ccs2"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFams, []string{"lsr", "op", "st"}) || f.gotRead.Scan != 13 || *f.gotRead.MinKw != 50 || f.gotRead.Connectors[0] != "iec62196T2COMBO" {
		t.Errorf("the read: %v %+v", f.gotFams, f.gotRead)
	}
	if len(f.gotTown) != 1 || f.gotTown[0].SiteID != "r" {
		t.Errorf("the town point read: %v", f.gotTown)
	}
	if len(a.Items) != 2 || a.Items[0].Members[0].Record.SiteID != "a" || a.Items[1].Members[0].Record.SiteID != "q" || !a.HasMore {
		t.Fatalf("the places: %+v", a.Items)
	}
	if a.Lang != "en" || a.RegisterReleasedOn != nil || a.InRadius != nil || a.Dropped != nil || !a.At.Equal(now) {
		t.Errorf("the answer: %+v", a)
	}
	var fams []string
	for _, c := range a.Families {
		fams = append(fams, c.Source.Family)
	}
	if !slices.Equal(fams, []string{"lsr", "op", "st"}) {
		t.Errorf("credits %v", fams)
	}
	// The moved register row lies 2 km off, the third line: past the limit.
	if len(f.gotElsewhere) != 2 {
		t.Errorf("the elsewhere read: %v", f.gotElsewhere)
	}

	// Only available: a free point alone answers; the rest are counted.
	f = &cpFake{recs: cpRecords()}
	a, err = cpService(f).ChargingPlaces(context.Background(), k, features.PlaceQuery{At: cpCentre, RadiusM: 5000, Limit: 5, OnlyAvailable: true})
	if err != nil || f.gotRead.Scan != 105 || f.gotRead.MinKw != nil || f.gotRead.Connectors != nil {
		t.Fatalf("only available: %v %+v", err, f.gotRead)
	}
	if len(a.Items) != 1 || a.Dropped == nil || *a.Dropped != (energy.Dropped{NoStatus: 2}) {
		t.Errorf("only available: %+v %+v", a.Items, a.Dropped)
	}

	// The register's release, when a register row is listed.
	f = &cpFake{recs: cpRecords()[1:2]}
	if a, err = cpService(f).ChargingPlaces(context.Background(), k, features.PlaceQuery{At: cpCentre, RadiusM: 5000, Limit: 5}); err != nil ||
		a.RegisterReleasedOn == nil || *a.RegisterReleasedOn != "2026-09-01" {
		t.Errorf("the release: %+v %v", a, err)
	}

	// None: how many sites the circle holds.
	f = &cpFake{fake: &fake{inRadius: 7}}
	if a, err = cpService(f).ChargingPlaces(context.Background(), k, features.PlaceQuery{At: cpCentre, RadiusM: 5000, Limit: 5}); err != nil ||
		a.InRadius == nil || *a.InRadius != 7 || len(a.Items) != 0 || a.Items == nil {
		t.Errorf("none: %+v %v", a, err)
	}
}

func TestChargingPlacesStatusStates(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	k := key("commercial", []access.Scope{"energy"}, "op", "lsr", "st")
	recs := cpRecords()[:1]
	at := func(status string, age time.Duration) []energy.PlaceRecord {
		r := recs[0]
		observed := now.Add(-age)
		r.Points = []energy.PlacePoint{{StatusSource: cpStr("st"), Status: cpStr(status), ObservedAt: &observed}}
		return []energy.PlaceRecord{r}
	}
	for name, c := range map[string]struct {
		recs []energy.PlaceRecord
		want energy.Dropped
	}{
		"occupied": {at("occupied", time.Minute), energy.Dropped{NotFree: 1}},
		"stale":    {at("available", time.Hour), energy.Dropped{Stale: 1}},
	} {
		a, err := cpService(&cpFake{recs: c.recs}).ChargingPlaces(context.Background(), k, features.PlaceQuery{At: cpCentre, RadiusM: 5000, Limit: 5, OnlyAvailable: true})
		if err != nil || len(a.Items) != 0 || *a.Dropped != c.want {
			t.Errorf("%s: %+v %v", name, a.Dropped, err)
		}
	}
}

func TestChargingPlacesRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"energy"}, "op", "lsr", "st")
	town := func(f *cpFake) *cpFake { f.recs, f.atTown = cpRecords(), []int{0}; return f }
	for name, c := range map[string]struct {
		f    *cpFake
		k    access.Record
		want error
	}{
		"gate":       {&cpFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&cpFake{}, key("commercial", []access.Scope{"energy"}, "x"), access.ErrNonePermitted},
		"register":   {&cpFake{regErr: errStore}, full, errStore},
		"records":    {&cpFake{recErr: errStore}, full, errStore},
		"liveness":   {&cpFake{liveErr: errStore}, full, errStore},
		"town point": {&cpFake{recs: cpRecords(), townErr: errStore}, full, errStore},
		"addresses":  {town(&cpFake{addrErr: errStore}), full, errStore},
		"elsewhere":  {&cpFake{recs: cpRecords(), elseErr: errStore}, full, errStore},
		"in radius":  {&cpFake{fake: &fake{inRadiusErr: errStore}}, full, errStore},
		"provenance": {&cpFake{fake: &fake{provErr: errStore}, recs: cpRecords()}, full, errStore},
		"post-check": {&cpFake{recs: []energy.PlaceRecord{{Source: "zz", SiteID: "z"}}}, full, features.ErrGateViolation},
	} {
		if _, err := cpService(c.f).ChargingPlaces(context.Background(), c.k, features.PlaceQuery{At: cpCentre, RadiusM: 5000, Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestNearbyChargingByPlace(t *testing.T) {
	req.Covers(t, "FR-PLACE-001", "FR-ENERGY-001")
	k := key("commercial", []access.Scope{"energy", "places"}, "op", "lsr", "st")
	f := &cpFake{recs: cpRecords()[1:2]}
	q := features.NearbyQuery{At: places.NearbyQuery{At: cpCentre, RadiusM: 5000}, Categories: []string{"charging"}, ChargingByPlace: true}
	a, err := cpService(f).Nearby(context.Background(), k, q)
	if err != nil || a.Charging != nil || a.ChargingPlaces == nil || len(a.ChargingPlaces.Items) != 1 || f.gotRead.Scan != 3*energy.ScanPerLine+energy.MovedHeadroom {
		t.Fatalf("%+v %v", a, err)
	}
	if r := a.ChargingPlaces.RegisterReleasedOn; r == nil || *r != "2026-09-01" || len(a.Meta.Families) != 1 {
		t.Errorf("the register %v, credits %v", r, a.Meta.Families)
	}
	f = &cpFake{recErr: errStore}
	if a, _ := cpService(f).Nearby(context.Background(), k, q); a.ChargingPlaces != nil {
		t.Errorf("a failed read answers: %+v", a.ChargingPlaces)
	}
}
