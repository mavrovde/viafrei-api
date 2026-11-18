package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// registerOf is a register of two families answering domain: f1 for every use
// class, f2 for consumers only.
func registerOf(domain string) []access.Terms {
	return []access.Terms{
		{Family: "f1", Domain: domain, UseClasses: all, Version: "v1"},
		{Family: "f2", Domain: domain, UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
	}
}

type speedFake struct {
	*fake
	v       roads.SpeedCameras
	err     error
	gotQ    roads.SpeedQuery
	gotFams []string
}

func (f *speedFake) SpeedCameras(_ context.Context, q roads.SpeedQuery, fams []string) (roads.SpeedCameras, error) {
	f.gotQ, f.gotFams = q, fams
	return f.v, f.err
}

func speedService(f *speedFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf("roads")
	return features.New(features.Repositories{Register: f, Speed: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestSpeedCamerasBothBlocksOnTheBerlinDay(t *testing.T) {
	req.Covers(t, "SEC-030")
	k := key("commercial", []access.Scope{"roads"}, "f1", "f2")
	f := &speedFake{v: roads.SpeedCameras{Official: []roads.SpeedCamera{{ID: "se:1", Family: "f1"}}, OfficialTotal: 3,
		OSM: []roads.SpeedCamera{{ID: "osm:node1", Family: "f1"}}, OSMTotal: 1}}
	a, err := speedService(f).SpeedCameras(context.Background(), k, roads.SpeedQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	// 01:30 UTC on 29 March is 03:30 in Berlin, the same day.
	if !slices.Equal(f.gotFams, []string{"f1"}) || f.gotQ.Today.Format(time.DateOnly) != "2026-03-29" ||
		a.OfficialTotal != 3 || len(a.OSM) != 1 || !a.Meta.Partial || !a.Meta.At.Equal(now) {
		t.Errorf("answer %+v asked %+v", a, f.gotQ)
	}
}

func TestSpeedCamerasRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"roads"}, "f1")
	for name, c := range map[string]struct {
		f    *speedFake
		k    access.Record
		want error
	}{
		"register":   {&speedFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&speedFake{}, key("commercial", []access.Scope{"roads"}, "f2"), access.ErrNonePermitted},
		"read":       {&speedFake{err: errStore}, full, errStore},
		"post-check": {&speedFake{v: roads.SpeedCameras{OSM: []roads.SpeedCamera{{Family: "f2"}}}}, full, features.ErrGateViolation},
		"provenance": {&speedFake{fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, err := speedService(c.f).SpeedCameras(context.Background(), c.k, roads.SpeedQuery{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
