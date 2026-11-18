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
	got, err := access.ParseAcceptance("sid-terms-1,other-terms-12")
	if err != nil || !slices.Equal(got, []string{"sid-terms-1", "other-terms-12"}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := access.ParseAcceptance(""); err != nil || got != nil {
		t.Errorf("empty: %v %v", got, err)
	}
	for _, bad := range []string{"sid", "sid-terms", "sid-terms-", "SID-terms-1", "sid-terms-1,sid-terms-1", "sid-terms-1,", "sid:1"} {
		if _, err := access.ParseAcceptance(bad); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
}

func TestCheckAcceptance(t *testing.T) {
	req.Covers(t, "SEC-030")
	reg := []access.Terms{
		{Family: "sid_dyn", AcceptanceRequired: true, Version: "sid-terms-2"},
		{Family: "sid_static", AcceptanceRequired: true, Version: "sid-terms-2"},
		{Family: "open", Version: "2026-03-01"},
	}
	if g := access.TermsGates(reg); !slices.Equal(g, []string{"sid-terms-2"}) {
		t.Errorf("TermsGates = %v", g)
	}
	for _, ok := range [][]string{nil, {"sid-terms-2"}} {
		if err := access.CheckAcceptance(reg, ok); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{"sid-terms-1"}, {"other-terms-1"}, {"sid-terms-2", "open-2026"}} {
		err := access.CheckAcceptance(reg, bad)
		var tv *access.TermsVersionError
		if !errors.Is(err, access.ErrTermsVersion) || !errors.As(err, &tv) || err.Error() != access.ErrTermsVersion.Error() || !slices.Equal(tv.Current, []string{"sid-terms-2"}) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	ctx := access.WithAccepted(context.Background(), []string{"sid-terms-2"})
	if got := access.AcceptedFrom(ctx); len(got) != 1 || access.AcceptedFrom(context.Background()) != nil {
		t.Errorf("context: %v", got)
	}
}
