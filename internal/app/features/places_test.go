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
	// An empty answer credits none, as the platform's place search.
	if a, err := service(&fake{}, nil).Places(context.Background(), key("commercial", []access.Scope{"places"}, "pl"), places.Query{Text: "nn", Limit: 1}); err != nil || len(a.Families) != 0 {
		t.Errorf("empty: %+v %v", a.Families, err)
	}
	// The family that names a place's Kreis is credited, once.
	f = &fake{place: places.Place{ID: "x", Family: "pl", KreisFamily: "st"}}
	if a, err := service(f, nil).Places(context.Background(), key("commercial", []access.Scope{"places"}, "pl", "st"), places.Query{Text: "nn", Limit: 1}); err != nil ||
		!slices.Equal(names(a.Families), []string{"pl", "st"}) && !slices.Equal(names(a.Families), []string{"st", "pl"}) {
		t.Errorf("the Kreis family: %+v %v", a.Families, err)
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
		"provenance": {&fake{place: places.Place{ID: "x", Family: "pl"}, provErr: errStore}, full, errStore},
	} {
		if _, err := service(c.f, nil).Places(context.Background(), c.k, places.Query{Limit: 5}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

// TestAJunctionIsAskedByItsName: a junction word in front of the name asks
// for a junction by the name after it, kind or none; a plain name keeps its
// kind and text (G5).
func TestAJunctionIsAskedByItsName(t *testing.T) {
	req.Covers(t, "FR-PLACE-001")
	k := key("commercial", []access.Scope{"places"}, "pl")
	for _, c := range []struct {
		text, kind, wantText, wantKind string
	}{
		{"AK Hilden", "", "hilden", "junction"},
		{"Hilden (A3)", "junction", "hilden", "junction"},
		{"Kreuzberg", "", "Kreuzberg", ""},
		{"AS Baden-Baden", "municipality", "AS Baden-Baden", "municipality"},
	} {
		f := &fake{}
		if _, err := service(f, nil).Places(context.Background(), k, places.Query{Text: c.text, Kind: places.Kind(c.kind), Limit: 1}); err != nil ||
			f.gotPlaceQ.Text != c.wantText || string(f.gotPlaceQ.Kind) != c.wantKind {
			t.Errorf("%q/%q: %+v %v", c.text, c.kind, f.gotPlaceQ, err)
		}
	}
}

// TestPlacesHandTheRoadsFamilies: the roads families a key may read go to the
// store for the motorway places; a key that may read none hands none (G5).
func TestPlacesHandTheRoadsFamilies(t *testing.T) {
	req.Covers(t, "FR-PLACE-001")
	f := &fake{}
	if _, err := service(f, nil).Places(context.Background(), key("commercial", []access.Scope{"places"}, "pl", "rd"), places.Query{Text: "A9", Limit: 1}); err != nil || !slices.Equal(f.gotPlaceRoads, []string{"rd"}) {
		t.Errorf("roads %v, %v", f.gotPlaceRoads, err)
	}
	f = &fake{}
	if _, err := service(f, nil).Places(context.Background(), key("commercial", []access.Scope{"places"}, "pl"), places.Query{Text: "A9", Limit: 1}); err != nil || f.gotPlaceRoads != nil {
		t.Errorf("roads %v, %v", f.gotPlaceRoads, err)
	}
}
