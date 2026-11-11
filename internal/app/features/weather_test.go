package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// wxRegister: two weather families (one consumer only), one rules family.
var wxRegister = []access.Terms{
	{Family: "w1", Domain: "weather", UseClasses: all, Version: "v1", Reviewed: true},
	{Family: "w2", Domain: "weather", UseClasses: []access.UseClass{"consumer"}, Version: "v1", Reviewed: true},
	{Family: "k1", Domain: "rules", UseClasses: all, Version: "v1", Reviewed: true},
}

type wxFake struct {
	*fake
	warnings    []weather.Warning
	topic       rules.Topic
	readErr     error
	gotQuery    weather.Query
	gotFamilies []string
	gotNow      time.Time
	gotCode     string
	readCalls   int
}

func (f *wxFake) WarningsAt(_ context.Context, q weather.Query, fams []string, at time.Time) ([]weather.Warning, error) {
	f.readCalls++
	f.gotQuery, f.gotFamilies, f.gotNow = q, fams, at
	return f.warnings, f.readErr
}

func (f *wxFake) Topic(_ context.Context, code string) (rules.Topic, error) {
	f.readCalls++
	f.gotCode = code
	return f.topic, f.readErr
}

func wxService(f *wxFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = wxRegister
	return features.New(features.Repositories{Register: f, Weather: f, Rules: f},
		features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestWeatherRiskIsGatedAttributedAndLit(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001", "SEC-030")
	f := &wxFake{warnings: []weather.Warning{{ID: "a", Family: "w1", Level: 3}, {ID: "b", Family: "w1", Level: 1}}}
	q := weather.Query{MinLevel: 2, Hazards: []weather.Hazard{"snow"}}
	r, err := wxService(f).WeatherRisk(context.Background(), key("commercial", []access.Scope{"weather"}, "w1", "w2", "k1"), q)
	if err != nil {
		t.Fatal(err)
	}
	if f.gotQuery.MinLevel != 2 || !slices.Equal(f.gotQuery.Hazards, q.Hazards) || !slices.Equal(f.gotFamilies, []string{"w1"}) || !f.gotNow.Equal(now) {
		t.Errorf("asked %+v of %v at %v", f.gotQuery, f.gotFamilies, f.gotNow)
	}
	if !r.Partial || len(r.Items) != 2 || !r.At.Equal(now) || r.Lamp != weather.LampRed || !slices.Equal(names(r.Families), []string{"w1"}) {
		t.Errorf("answer %+v", r)
	}

	f = &wxFake{}
	r, err = wxService(f).WeatherRisk(context.Background(), key("consumer", []access.Scope{"weather"}, "w1", "w2"), weather.Query{})
	if err != nil || r.Partial || r.Lamp != weather.LampGreen || !slices.Equal(names(r.Families), []string{"w1", "w2"}) {
		t.Errorf("an empty answer is green and names the consulted families: %+v %v", r, err)
	}
}

func TestWeatherRiskRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"weather"}, "w1")
	for name, c := range map[string]struct {
		f    *wxFake
		k    access.Record
		want error
	}{
		"register":   {&wxFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&wxFake{}, key("commercial", []access.Scope{"weather"}, "k1"), access.ErrNonePermitted},
		"read":       {&wxFake{readErr: errStore}, full, errStore},
		"post-check": {&wxFake{warnings: []weather.Warning{{Family: "w2"}}}, full, features.ErrGateViolation},
		"provenance": {&wxFake{fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, err := wxService(c.f).WeatherRisk(context.Background(), c.k, weather.Query{}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestDrivingRulesCarryTheirTopicAndFamilies(t *testing.T) {
	req.Covers(t, "FR-RULES-001", "SEC-030")
	reviewed := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f := &wxFake{topic: rules.Topic{Code: "winter_tyres", Title: "Winter tyres", ReviewedOn: reviewed, Rules: []rules.Rule{{Text: "one"}, {Text: "two"}}}}
	topic, a, err := wxService(f).DrivingRules(context.Background(), key("commercial", []access.Scope{"rules"}, "k1", "w1"), "winter_tyres")
	if err != nil {
		t.Fatal(err)
	}
	if f.gotCode != "winter_tyres" || topic.Code != "winter_tyres" || !topic.ReviewedOn.Equal(reviewed) || len(a.Items) != 2 || a.Items[1].Text != "two" {
		t.Errorf("topic %+v, answer %+v", topic, a)
	}
	if a.Partial || !a.At.Equal(now) || !slices.Equal(names(a.Families), []string{"k1"}) {
		t.Errorf("answer %+v", a)
	}

	full := key("commercial", []access.Scope{"rules"}, "k1")
	for name, c := range map[string]struct {
		f    *wxFake
		k    access.Record
		want error
	}{
		"register":   {&wxFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&wxFake{}, key("commercial", []access.Scope{"rules"}, "w1"), access.ErrNonePermitted},
		"unknown":    {&wxFake{readErr: rules.ErrUnknownTopic}, full, rules.ErrUnknownTopic},
		"provenance": {&wxFake{fake: &fake{provErr: errStore}}, full, errStore},
	} {
		if _, _, err := wxService(c.f).DrivingRules(context.Background(), c.k, "x"); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	f = &wxFake{fake: &fake{regErr: errStore}}
	if _, _, err := wxService(f).DrivingRules(context.Background(), full, "x"); err == nil || f.readCalls != 0 {
		t.Errorf("a failed register read the topic (%d)", f.readCalls)
	}
}
