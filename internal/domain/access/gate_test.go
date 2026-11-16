package access_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// register mirrors the fictional families of the fixture set, one per
// licence class (docs/test-data.md).
var register = []access.Terms{
	{Family: "alpha", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "v1"},
	{Family: "beta", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "v1"},
	{Family: "gamma", UseClasses: []access.UseClass{"consumer"}, Version: "v1"},
	{Family: "delta", UseClasses: []access.UseClass{"consumer", "commercial"}, AcceptanceRequired: true, Version: "v2"},
	{Family: "epsilon", UseClasses: nil, Version: "v1", HoldReason: "no_api_terms"},
	{Family: "zeta", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "v1"},
	{Family: "eta", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "v1", Audience: access.AudienceOwn},
}

var all = []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"}

// pub is the audience of a key that is not one of ViaFrei's own.
const pub = access.AudiencePublic

func plan(use access.UseClass, sources ...string) access.Plan {
	return access.Plan{Name: "p", UseClass: use, Sources: sources}
}

func TestGateDefaultDeny(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("commercial"), pub, nil, nil)
	if len(g.Allowed()) != 0 {
		t.Fatalf("a plan with no families may read %v", g.Allowed())
	}
	if g.Refused("not-in-register") != access.Unknown {
		t.Error("a family missing from the register is not refused as unknown")
	}
}

// One test per restricted family: the expected answers are written out here,
// never computed from the register the code reads.

func TestGateRestrictedPurposeFamily(t *testing.T) {
	req.Covers(t, "SEC-030")
	for use, want := range map[access.UseClass]access.Refusal{
		"consumer": access.Allowed, "non_commercial": access.UseClassForbidden, "commercial": access.UseClassForbidden,
	} {
		if got := access.NewGate(register, plan(use, all...), pub, nil, nil).Refused("gamma"); got != want {
			t.Errorf("gamma for %s: %q, want %q", use, got, want)
		}
	}
}

func TestGateTermsAcceptanceFamily(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-034")
	p := plan("commercial", all...)
	cases := []struct {
		name     string
		accepted []access.Acceptance
		want     access.Refusal
	}{
		{"none", nil, access.TermsNotAccepted},
		{"an older version", []access.Acceptance{{Source: "delta", Version: "v1"}}, access.TermsNotAccepted},
		{"another family", []access.Acceptance{{Source: "alpha", Version: "v2"}}, access.TermsNotAccepted},
		{"the current version", []access.Acceptance{{Source: "delta", Version: "v2"}}, access.Allowed},
	}
	for _, c := range cases {
		if got := access.NewGate(register, p, pub, c.accepted, nil).Refused("delta"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	accepted := []access.Acceptance{{Source: "delta", Version: "v2"}}
	if got := access.NewGate(register, plan("non_commercial", all...), pub, accepted, nil).Refused("delta"); got != access.UseClassForbidden {
		t.Errorf("acceptance must not lift a use-class refusal: %q", got)
	}
}

func TestGateHeldFamily(t *testing.T) {
	req.Covers(t, "SEC-030")
	for _, use := range access.UseClasses {
		for _, aud := range []access.Audience{pub, access.AudienceOwn} {
			if got := access.NewGate(register, plan(use, all...), aud, nil, nil).Refused("epsilon"); got != access.Held {
				t.Errorf("epsilon for %s/%s: %q", use, aud, got)
			}
		}
	}
}

func TestGateOwnAudienceFamily(t *testing.T) {
	req.Covers(t, "SEC-030")
	cases := []struct {
		key  access.Audience
		want access.Refusal
	}{
		{access.AudienceOwn, access.Allowed},
		{access.AudiencePublic, access.OwnAudienceOnly},
		{"", access.OwnAudienceOnly},         // a key that names none is public
		{"internal", access.OwnAudienceOnly}, // and so is one that names an unknown audience
	}
	for _, c := range cases {
		if got := access.NewGate(register, plan("consumer", all...), c.key, nil, nil).Refused("eta"); got != c.want {
			t.Errorf("eta for key audience %q: %q, want %q", c.key, got, c.want)
		}
	}
	// A public family is read by an own key as by any other.
	if got := access.NewGate(register, plan("consumer", all...), access.AudienceOwn, nil, nil).Refused("alpha"); got != access.Allowed {
		t.Errorf("alpha for an own key: %q", got)
	}
}

// TestGateOrder holds the order of the rules: each family below breaks two
// rules, and the refusal names the earlier one.
func TestGateOrder(t *testing.T) {
	req.Covers(t, "SEC-030")
	reg := []access.Terms{
		{Family: "dis-held", HoldReason: "x", UseClasses: access.UseClasses},
		{Family: "held-own", HoldReason: "x", Audience: access.AudienceOwn, UseClasses: access.UseClasses},
		{Family: "own-plan", Audience: access.AudienceOwn, UseClasses: access.UseClasses},
		{Family: "plan-use", UseClasses: []access.UseClass{"commercial"}},
		{Family: "use-terms", UseClasses: []access.UseClass{"commercial"}, AcceptanceRequired: true, Version: "v1"},
	}
	p := plan("consumer", "dis-held", "held-own", "use-terms")
	g := access.NewGate(reg, p, pub, nil, []string{"dis-held"})
	for fam, want := range map[string]access.Refusal{
		"dis-held": access.Disabled, "held-own": access.Held, "own-plan": access.OwnAudienceOnly,
		"plan-use": access.NotInPlan, "use-terms": access.UseClassForbidden,
	} {
		if got := g.Refused(fam); got != want {
			t.Errorf("%s: %q, want %q", fam, got, want)
		}
	}
}

func TestRowPermitted(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-032")
	own, public := access.AudienceOwn, access.AudiencePublic
	cases := []struct {
		name         string
		displayClass string
		row          access.Audience
		hold         string
		key          access.Audience
		want         bool
	}{
		{"open public row, public key", "open", public, "", public, true},
		{"open public row, own key", "open", public, "", own, true},
		{"consumer_only row", "consumer_only", public, "", public, true},
		{"gated row (the gate decides the terms)", "gated", public, "", public, true},
		{"withheld row, public key", "withhold", public, "", public, false},
		{"withheld row, own key", "withhold", own, "", own, false},
		{"held row, own key", "open", own, "no_api_terms", own, false},
		{"held row, public key", "gated", public, "no_api_terms", public, false},
		{"own row, public key", "no_redistribution", own, "", public, false},
		{"own row, key naming none", "open", own, "", "", false},
		{"own row, own key", "no_redistribution", own, "", own, true},
		{"row naming no audience is public", "open", "", "", public, true},
	}
	for _, c := range cases {
		if got := access.RowPermitted(c.displayClass, c.row, c.hold, c.key); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func FuzzRowPermitted(f *testing.F) {
	f.Add("open", "public", "", "public")
	f.Add("withhold", "own", "x", "own")
	f.Fuzz(func(t *testing.T, dc, row, hold, key string) {
		got := access.RowPermitted(dc, access.Audience(row), hold, access.Audience(key))
		want := dc != "withhold" && hold == "" && (row != "own" || key == "own")
		if got != want {
			t.Fatalf("RowPermitted(%q, %q, %q, %q) = %v", dc, row, hold, key, got)
		}
	})
}

func TestGateDisabledFamily(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("consumer", all...), pub, nil, []string{"alpha"})
	if g.Refused("alpha") != access.Disabled || g.Permits("alpha") {
		t.Fatal("a disabled family is served")
	}
}

func TestGateFamilyOutsideThePlan(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("consumer", "alpha"), pub, nil, nil)
	if g.Refused("zeta") != access.NotInPlan {
		t.Fatal("a family outside the plan is served")
	}
}

func TestGateAllowedSet(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("consumer", "zeta", "alpha", "gamma", "delta"), pub, nil, nil)
	if want := []string{"alpha", "gamma", "zeta"}; !slices.Equal(g.Allowed(), want) {
		t.Fatalf("allowed %v, want %v", g.Allowed(), want)
	}
	if g.Refused("alpha") != access.Allowed {
		t.Error("an allowed family reports a refusal")
	}
}

func TestPostCheck(t *testing.T) {
	req.Covers(t, "SEC-032")
	g := access.NewGate(register, plan("consumer", "alpha"), pub, nil, nil)
	if err := g.Check([]string{"alpha", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Check([]string{"alpha", "gamma"}); !errors.Is(err, access.ErrForbiddenItem) {
		t.Fatalf("a forbidden item passed the post-check: %v", err)
	}
}

func TestAnswer(t *testing.T) {
	req.Covers(t, "SEC-033")
	g := access.NewGate(register, plan("commercial", all...), pub, nil, nil)
	got, partial, err := g.Answer([]string{"alpha", "gamma"})
	if err != nil || !partial || !slices.Equal(got, []string{"alpha"}) {
		t.Errorf("partial answer: %v %v %v", got, partial, err)
	}
	got, partial, err = g.Answer([]string{"alpha", "zeta"})
	if err != nil || partial || len(got) != 2 {
		t.Errorf("full answer: %v %v %v", got, partial, err)
	}
	if _, _, err := g.Answer([]string{"gamma", "epsilon"}); !errors.Is(err, access.ErrNonePermitted) {
		t.Errorf("none permitted: %v", err)
	}
	if _, _, err := g.Answer([]string{"delta"}); !errors.Is(err, access.ErrTermsNotAccepted) {
		t.Errorf("terms only: %v", err)
	}
	if _, _, err := g.Answer([]string{"delta", "gamma"}); !errors.Is(err, access.ErrNonePermitted) {
		t.Errorf("terms and use class: %v", err)
	}
	if _, _, err := g.Answer(nil); !errors.Is(err, access.ErrNonePermitted) {
		t.Errorf("nothing answering: %v", err)
	}
}

func TestAnsweringIsTheDomainsFamiliesSorted(t *testing.T) {
	req.Covers(t, "SEC-032")
	reg := []access.Terms{{Family: "zeta", Domain: "energy"}, {Family: "beta", Domain: "parking"}, {Family: "alpha", Domain: "energy"}}
	if got := access.Answering(reg, "energy"); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Errorf("Answering energy = %v", got)
	}
	if got := access.Answering(reg, "places"); len(got) != 0 {
		t.Errorf("Answering places = %v", got)
	}
}

func TestPendingIsTermsOnly(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("commercial", "alpha", "gamma", "delta", "epsilon"), pub, nil, nil)
	if got := g.Pending(all); !slices.Equal(got, []string{"delta"}) {
		t.Errorf("Pending = %v, want only the family waiting for acceptance", got)
	}
	accepted := access.NewGate(register, plan("commercial", "delta"), pub, []access.Acceptance{{Source: "delta", Version: "v2"}}, nil)
	if got := accepted.Pending(all); len(got) != 0 {
		t.Errorf("Pending after acceptance = %v", got)
	}
}
