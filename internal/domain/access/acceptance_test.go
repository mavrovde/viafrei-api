package access_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestParseAcceptance(t *testing.T) {
	req.Covers(t, "SEC-030")
	got, err := access.ParseAcceptance("sid:2026-03-01,other:v2")
	if err != nil || !slices.Equal(got, []access.Acceptance{{Source: "sid", Version: "2026-03-01"}, {Source: "other", Version: "v2"}}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := access.ParseAcceptance(""); err != nil || got != nil {
		t.Errorf("empty: %v %v", got, err)
	}
	for _, bad := range []string{"sid", "sid:", ":v1", "sid:v1,sid:v2", "sid:v1,"} {
		if _, err := access.ParseAcceptance(bad); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
	if v := access.TermsValue(access.Terms{Family: "sid", Version: "v3"}); v != "sid:v3" {
		t.Errorf("TermsValue = %q", v)
	}
}

func TestCheckAcceptance(t *testing.T) {
	req.Covers(t, "SEC-030")
	reg := []access.Terms{
		{Family: "sid", AcceptanceRequired: true, Version: "v3"},
		{Family: "open", Version: "v1"},
	}
	if err := access.CheckAcceptance(reg, []access.Acceptance{{Source: "sid", Version: "v3"}}); err != nil {
		t.Errorf("the current version: %v", err)
	}
	if err := access.CheckAcceptance(reg, nil); err != nil {
		t.Errorf("none: %v", err)
	}
	for name, c := range map[string]struct {
		accepted []access.Acceptance
		current  []string
	}{
		"an old version":              {[]access.Acceptance{{Source: "sid", Version: "v2"}}, []string{"sid"}},
		"a family that needs none":    {[]access.Acceptance{{Source: "open", Version: "v1"}}, nil},
		"an unknown family":           {[]access.Acceptance{{Source: "nope", Version: "v1"}}, nil},
		"a current and an old family": {[]access.Acceptance{{Source: "sid", Version: "v3"}, {Source: "nope", Version: "v1"}}, []string{"sid"}},
	} {
		err := access.CheckAcceptance(reg, c.accepted)
		var tv *access.TermsVersionError
		if !errors.Is(err, access.ErrTermsVersion) || !errors.As(err, &tv) || err.Error() != access.ErrTermsVersion.Error() {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var fams []string
		for _, c := range tv.Current {
			fams = append(fams, c.Family)
		}
		if !slices.Equal(fams, c.current) {
			t.Errorf("%s: current %v, want %v", name, fams, c.current)
		}
	}
	ctx := access.WithAccepted(context.Background(), []access.Acceptance{{Source: "sid", Version: "v3"}})
	if got := access.AcceptedFrom(ctx); len(got) != 1 || access.AcceptedFrom(context.Background()) != nil {
		t.Errorf("context: %v", got)
	}
}
