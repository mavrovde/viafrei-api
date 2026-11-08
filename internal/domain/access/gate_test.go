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
	{Family: "alpha", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "v1", Reviewed: true},
	{Family: "beta", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "v1", Reviewed: true},
	{Family: "gamma", UseClasses: []access.UseClass{"consumer"}, Version: "v1", Reviewed: true},
	{Family: "delta", UseClasses: []access.UseClass{"consumer", "commercial"}, AcceptanceRequired: true, Version: "v2", Reviewed: true},
	{Family: "epsilon", UseClasses: nil, Version: "v1", Reviewed: false},
	{Family: "zeta", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "v1", Reviewed: true},
}

var all = []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}

func plan(use access.UseClass, sources ...string) access.Plan {
	return access.Plan{Name: "p", UseClass: use, Sources: sources}
}

func TestGateDefaultDeny(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("commercial"), nil, nil)
	if len(g.Allowed()) != 0 {
		t.Fatalf("a plan with no families may read %v", g.Allowed())
	}
	if g.Refused("not-in-register") != access.NotReviewed {
		t.Error("a family missing from the register is not refused as unreviewed")
	}
}

// One test per restricted family: the expected answers are written out here,
// never computed from the register the code reads.

func TestGateRestrictedPurposeFamily(t *testing.T) {
	req.Covers(t, "SEC-030")
	for use, want := range map[access.UseClass]access.Refusal{
		"consumer": access.Allowed, "non_commercial": access.UseClassForbidden, "commercial": access.UseClassForbidden,
	} {
		if got := access.NewGate(register, plan(use, all...), nil, nil).Refused("gamma"); got != want {
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
		if got := access.NewGate(register, p, c.accepted, nil).Refused("delta"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	accepted := []access.Acceptance{{Source: "delta", Version: "v2"}}
	if got := access.NewGate(register, plan("non_commercial", all...), accepted, nil).Refused("delta"); got != access.UseClassForbidden {
		t.Errorf("acceptance must not lift a use-class refusal: %q", got)
	}
}

func TestGateUnreviewedFamily(t *testing.T) {
	req.Covers(t, "SEC-030")
	for _, use := range access.UseClasses {
		if got := access.NewGate(register, plan(use, all...), nil, nil).Refused("epsilon"); got != access.NotReviewed {
			t.Errorf("epsilon for %s: %q", use, got)
		}
	}
}

func TestGateDisabledFamily(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("consumer", all...), nil, []string{"alpha"})
	if g.Refused("alpha") != access.Disabled || g.Permits("alpha") {
		t.Fatal("a disabled family is served")
	}
}

func TestGateFamilyOutsideThePlan(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("consumer", "alpha"), nil, nil)
	if g.Refused("zeta") != access.NotInPlan {
		t.Fatal("a family outside the plan is served")
	}
}

func TestGateAllowedSet(t *testing.T) {
	req.Covers(t, "SEC-030")
	g := access.NewGate(register, plan("consumer", "zeta", "alpha", "gamma", "delta"), nil, nil)
	if want := []string{"alpha", "gamma", "zeta"}; !slices.Equal(g.Allowed(), want) {
		t.Fatalf("allowed %v, want %v", g.Allowed(), want)
	}
	if g.Refused("alpha") != access.Allowed {
		t.Error("an allowed family reports a refusal")
	}
}

func TestPostCheck(t *testing.T) {
	req.Covers(t, "SEC-032")
	g := access.NewGate(register, plan("consumer", "alpha"), nil, nil)
	if err := g.Check([]string{"alpha", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Check([]string{"alpha", "gamma"}); !errors.Is(err, access.ErrForbiddenItem) {
		t.Fatalf("a forbidden item passed the post-check: %v", err)
	}
}

func TestAnswer(t *testing.T) {
	req.Covers(t, "SEC-033")
	g := access.NewGate(register, plan("commercial", all...), nil, nil)
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
	g := access.NewGate(register, plan("commercial", "alpha", "gamma", "delta", "epsilon"), nil, nil)
	if got := g.Pending(all); !slices.Equal(got, []string{"delta"}) {
		t.Errorf("Pending = %v, want only the family waiting for acceptance", got)
	}
	accepted := access.NewGate(register, plan("commercial", "delta"), []access.Acceptance{{Source: "delta", Version: "v2"}}, nil)
	if got := accepted.Pending(all); len(got) != 0 {
		t.Errorf("Pending after acceptance = %v", got)
	}
}
