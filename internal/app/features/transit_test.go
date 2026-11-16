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
