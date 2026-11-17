package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestPlacesAreGatedAndAttributed(t *testing.T) {
	req.Covers(t, "FR-PLACE-001", "SEC-030")
	f := &fake{place: places.Place{ID: "x", Family: "pl"}}
	a, err := service(f, nil).Places(context.Background(), key("commercial", []access.Scope{"places"}, "pl"), places.Query{Text: "nn", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.gotFamilies, []string{"pl"}) || f.gotStops != nil || len(a.Items) != 1 || !a.HasMore || !a.At.Equal(now) || !slices.Equal(names(a.Families), []string{"pl"}) {
		t.Errorf("answer %+v", a)
	}
	// The stops a key may read answer a name too, without the transit scope.
	f = &fake{place: places.Place{ID: "x", Family: "st"}}
	if _, err := service(f, nil).Places(context.Background(), key("commercial", []access.Scope{"places"}, "pl", "st"), places.Query{Text: "nn", Limit: 1}); err != nil || !slices.Equal(f.gotStops, []string{"st"}) {
		t.Errorf("stops %v, %v", f.gotStops, err)
	}
	full := key("commercial", []access.Scope{"places"}, "pl")
	for name, c := range map[string]struct {
		f    *fake
		k    access.Record
		want error
	}{
		"register":   {&fake{regErr: errStore}, full, errStore},
		"none":       {&fake{}, key("commercial", []access.Scope{"places"}, "e1"), access.ErrNonePermitted},
		"read":       {&fake{plErr: errStore}, full, errStore},
		"post-check": {&fake{place: places.Place{ID: "y", Family: "e1"}}, full, features.ErrGateViolation},
		"provenance": {&fake{provErr: errStore}, full, errStore},
	} {
		if _, err := service(c.f, nil).Places(context.Background(), c.k, places.Query{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
