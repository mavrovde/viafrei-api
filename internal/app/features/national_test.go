package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/national"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

var natRegister = []access.Terms{
	{Family: "r1", Domain: "roads", UseClasses: all, Version: "v1"},
	{Family: "e1", Domain: "energy", UseClasses: all, Version: "v1"},
	{Family: "w1", Domain: "weather", UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
}

type natFake struct {
	*fake
	rows    map[string][]national.Row
	err     error
	asked   []string
	gotWin  time.Duration
	gotFams map[string][]string
}

func (f *natFake) National(_ context.Context, d string, fams []string, _ time.Time, win time.Duration) ([]national.Row, error) {
	f.asked, f.gotWin = append(f.asked, d), win
	if f.gotFams == nil {
		f.gotFams = map[string][]string{}
	}
	f.gotFams[d] = fams
	return f.rows[d], f.err
}

func natService(f *natFake) *features.Service {
	f.fake = &fake{reg: natRegister}
	return features.New(features.Repositories{Register: f, National: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestNationalReadsEveryScopedDomain(t *testing.T) {
	f := &natFake{rows: map[string][]national.Row{
		"energy": {{Section: national.SectionFuel, Key: "e10", Family: "e1", N: 2}},
		"roads":  {{Section: national.SectionSituations, Key: "closure", Family: "r1", N: 3}},
	}}
	s := natService(f)
	k := key("commercial", []access.Scope{"roads", "energy", "weather"}, "r1", "e1", "w1")
	a, err := s.National(context.Background(), k, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Answer order is by section, whatever order the domains were read in.
	if len(a.Rows) != 2 || a.Rows[0].Section != national.SectionSituations || !a.Meta.Partial || f.gotWin != time.Hour {
		t.Fatalf("rows %+v partial %v", a.Rows, a.Meta.Partial)
	}
	if !slices.Equal(f.asked, []string{"roads", "energy"}) {
		t.Errorf("asked %v: weather is consumer-only, parking and transit not scoped", f.asked)
	}
	if !slices.Equal(names(a.Meta.Families), []string{"e1", "r1"}) {
		t.Errorf("families %v", names(a.Meta.Families))
	}
}

func TestNationalRefusals(t *testing.T) {
	boom := errors.New("boom")
	ctx := context.Background()
	f := &natFake{}
	s := natService(f)
	if _, err := s.National(ctx, key("commercial", []access.Scope{"places"}, "r1"), time.Hour); !errors.Is(err, access.ErrNonePermitted) {
		t.Errorf("no scoped domain: %v", err)
	}
	if _, err := s.National(ctx, key("commercial", []access.Scope{"weather"}, "w1"), time.Hour); !errors.Is(err, access.ErrNonePermitted) {
		t.Errorf("every domain withheld: %v", err)
	}
	f.err = boom
	if _, err := s.National(ctx, key("commercial", []access.Scope{"roads"}, "r1"), time.Hour); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
	f.err, f.rows = nil, map[string][]national.Row{"roads": {{Section: national.SectionStates, Family: "zz"}}}
	if _, err := s.National(ctx, key("commercial", []access.Scope{"roads"}, "r1"), time.Hour); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("a foreign family must abort: %v", err)
	}
	f.rows = nil
	f.fake.provErr = boom
	if _, err := s.National(ctx, key("commercial", []access.Scope{"roads"}, "r1"), time.Hour); !errors.Is(err, boom) {
		t.Errorf("provenance error: %v", err)
	}
	f.fake.provErr, f.fake.regErr = nil, boom
	if _, err := s.National(ctx, key("commercial", []access.Scope{"roads"}, "r1"), time.Hour); !errors.Is(err, boom) {
		t.Errorf("register error: %v", err)
	}
}

type afFake struct {
	*fake
	v   []transit.Affected
	err error
}

func (f *afFake) AffectedStations(context.Context, []string, time.Time, int) ([]transit.Affected, error) {
	return f.v, f.err
}

func TestAffectedStationsCreditEveryFigure(t *testing.T) {
	ctx := context.Background()
	f := &afFake{fake: &fake{reg: []access.Terms{{Family: "t1", Domain: "transit", UseClasses: all, Version: "v1"},
		{Family: "t2", Domain: "transit", UseClasses: all, Version: "v1"}, {Family: "l1", Domain: "accessibility", UseClasses: all, Version: "v1"}}},
		v: []transit.Affected{{Station: transit.Station{ID: "a", Family: "t1"}, Families: []string{"t1", "t2", "l1", "t1"}}}}
	s := features.New(features.Repositories{Register: f, Affected: f}, features.Settings{Language: "en"}, func() time.Time { return now })
	v, a, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit", "accessibility"}, "t1", "t2", "l1"), 5)
	if err != nil || len(v) != 1 || a.Partial || !slices.Equal(names(a.Families), []string{"l1", "t1", "t2"}) {
		t.Fatalf("%+v %+v %v", v, a, err)
	}
	// A family the key may not read in the answer is a gate violation.
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1", "t2"), 5); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("lifts without the scope: %v", err)
	}
	f.v = []transit.Affected{{Station: transit.Station{ID: "a", Family: "t1"}, Families: []string{"t1"}}}
	if _, a, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1"), 5); err != nil || !a.Partial {
		t.Errorf("without the scope accessibility the answer is partial: %+v %v", a, err)
	}
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "l1"), 5); !errors.Is(err, access.ErrNonePermitted) {
		t.Errorf("no transit family: %v", err)
	}
	boom := errors.New("boom")
	f.err = boom
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1"), 5); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
	f.err, f.fake.provErr = nil, boom
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1"), 5); !errors.Is(err, boom) {
		t.Errorf("provenance error: %v", err)
	}
	f.fake.provErr, f.fake.regErr = nil, boom
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1"), 5); !errors.Is(err, boom) {
		t.Errorf("register error: %v", err)
	}
}

type covFake struct {
	*fake
	c   places.Coverage
	err error
}

func (f *covFake) PlacesCoverage(context.Context, []string) (places.Coverage, error) {
	return f.c, f.err
}

func TestPlacesCoverageCreditsItsFamilies(t *testing.T) {
	f := &covFake{fake: &fake{reg: []access.Terms{{Family: "g1", Domain: "places", UseClasses: all, Version: "v1"}}},
		c: places.Coverage{Kinds: []places.KindCount{{Family: "g1", Kind: "city", Rows: 3}}}}
	s := features.New(features.Repositories{Register: f, Coverage: f}, features.Settings{Language: "en"}, func() time.Time { return now })
	c, a, err := s.PlacesCoverage(context.Background(), key("commercial", []access.Scope{"places"}, "g1"))
	if err != nil || len(c.Kinds) != 1 || !slices.Equal(names(a.Families), []string{"g1"}) {
		t.Fatalf("%+v %+v %v", c, a, err)
	}
	f.err = errors.New("boom")
	if _, _, err := s.PlacesCoverage(context.Background(), key("commercial", []access.Scope{"places"}, "g1")); err == nil {
		t.Error("a read error must surface")
	}
}
