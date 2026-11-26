package features_test

import (
	"context"
	"errors"
	"slices"
	"sync"
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
	mu      sync.Mutex
	rows    map[string][]national.Row
	err     error
	asked   []string
	gotWin  time.Duration
	gotFams map[string][]string
}

func (f *natFake) National(_ context.Context, d string, fams []string, _ time.Time, win time.Duration) ([]national.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

// barrierNat answers each domain only once every asked domain is being
// read, so a National that read them one after another never answers.
type barrierNat struct {
	*fake
	want    int
	mu      sync.Mutex
	arrived int
	all     chan struct{}
	errs    map[string]error
}

func (f *barrierNat) National(ctx context.Context, d string, _ []string, _ time.Time, _ time.Duration) ([]national.Row, error) {
	f.mu.Lock()
	if f.arrived++; f.arrived == f.want {
		close(f.all)
	}
	f.mu.Unlock()
	select {
	case <-f.all:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []national.Row{{Section: national.SectionSituations, Family: map[string]string{"roads": "r1", "energy": "e1"}[d]}}, f.errs[d]
}

// The domains are read at the same time, their rows kept in the domains'
// order, and the first failure in that order is the answer's.
func TestNationalReadsTheDomainsAtOnce(t *testing.T) {
	k := key("commercial", []access.Scope{"roads", "energy"}, "r1", "e1")
	read := func(errs map[string]error) (features.NationalAnswer, error) {
		f := &barrierNat{want: 2, all: make(chan struct{}), errs: errs}
		f.fake = &fake{reg: natRegister}
		svc := features.New(features.Repositories{Register: f, National: f}, features.Settings{Language: "en"}, func() time.Time { return now })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return svc.National(ctx, k, time.Hour)
	}
	a, err := read(nil)
	if err != nil || len(a.Rows) != 2 || a.Rows[0].Family != "r1" || a.Rows[1].Family != "e1" {
		t.Fatalf("at once: %+v %v", a.Rows, err)
	}
	errRoads, errEnergy := errors.New("roads"), errors.New("energy")
	if _, err := read(map[string]error{"roads": errRoads, "energy": errEnergy}); !errors.Is(err, errRoads) {
		t.Errorf("both failed: %v, want the roads' failure", err)
	}
	if _, err := read(map[string]error{"energy": errEnergy}); !errors.Is(err, errEnergy) {
		t.Errorf("energy failed: %v", err)
	}
}

// peakNat records the most reads running at once; the first two wait for
// each other, so two that may run together do.
type peakNat struct {
	*fake
	mu                  sync.Mutex
	running, peak, seen int
	two                 chan struct{}
}

func (f *peakNat) National(ctx context.Context, d string, _ []string, _ time.Time, _ time.Duration) ([]national.Row, error) {
	f.mu.Lock()
	f.running++
	f.peak = max(f.peak, f.running)
	if f.seen++; f.seen == 2 {
		close(f.two)
	}
	first := f.seen <= 2
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.running--; f.mu.Unlock() }()
	if first {
		select {
		case <-f.two:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, nil
}

// No more than Settings.ReadsAtOnce domains are read together: the reads
// past a key's admission share would queue behind its own.
func TestNationalReadsNoMoreThanTheKeysShareAtOnce(t *testing.T) {
	f := &peakNat{fake: &fake{reg: natRegister}, two: make(chan struct{})}
	svc := features.New(features.Repositories{Register: f, National: f}, features.Settings{Language: "en", ReadsAtOnce: 2}, func() time.Time { return now })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	k := key("consumer", []access.Scope{"roads", "energy", "weather"}, "r1", "e1", "w1")
	if _, err := svc.National(ctx, k, time.Hour); err != nil || f.seen != 3 || f.peak != 2 {
		t.Fatalf("read %d domains, %d at most at once (want 3, 2): %v", f.seen, f.peak, err)
	}
}

// panicNat panics in the energy read.
type panicNat struct{ *fake }

func (panicNat) National(_ context.Context, d string, _ []string, _ time.Time, _ time.Duration) ([]national.Row, error) {
	if d == "energy" {
		panic("energy read")
	}
	return nil, nil
}

// A read's panic reaches the caller's goroutine, where the handler's
// recovery answers it (ERR-008), not the read's own, which would end the
// process.
func TestNationalRaisesAReadsPanicInTheCaller(t *testing.T) {
	f := panicNat{&fake{reg: natRegister}}
	svc := features.New(features.Repositories{Register: f, National: f}, features.Settings{Language: "en"}, func() time.Time { return now })
	k := key("commercial", []access.Scope{"roads", "energy"}, "r1", "e1")
	defer func() {
		if r := recover(); r != "energy read" {
			t.Errorf("recovered %v, want the energy read's panic", r)
		}
	}()
	_, _ = svc.National(context.Background(), k, time.Hour)
	t.Error("National returned, want the read's panic")
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
	// the domains are read at once, so in any order
	if slices.Sort(f.asked); !slices.Equal(f.asked, []string{"energy", "roads"}) {
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
	f.provErr = boom
	if _, err := s.National(ctx, key("commercial", []access.Scope{"roads"}, "r1"), time.Hour); !errors.Is(err, boom) {
		t.Errorf("provenance error: %v", err)
	}
	f.provErr, f.regErr = nil, boom
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

// TestAffectedStationsCreditTheLifts: the board credits the lift families
// the key may read, as the site's station board credits the station lifts
// alone, and what contributed when it may read none.
func TestAffectedStationsCreditTheLifts(t *testing.T) {
	ctx := context.Background()
	f := &afFake{fake: &fake{reg: []access.Terms{{Family: "t1", Domain: "transit", UseClasses: all, Version: "v1"},
		{Family: "t2", Domain: "transit", UseClasses: all, Version: "v1"}, {Family: "l1", Domain: "accessibility", UseClasses: all, Version: "v1"}}},
		v: []transit.Affected{{Station: transit.Station{ID: "a", Family: "t1"}, Families: []string{"t1", "t2", "l1", "t1"}}}}
	s := features.New(features.Repositories{Register: f, Affected: f}, features.Settings{Language: "en"}, func() time.Time { return now })
	v, a, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit", "accessibility"}, "t1", "t2", "l1"), 5)
	if err != nil || len(v) != 1 || a.Partial || !slices.Equal(names(a.Families), []string{"l1"}) {
		t.Fatalf("%+v %+v %v", v, a, err)
	}
	// A family the key may not read in the answer is a gate violation.
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1", "t2"), 5); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("lifts without the scope: %v", err)
	}
	f.v = []transit.Affected{{Station: transit.Station{ID: "a", Family: "t1"}, Families: []string{"t1"}}}
	if _, a, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1"), 5); err != nil || !a.Partial || !slices.Equal(names(a.Families), []string{"t1"}) {
		t.Errorf("without the scope accessibility the answer is partial: %+v %v", a, err)
	}
	// A key that may read a lift family is credited with it, scope or none.
	f.v = nil
	if _, a, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1", "l1"), 5); err != nil || !slices.Equal(names(a.Families), []string{"l1"}) {
		t.Errorf("an empty board: %+v %v", a, err)
	}
	f.v = []transit.Affected{{Station: transit.Station{ID: "a", Family: "t1"}, Families: []string{"t1"}}}
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "l1"), 5); !errors.Is(err, access.ErrNonePermitted) {
		t.Errorf("no transit family: %v", err)
	}
	boom := errors.New("boom")
	f.err = boom
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1"), 5); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
	f.err, f.provErr = nil, boom
	if _, _, err := s.AffectedStations(ctx, key("commercial", []access.Scope{"transit"}, "t1"), 5); !errors.Is(err, boom) {
		t.Errorf("provenance error: %v", err)
	}
	f.provErr, f.regErr = nil, boom
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
