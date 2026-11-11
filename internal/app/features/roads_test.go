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

// roadRegister: two roads families, one for consumer use only, and two
// weather families, one for consumer use only.
var roadRegister = []access.Terms{
	{Family: "r1", Domain: "roads", UseClasses: all, Version: "v1", Reviewed: true},
	{Family: "r2", Domain: "roads", UseClasses: []access.UseClass{"consumer"}, Version: "v1", Reviewed: true},
	{Family: "e1", Domain: "energy", UseClasses: all, Version: "v1", Reviewed: true},
	{Family: "w1", Domain: "weather", UseClasses: all, Version: "v1", Reviewed: true},
	{Family: "w2", Domain: "weather", UseClasses: []access.UseClass{"consumer"}, Version: "v1", Reviewed: true},
}

type roadFake struct {
	*fake
	road                 roads.Road
	roadErr, readErr     error
	sits                 []roads.Situation
	msgs                 []roads.Message
	warns                []roads.Warning
	stale, missing       []string
	gotSince             time.Time
	gotRoad              roads.CorridorQuery
	gotArea              roads.AreaQuery
	gotFamilies          []string
	gotFams              roads.Families
	gotNow               time.Time
	roadCalls, readCalls int
}

func (f *roadFake) Road(_ context.Context, ref string) (roads.Road, error) {
	f.roadCalls++
	f.gotRoad.Road = ref
	return f.road, f.roadErr
}

// page answers every family given fresh at now, except those in f.stale,
// and leaves out those in f.missing; the warnings only when a weather
// family was given, as the store reads them.
func (f *roadFake) page(fams roads.Families, more bool) roads.Page {
	p := roads.Page{Items: f.sits, HasMore: more, Messages: f.msgs}
	if len(fams.Weather) > 0 {
		p.Warnings = append([]roads.Warning{}, f.warns...)
	}
	old := now.Add(-2 * time.Minute)
	for _, fam := range append(slices.Clone(fams.Roads), fams.Weather...) {
		if slices.Contains(f.missing, fam) {
			continue
		}
		pf := provenance.Family{Source: access.Source{Family: fam}, StaleAfter: time.Minute, DataAsOf: &now}
		if slices.Contains(f.stale, fam) {
			pf.DataAsOf = &old
		}
		p.Sources = append(p.Sources, pf)
	}
	return p
}

func (f *roadFake) SituationsOnRoad(_ context.Context, q roads.CorridorQuery, fams roads.Families, at time.Time) (roads.Page, error) {
	f.readCalls++
	f.gotRoad, f.gotFams, f.gotNow = q, fams, at
	return f.page(fams, true), f.readErr
}

func (f *roadFake) SituationsNear(_ context.Context, q roads.AreaQuery, fams roads.Families, at time.Time) (roads.Page, error) {
	f.readCalls++
	f.gotArea, f.gotFams, f.gotNow = q, fams, at
	return f.page(fams, false), f.readErr
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

var (
	roadsScope   = []access.Scope{"roads"}
	weatherScope = []access.Scope{"roads", "weather"}
)

func de(kind roads.Kind, headline string) roads.Message {
	return roads.Message{Kind: kind, Headline: headline, TextLanguage: "de"}
}

func lit(t *testing.T, name string, c features.Conditions, lamp roads.Lamp, reason roads.Reason) {
	t.Helper()
	if c.Lamp != lamp || (reason == "") != (c.Reason == nil) || (c.Reason != nil && *c.Reason != reason) {
		t.Errorf("%s: lamp %s %v, want %s %q", name, c.Lamp, c.Reason, lamp, reason)
	}
}

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
	ctx := context.Background()
	f := &roadFake{
		road: roads.Road{Ref: "A8", Class: roads.ClassMotorway},
		sits: []roads.Situation{{ID: "a", Family: "r1"}, {ID: "b", Family: "r1"}},
		msgs: []roads.Message{de(roads.KindRoadworks, "Baustelle")},
	}
	commercial := key("commercial", weatherScope, "r1", "r2", "e1", "w1")
	c, err := roadService(f).MotorwayConditions(ctx, commercial, roads.CorridorQuery{Road: "A8", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c.Road == nil || c.Road.Ref != "A8" || f.gotRoad.Road != "A8" || f.gotRoad.Limit != 1 || !slices.Equal(f.gotFams.Roads, []string{"r1"}) || !slices.Equal(f.gotFams.Weather, []string{"w1"}) || !f.gotNow.Equal(now) {
		t.Errorf("road %v, asked %+v of %v", c.Road, f.gotRoad, f.gotFams)
	}
	if !c.Partial || !c.HasMore || !c.At.Equal(now) || len(c.Items) != 2 || !slices.Equal(names(c.Families), []string{"r1"}) {
		t.Errorf("answer %+v", c.Answer)
	}
	// Works on the carriageway are amber, and come before the limited sight
	// of r2, which is withheld from a commercial key.
	lit(t, "works on a partial answer", c, roads.LampAmber, roads.ReasonNarrowing)
	f.msgs = nil
	c, _ = roadService(f).MotorwayConditions(ctx, commercial, roads.CorridorQuery{Road: "A8"})
	lit(t, "nothing on a partial answer", c, roads.LampAmber, roads.ReasonLimitedSight)
	// A safety word is red on any kind, even on a partial answer; a word in
	// a text that is not German is not read.
	f.msgs = []roads.Message{de(roads.KindOther, "Falschfahrer auf der A8")}
	c, _ = roadService(f).MotorwayConditions(ctx, commercial, roads.CorridorQuery{Road: "A8"})
	lit(t, "a wrong-way driver", c, roads.LampRed, roads.ReasonSafety)
	f.msgs[0].TextLanguage = "en"
	c, _ = roadService(f).MotorwayConditions(ctx, key("consumer", weatherScope, "r1", "r2", "w1", "w2"), roads.CorridorQuery{Road: "A8"})
	lit(t, "a word in English", c, roads.LampAmber, roads.ReasonWarning)

	f = &roadFake{road: roads.Road{Ref: "A8"}}
	c, err = roadService(f).MotorwayConditions(ctx, key("consumer", weatherScope, "r1", "r2", "w1", "w2"), roads.CorridorQuery{Road: "A8"})
	if err != nil || c.Partial || !slices.Equal(names(c.Families), []string{"r1", "r2"}) {
		t.Errorf("an empty answer names the consulted roads families: %v %v %v", names(c.Families), c.Partial, err)
	}
	lit(t, "nothing, every group fresh", c, roads.LampGreen, "")
}

func TestRoadConditionsAnswerAnArea(t *testing.T) {
	req.Covers(t, "FR-ROAD-006")
	f := &roadFake{sits: []roads.Situation{{ID: "a", Family: "r2"}}, msgs: []roads.Message{de(roads.KindClosure, "Vollsperrung")}}
	q := roads.AreaQuery{RadiusM: 900, Classes: []roads.Class{roads.ClassUrban}}
	c, err := roadService(f).RoadConditions(context.Background(), key("consumer", roadsScope, "r1", "r2"), q)
	if err != nil {
		t.Fatal(err)
	}
	if c.Road != nil || c.HasMore || f.gotArea.RadiusM != 900 || !slices.Equal(names(c.Families), []string{"r2"}) {
		t.Errorf("answer %+v, asked %+v", c, f.gotArea)
	}
	lit(t, "a closure", c, roads.LampRed, roads.ReasonRoadClosed)
}

func TestTheLampSeesStaleFamilies(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "ENV-003")
	consumer := key("consumer", weatherScope, "r1", "r2", "w1", "w2")
	// r1 contributed nothing and is stale: the sight is limited, so amber,
	// while the attribution names only the family that answered.
	f := &roadFake{sits: []roads.Situation{{ID: "a", Family: "r2"}}, stale: []string{"r1"}}
	c, err := roadService(f).RoadConditions(context.Background(), consumer, roads.AreaQuery{})
	if err != nil || !slices.Equal(names(c.Families), []string{"r2"}) {
		t.Errorf("a stale family: %v, %v", names(c.Families), err)
	}
	lit(t, "a stale roads family", c, roads.LampAmber, roads.ReasonLimitedSight)
	f = &roadFake{}
	if c, err = roadService(f).RoadConditions(context.Background(), consumer, roads.AreaQuery{}); err != nil {
		t.Fatal(err)
	}
	lit(t, "every family fresh and nothing reported", c, roads.LampGreen, "")
	if !slices.Equal(f.gotProv[0], []string{"r1", "r2"}) || len(f.gotProv) != 1 {
		t.Errorf("the register was asked %v", f.gotProv)
	}
	for name, f := range map[string]*roadFake{
		"a roads family without data":   {sits: []roads.Situation{{ID: "a", Family: "r2"}}, missing: []string{"r1"}},
		"a weather family stale":        {stale: []string{"w2"}},
		"a weather family without data": {missing: []string{"w1"}},
	} {
		c, err = roadService(f).RoadConditions(context.Background(), consumer, roads.AreaQuery{})
		if err != nil {
			t.Fatal(err)
		}
		lit(t, name, c, roads.LampAmber, roads.ReasonLimitedSight)
	}
}

func TestTheLampReadsTheDWDWarnings(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	ctx := context.Background()
	consumer := key("consumer", weatherScope, "r1", "r2", "w1", "w2")
	for _, c := range []struct {
		level  int
		lamp   roads.Lamp
		reason roads.Reason
	}{
		{4, roads.LampRed, roads.ReasonWeatherWarning},
		{3, roads.LampRed, roads.ReasonWeatherWarning},
		{2, roads.LampAmber, roads.ReasonWeatherWarning},
		{1, roads.LampAmber, roads.ReasonWeatherWarning},
	} {
		f := &roadFake{warns: []roads.Warning{{Family: "w2", Level: c.level}}}
		got, err := roadService(f).RoadConditions(ctx, consumer, roads.AreaQuery{})
		if err != nil || !slices.Equal(names(got.Families), []string{"w2"}) || !slices.Equal(f.gotFams.Weather, []string{"w1", "w2"}) {
			t.Errorf("level %d: attributed %v, asked %v, %v", c.level, names(got.Families), f.gotFams, err)
		}
		lit(t, "a DWD warning", got, c.lamp, c.reason)
	}
	// A jam of 30 minutes comes before a red warning in the platform's order.
	delay := 30
	f := &roadFake{warns: []roads.Warning{{Family: "w1", Level: 3}}, msgs: []roads.Message{{Kind: roads.KindJam, Headline: "Stau", DelayMin: &delay}}}
	c, _ := roadService(f).RoadConditions(ctx, consumer, roads.AreaQuery{})
	lit(t, "a long jam and a red warning", c, roads.LampRed, roads.ReasonLongJam)

	// A key without the weather scope, or one that may read only some of
	// the weather families, or none, does not see the warnings: the DWD
	// group is missing, so the sight is limited.
	for name, k := range map[string]access.Record{
		"no weather scope":           key("consumer", roadsScope, "r1", "r2", "w1", "w2"),
		"some weather families only": key("commercial", weatherScope, "r1", "w1", "w2"),
		"no weather family":          key("consumer", weatherScope, "r1", "r2"),
	} {
		f := &roadFake{}
		c, err := roadService(f).RoadConditions(ctx, k, roads.AreaQuery{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lit(t, name, c, roads.LampAmber, roads.ReasonLimitedSight)
	}
	f = &roadFake{}
	_, _ = roadService(f).RoadConditions(ctx, key("consumer", roadsScope, "r1", "r2", "w1", "w2"), roads.AreaQuery{})
	if f.gotFams.Weather != nil {
		t.Errorf("a key without the weather scope asked for %v", f.gotFams.Weather)
	}
}

func TestConditionsRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	ctx := context.Background()
	ask := func(f *roadFake, k access.Record) error {
		_, err := roadService(f).RoadConditions(ctx, k, roads.AreaQuery{})
		return err
	}
	full := key("commercial", weatherScope, "r1", "w1")
	for name, c := range map[string]struct {
		f    *roadFake
		k    access.Record
		want error
	}{
		"register":           {&roadFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":               {&roadFake{}, key("commercial", roadsScope, "e1"), access.ErrNonePermitted},
		"read":               {&roadFake{readErr: errStore}, full, errStore},
		"post-check":         {&roadFake{sits: []roads.Situation{{Family: "r2"}}}, full, features.ErrGateViolation},
		"warning post-check": {&roadFake{warns: []roads.Warning{{Family: "w2", Level: 1}}}, full, features.ErrGateViolation},
		"provenance":         {&roadFake{fake: &fake{provErr: errStore}}, full, errStore},
		"withheld all":       {&roadFake{}, key("commercial", roadsScope, "r2"), access.ErrNonePermitted},
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
