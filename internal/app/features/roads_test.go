package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// roadRegister: two roads families, one for consumer use only.
var roadRegister = []access.Terms{
	{Family: "r1", Domain: "roads", UseClasses: all, Version: "v1", Reviewed: true},
	{Family: "r2", Domain: "roads", UseClasses: []access.UseClass{"consumer"}, Version: "v1", Reviewed: true},
	{Family: "e1", Domain: "energy", UseClasses: all, Version: "v1", Reviewed: true},
}

type roadFake struct {
	*fake
	road                 roads.Road
	roadErr, readErr     error
	sits                 []roads.Situation
	sigs                 []roads.Signal
	stale                []string
	gotSince             time.Time
	gotRoad              roads.CorridorQuery
	gotArea              roads.AreaQuery
	gotFamilies          []string
	gotNow               time.Time
	roadCalls, readCalls int
}

func (f *roadFake) Road(_ context.Context, ref string) (roads.Road, error) {
	f.roadCalls++
	f.gotRoad.Road = ref
	return f.road, f.roadErr
}

func (f *roadFake) SituationsOnRoad(_ context.Context, q roads.CorridorQuery, fams []string, at time.Time) ([]roads.Situation, []roads.Signal, bool, error) {
	f.readCalls++
	f.gotRoad, f.gotFamilies, f.gotNow = q, fams, at
	return f.sits, f.sigs, true, f.readErr
}

func (f *roadFake) SituationsNear(_ context.Context, q roads.AreaQuery, fams []string, at time.Time) ([]roads.Situation, []roads.Signal, bool, error) {
	f.readCalls++
	f.gotArea, f.gotFamilies, f.gotNow = q, fams, at
	return f.sits, f.sigs, false, f.readErr
}

// Provenance answers every family fresh at now, except those in f.stale.
func (f *roadFake) Provenance(ctx context.Context, fams []string, lang string) ([]provenance.Family, error) {
	out, err := f.fake.Provenance(ctx, fams, lang)
	for i := range out {
		out[i].StaleAfter = time.Minute
		if !slices.Contains(f.stale, out[i].Source.Family) {
			out[i].DataAsOf = &now
		}
	}
	return out, err
}

func (f *roadFake) Situation(_ context.Context, id string, fams []string, endedSince time.Time) (roads.Situation, error) {
	f.readCalls++
	f.gotFamilies, f.gotSince = fams, endedSince
	if len(f.sits) == 0 {
		return roads.Situation{}, f.readErr
	}
	s := f.sits[0]
	s.ID = id
	return s, f.readErr
}

func roadService(f *roadFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = roadRegister
	return features.New(features.Repositories{Register: f, Roads: f},
		features.Settings{Language: "en", EndedRetention: 24 * time.Hour}, func() time.Time { return now })
}

var roadsScope = []access.Scope{"roads"}

func TestMotorwayConditionsResolveTheRoadFirst(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	f := &roadFake{roadErr: roads.ErrUnknownRoad, fake: &fake{regErr: errStore}}
	if _, err := roadService(f).MotorwayConditions(context.Background(), key("commercial", roadsScope), roads.CorridorQuery{Road: "A999"}); !errors.Is(err, roads.ErrUnknownRoad) {
		t.Fatalf("got %v", err)
	}
	if f.regCalls != 0 || f.readCalls != 0 {
		t.Errorf("an unknown road read the register (%d) or the situations (%d)", f.regCalls, f.readCalls)
	}
}

func TestMotorwayConditionsAreGatedAttributedAndLit(t *testing.T) {
	req.Covers(t, "FR-ROAD-003", "FR-ROAD-004", "SEC-030", "ENV-003")
	f := &roadFake{
		road: roads.Road{Ref: "A8", Class: roads.ClassMotorway},
		sits: []roads.Situation{{ID: "a", Family: "r1"}, {ID: "b", Family: "r1"}},
		sigs: []roads.Signal{{Kind: roads.KindRoadworks, Severity: roads.SeverityMinor}, {Kind: roads.KindRoadworks, Severity: roads.SeverityLaneClosed}},
	}
	c, err := roadService(f).MotorwayConditions(context.Background(), key("commercial", roadsScope, "r1", "r2", "e1"), roads.CorridorQuery{Road: "A8", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c.Road == nil || c.Road.Ref != "A8" || f.gotRoad.Road != "A8" || f.gotRoad.Limit != 1 || !slices.Equal(f.gotFamilies, []string{"r1"}) || !f.gotNow.Equal(now) {
		t.Errorf("road %v, asked %+v of %v", c.Road, f.gotRoad, f.gotFamilies)
	}
	if !c.Partial || !c.HasMore || !c.At.Equal(now) || len(c.Items) != 2 || !slices.Equal(names(c.Families), []string{"r1"}) {
		t.Errorf("answer %+v", c.Answer)
	}
	// r2 is withheld from a commercial key, so the narrowing is not enough
	// to say amber: a closure could be among r2's situations.
	if c.Lamp != roads.LampUnknown || c.Reason == nil || *c.Reason != roads.ReasonPartial {
		t.Errorf("lamp %s %v", c.Lamp, c.Reason)
	}
	f.sigs = append(f.sigs, roads.Signal{Kind: roads.KindHazard, Severity: roads.SeverityInfo})
	if c, _ = roadService(f).MotorwayConditions(context.Background(), key("commercial", roadsScope, "r1", "r2", "e1"), roads.CorridorQuery{Road: "A8"}); c.Lamp != roads.LampRed || *c.Reason != roads.ReasonSafety {
		t.Errorf("a hazard is red even on a partial answer: %s %v", c.Lamp, c.Reason)
	}
	f.sigs = f.sigs[:2]
	if c, _ = roadService(f).MotorwayConditions(context.Background(), key("consumer", roadsScope, "r1", "r2"), roads.CorridorQuery{Road: "A8"}); c.Partial || c.Lamp != roads.LampAmber || *c.Reason != roads.ReasonNarrowing {
		t.Errorf("a full answer with a narrowing: %s %v %v", c.Lamp, c.Reason, c.Partial)
	}

	f = &roadFake{road: roads.Road{Ref: "A8"}}
	c, err = roadService(f).MotorwayConditions(context.Background(), key("consumer", roadsScope, "r1", "r2"), roads.CorridorQuery{Road: "A8"})
	if err != nil || c.Partial || c.Lamp != roads.LampGreen || c.Reason != nil || !slices.Equal(names(c.Families), []string{"r1", "r2"}) {
		t.Errorf("an empty answer is green and names the consulted families: %v %v %v %v", c.Lamp, names(c.Families), c.Partial, err)
	}
}

func TestRoadConditionsAnswerAnArea(t *testing.T) {
	req.Covers(t, "FR-ROAD-006")
	f := &roadFake{sits: []roads.Situation{{ID: "a", Family: "r2"}}, sigs: []roads.Signal{{Kind: roads.KindClosure, Severity: roads.SeverityRoadClosed}}}
	q := roads.AreaQuery{RadiusM: 900, Classes: []roads.Class{roads.ClassUrban}}
	c, err := roadService(f).RoadConditions(context.Background(), key("consumer", roadsScope, "r1", "r2"), q)
	if err != nil {
		t.Fatal(err)
	}
	if c.Road != nil || c.Lamp != roads.LampRed || c.HasMore || f.gotArea.RadiusM != 900 || !slices.Equal(names(c.Families), []string{"r2"}) {
		t.Errorf("answer %+v, asked %+v", c, f.gotArea)
	}
}

func TestTheLampSeesStaleFamilies(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "ENV-003")
	// r1 contributed nothing and is stale: the sight is limited, so amber,
	// while the attribution names only the family that answered.
	f := &roadFake{sits: []roads.Situation{{ID: "a", Family: "r2"}}, stale: []string{"r1"}}
	c, err := roadService(f).RoadConditions(context.Background(), key("consumer", roadsScope, "r1", "r2"), roads.AreaQuery{})
	if err != nil || c.Lamp != roads.LampAmber || c.Reason == nil || *c.Reason != roads.ReasonStaleSource || !slices.Equal(names(c.Families), []string{"r2"}) {
		t.Errorf("a stale family: %s %v %v, %v", c.Lamp, c.Reason, names(c.Families), err)
	}
	f = &roadFake{}
	if c, err = roadService(f).RoadConditions(context.Background(), key("consumer", roadsScope, "r1", "r2"), roads.AreaQuery{}); err != nil || c.Lamp != roads.LampGreen || c.Reason != nil {
		t.Errorf("every family fresh and nothing reported: %s %v, %v", c.Lamp, c.Reason, err)
	}
	if !slices.Equal(f.gotProv[0], []string{"r1", "r2"}) || len(f.gotProv) != 1 {
		t.Errorf("the register was asked %v", f.gotProv)
	}
}

func TestConditionsRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	ctx := context.Background()
	ask := func(f *roadFake, k access.Record) error {
		_, err := roadService(f).RoadConditions(ctx, k, roads.AreaQuery{})
		return err
	}
	full := key("commercial", roadsScope, "r1")
	for name, c := range map[string]struct {
		f    *roadFake
		k    access.Record
		want error
	}{
		"register":     {&roadFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":         {&roadFake{}, key("commercial", roadsScope, "e1"), access.ErrNonePermitted},
		"read":         {&roadFake{readErr: errStore}, full, errStore},
		"post-check":   {&roadFake{sits: []roads.Situation{{Family: "r2"}}}, full, features.ErrGateViolation},
		"provenance":   {&roadFake{fake: &fake{provErr: errStore}}, full, errStore},
		"withheld all": {&roadFake{}, key("commercial", roadsScope, "r2"), access.ErrNonePermitted},
	} {
		if err := ask(c.f, c.k); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	f := &roadFake{road: roads.Road{Ref: "A8"}, readErr: errStore}
	if _, err := roadService(f).MotorwayConditions(ctx, full, roads.CorridorQuery{Road: "A8"}); !errors.Is(err, errStore) {
		t.Errorf("motorway read: %v", err)
	}
}

func TestOneSituationIsNeverPartial(t *testing.T) {
	req.Covers(t, "FR-ROAD-002", "SEC-030")
	ctx := context.Background()
	f := &roadFake{sits: []roads.Situation{{Family: "r1"}}}
	a, err := roadService(f).RoadSituation(ctx, key("commercial", roadsScope, "r1", "r2"), "ev-1")
	if err != nil || len(a.Items) != 1 || a.Items[0].ID != "ev-1" || a.Partial || !slices.Equal(f.gotFamilies, []string{"r1"}) || !slices.Equal(names(a.Families), []string{"r1"}) || !a.At.Equal(now) {
		t.Fatalf("answer %+v, asked %v, %v", a, f.gotFamilies, err)
	}
	// An ended situation answers for roads.ended_retention_h, then not.
	if !f.gotSince.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("asked for situations ended since %v", f.gotSince)
	}
	full := key("commercial", roadsScope, "r1")
	for name, c := range map[string]struct {
		f    *roadFake
		k    access.Record
		want error
	}{
		"register":   {&roadFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&roadFake{}, key("commercial", roadsScope, "e1"), access.ErrNonePermitted},
		"unknown":    {&roadFake{readErr: roads.ErrUnknownSituation}, full, roads.ErrUnknownSituation},
		"post-check": {&roadFake{sits: []roads.Situation{{Family: "r2"}}}, full, features.ErrGateViolation},
		"provenance": {&roadFake{sits: []roads.Situation{{Family: "r1"}}, fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, err := roadService(c.f).RoadSituation(ctx, c.k, "ev-1"); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
