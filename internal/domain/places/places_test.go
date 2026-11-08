package places_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestMapKind(t *testing.T) {
	req.Covers(t, "ENV-051")
	for _, k := range places.Kinds[:len(places.Kinds)-1] {
		if got := places.MapKind(string(k)); got != k {
			t.Errorf("MapKind(%q) = %q", k, got)
		}
	}
	for _, raw := range []string{"x_unmapped", "other", "", "City"} {
		if got := places.MapKind(raw); got != places.Other {
			t.Errorf("MapKind(%q) = %q", raw, got)
		}
	}
	if len(places.Kinds) != 7 || places.Kinds[len(places.Kinds)-1] != places.Other {
		t.Error("the enumeration changed")
	}
}
