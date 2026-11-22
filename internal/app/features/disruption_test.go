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

type disruptionFake struct {
	*fake
	v       transit.Disruptions
	err     error
	gotQ    transit.DisruptionQuery
	gotFams []string
}

func (f *disruptionFake) Disruptions(_ context.Context, q transit.DisruptionQuery, fams []string) (transit.Disruptions, error) {
	f.gotQ, f.gotFams = q, fams
	return f.v, f.err
}

func disruptionService(f *disruptionFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf("transit")
	return features.New(features.Repositories{Register: f, Disruptions: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestDisruptionsAlertsAndPunctualityAtNow(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-003", "SEC-030")
	k := key("commercial", []access.Scope{"transit"}, "f1", "f2")
	f := &disruptionFake{v: transit.Disruptions{Alerts: []transit.Alert{{ID: "a:1", Family: "f1"}}, AlertsTotal: 3,
		Punctuality: &transit.Punctuality{AreaCode: "05315", Family: "f1"}}}
	a, err := disruptionService(f).Disruptions(context.Background(), k, transit.DisruptionQuery{Area: "05315", Limit: 6})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFams, []string{"f1"}) || !f.gotQ.Now.Equal(now) || f.gotQ.Area != "05315" ||
		a.AlertsTotal != 3 || a.Punctuality == nil || !a.Meta.Partial || !a.Meta.At.Equal(now) {
		t.Errorf("answer %+v asked %+v", a, f.gotQ)
	}
}

func TestDisruptionsRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033")
	full := key("commercial", []access.Scope{"transit"}, "f1")
	for name, c := range map[string]struct {
		f    *disruptionFake
		k    access.Record
		want error
	}{
		"register":   {&disruptionFake{fake: &fake{regErr: errStore}}, full, errStore},
		"none":       {&disruptionFake{}, key("commercial", []access.Scope{"transit"}, "f2"), access.ErrNonePermitted},
		"read":       {&disruptionFake{err: errStore}, full, errStore},
		"post-check": {&disruptionFake{v: transit.Disruptions{Punctuality: &transit.Punctuality{Family: "f2"}}}, full, features.ErrGateViolation},
		"provenance": {&disruptionFake{fake: &fake{provErr: errStore}, v: transit.Disruptions{PunctualityRead: []string{"f1"}}}, full, errStore},
		// the trip count is gated like the rest: a family the key may not read fails the answer
		"trips post-check": {&disruptionFake{v: transit.Disruptions{Trips: &transit.TripCount{Observed: 1, Family: "f2"}}}, full, features.ErrGateViolation},
	} {
		if _, err := disruptionService(c.f).Disruptions(context.Background(), c.k, transit.DisruptionQuery{Limit: 6}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestDisruptionsCreditWhatTheAnswerRead(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-003")
	k := key("consumer", []access.Scope{"transit"}, "f1", "f2")
	for name, c := range map[string]struct {
		v    transit.Disruptions
		want []string
	}{
		// The realtime source read for the area is credited with no trip counted, as the platform's "no reports".
		"no reports": {transit.Disruptions{PunctualityRead: []string{"f2"}}, []string{"f2"}},
		"alert":      {transit.Disruptions{Alerts: []transit.Alert{{ID: "a:1", Family: "f1"}}, PunctualityRead: []string{"f2"}}, []string{"f1", "f2"}},
		// Nothing listed and no punctuality read: no family is credited.
		"nothing": {transit.Disruptions{}, nil},
		// The timetable that knows the line asked is credited for saying so; one that does not, is not.
		"line known":   {transit.Disruptions{Line: &transit.LineCheck{Checked: true, InTimetable: true, Families: []string{"f1"}}}, []string{"f1"}},
		"line unknown": {transit.Disruptions{Line: &transit.LineCheck{Checked: true}}, nil},
	} {
		a, err := disruptionService(&disruptionFake{v: c.v}).Disruptions(context.Background(), k, transit.DisruptionQuery{Line: "RE45", Limit: 6})
		if err != nil {
			t.Fatal(err)
		}
		if got := names(a.Meta.Families); !slices.Equal(got, c.want) {
			t.Errorf("%s: credits %v, want %v", name, got, c.want)
		}
	}
}
