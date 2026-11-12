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
	levels      []weather.Level // the levels in force; those of warnings when nil
	more        bool
	next        *time.Time // the next start or end of a warning placed at the position
	stale       []string
	missing     []string
	topic       rules.Topic
	readErr     error
	gotQuery    weather.Query
	gotFamilies []string
	gotNow      time.Time
	gotCode     string
	readCalls   int
}

func (f *wxFake) WarningsAt(_ context.Context, q weather.Query, fams []string, at time.Time) (weather.Scope, error) {
	f.readCalls++
	f.gotQuery, f.gotFamilies, f.gotNow = q, fams, at
	levels := f.levels
	if levels == nil {
		for _, w := range f.warnings {
			levels = append(levels, w.Level)
		}
	}
	return weather.Scope{Warnings: f.warnings, Levels: levels, HasMore: f.more, NextChange: f.next}, f.readErr
}

// Provenance answers every family fresh at now, except those in f.stale,
// and leaves out those in f.missing.
func (f *wxFake) Provenance(ctx context.Context, fams []string, lang string) ([]provenance.Family, error) {
	out, err := f.fake.Provenance(ctx, fams, lang)
	out = slices.DeleteFunc(out, func(p provenance.Family) bool { return slices.Contains(f.missing, p.Source.Family) })
	for i := range out {
		out[i].StaleAfter = time.Minute
		if !slices.Contains(f.stale, out[i].Source.Family) {
			out[i].DataAsOf = &now
		}
	}
	return out, err
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
	// No warning on the same partial answer: a withheld family could hold
	// one, so the sight is limited and the lamp amber, never green.
	f.warnings = nil
	if r, err = wxService(f).WeatherRisk(context.Background(), key("commercial", []access.Scope{"weather"}, "w1", "w2", "k1"), q); err != nil || !r.Partial || r.Lamp != weather.LampAmber {
		t.Errorf("a partial answer with no warning: %s %v", r.Lamp, err)
	}
	f = &wxFake{warnings: []weather.Warning{{ID: "b", Family: "w1", Level: 1}}}
	if r, err = wxService(f).WeatherRisk(context.Background(), key("consumer", []access.Scope{"weather"}, "w1", "w2"), q); err != nil || r.Partial || r.Lamp != weather.LampAmber {
		t.Errorf("a full answer at level 1: %s %v", r.Lamp, err)
	}

	f = &wxFake{}
	r, err = wxService(f).WeatherRisk(context.Background(), key("consumer", []access.Scope{"weather"}, "w1", "w2"), weather.Query{})
	if err != nil || r.Partial || r.Lamp != weather.LampGreen || r.HasMore || !slices.Equal(names(r.Families), []string{"w1", "w2"}) {
		t.Errorf("an empty answer is green and names the consulted families: %+v %v", r, err)
	}
}

func TestTheWeatherLampReadsEveryWarningInForce(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	both := key("consumer", []access.Scope{"weather"}, "w1", "w2")
	// min_level=3 keeps nothing of a level-2 ice warning in force: the list
	// is empty, the lamp still amber.
	f := &wxFake{levels: []weather.Level{2}}
	r, err := wxService(f).WeatherRisk(context.Background(), both, weather.Query{MinLevel: 3})
	if err != nil || len(r.Items) != 0 || r.Lamp != weather.LampAmber {
		t.Errorf("min_level 3 over a level-2 warning: %d listed, %s, %v", len(r.Items), r.Lamp, err)
	}
	// More warnings than the cap: the answer says so.
	f = &wxFake{warnings: []weather.Warning{{ID: "a", Family: "w1", Level: 1}}, more: true}
	if r, err = wxService(f).WeatherRisk(context.Background(), both, weather.Query{}); err != nil || !r.HasMore {
		t.Errorf("a capped list: %+v %v", r, err)
	}
}

func TestTheWeatherLampSeesStaleAndMissingFamilies(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001", "ENV-003")
	both := key("consumer", []access.Scope{"weather"}, "w1", "w2")
	for name, f := range map[string]*wxFake{
		"stale":   {stale: []string{"w2"}},
		"missing": {missing: []string{"w2"}},
	} {
		r, err := wxService(f).WeatherRisk(context.Background(), both, weather.Query{})
		if err != nil || r.Lamp != weather.LampAmber {
			t.Errorf("%s, no warning: %s %v", name, r.Lamp, err)
		}
		if !slices.Equal(f.gotProv[0], []string{"w1", "w2"}) || len(f.gotProv) != 1 {
			t.Errorf("%s: the register was asked %v", name, f.gotProv)
		}
	}
	// A severe warning heard from the fresh family is still red.
	f := &wxFake{warnings: []weather.Warning{{ID: "a", Family: "w1", Level: 4}}, stale: []string{"w2"}}
	if r, err := wxService(f).WeatherRisk(context.Background(), both, weather.Query{}); err != nil || r.Lamp != weather.LampRed || !slices.Equal(names(r.Families), []string{"w1"}) {
		t.Errorf("stale and a level-4 warning: %s %v %v", r.Lamp, names(r.Families), err)
	}
}

func TestTheWeatherAnswerSaysWhenTheClockChangesIt(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	both := key("consumer", []access.Scope{"weather"}, "w1", "w2")
	// Every family is fresh at now and turns stale 61 s later (a threshold
	// of one minute): a warning starting earlier is the answer's change.
	soon, late := now.Add(30*time.Second), now.Add(time.Hour)
	turn := now.Add(61 * time.Second)
	for name, c := range map[string]struct {
		f       *wxFake
		changes *time.Time
	}{
		"a warning starts first": {&wxFake{next: &soon}, &soon},
		"a family turns stale":   {&wxFake{next: &late}, &turn},
		"no warning changes":     {&wxFake{}, &turn},
		"every family stale":     {&wxFake{next: &late, stale: []string{"w1", "w2"}}, &late},
		"nothing changes":        {&wxFake{stale: []string{"w1", "w2"}}, nil},
	} {
		r, err := wxService(c.f).WeatherRisk(context.Background(), both, weather.Query{})
		if err != nil {
			t.Fatal(err)
		}
		if (r.ChangesAt == nil) != (c.changes == nil) || (c.changes != nil && !r.ChangesAt.Equal(*c.changes)) {
			t.Errorf("%s: changes at %v, want %v", name, r.ChangesAt, c.changes)
		}
		if r.WarningChange != c.f.next || !slices.Equal(r.Read, []string{"w1", "w2"}) {
			t.Errorf("%s: warning change %v, read %v", name, r.WarningChange, r.Read)
		}
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
