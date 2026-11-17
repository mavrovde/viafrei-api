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
