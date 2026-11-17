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

type h2Fake struct {
	*fake
	near, nearest       []energy.HydrogenStation
	nearErr, nearestErr error
	gotFams             []string
}

func (f *h2Fake) HydrogenNear(_ context.Context, _ energy.HydrogenQuery, fams []string) ([]energy.HydrogenStation, error) {
	f.gotFams = fams
	return f.near, f.nearErr
}

func (f *h2Fake) HydrogenNearest(context.Context, energy.HydrogenQuery, []string) ([]energy.HydrogenStation, error) {
	return f.nearest, f.nearestErr
}

func h2Service(f *h2Fake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = fuRegister
	return features.New(features.Repositories{Register: f, Hydrogen: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestHydrogenStationsInsideOrTheNearestBeyond(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001", "SEC-030")
	k := key("commercial", []access.Scope{"energy"}, "f1", "f2")
	f := &h2Fake{near: []energy.HydrogenStation{{ID: "a", Family: "f1"}, {ID: "b", Family: "f1"}}}
	a, err := h2Service(f).HydrogenStations(context.Background(), k, energy.HydrogenQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFams, []string{"f1"}) || len(a.Items) != 1 || !a.HasMore || a.Nearest != nil || !a.Partial || !a.At.Equal(now) {
		t.Errorf("inside %+v", a)
	}
	f = &h2Fake{nearest: []energy.HydrogenStation{{ID: "z", Family: "f1"}}}
	if a, err = h2Service(f).HydrogenStations(context.Background(), k, energy.HydrogenQuery{Limit: 5}); err != nil || a.Nearest == nil || a.Nearest.ID != "z" || len(a.Items) != 0 {
		t.Errorf("beyond %+v %v", a, err)
	}
	if a, err = h2Service(&h2Fake{}).HydrogenStations(context.Background(), k, energy.HydrogenQuery{Limit: 5}); err != nil || a.Nearest != nil {
		t.Errorf("none %+v %v", a, err)
	}
}

func TestHydrogenStationsRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"energy"}, "f1")
	for name, c := range map[string]struct {
		f    *h2Fake
		k    access.Record
		want error
	}{
		"register":   {&h2Fake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&h2Fake{}, key("commercial", []access.Scope{"energy"}, "f2"), access.ErrNonePermitted},
		"read":       {&h2Fake{nearErr: errStore}, full, errStore},
		"nearest":    {&h2Fake{nearestErr: errStore}, full, errStore},
		"post-check": {&h2Fake{nearest: []energy.HydrogenStation{{Family: "f2"}}}, full, features.ErrGateViolation},
		"provenance": {&h2Fake{fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, err := h2Service(c.f).HydrogenStations(context.Background(), c.k, energy.HydrogenQuery{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
