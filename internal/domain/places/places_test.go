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
	if len(places.Kinds) != 9 || places.Kinds[len(places.Kinds)-1] != places.Other {
		t.Error("the enumeration changed")
	}
}

// A place states its official key only when it is an AGS, as the platform
// does: digits, two to twelve of them.
func TestAGSIsAnOfficialKeyOfDigits(t *testing.T) {
	key := func(s string) *string { return &s }
	for _, c := range []struct {
		key  *string
		want bool
	}{{key("01055033"), true}, {key("06"), true}, {key("060000000000"), true}, {key("0600000000001"), false}, {key("HE"), false}, {key("1"), false}, {key(""), false}, {nil, false}} {
		got := places.Place{OfficialKey: c.key}.AGS()
		if (got != nil) != c.want || (got != nil && *got != *c.key) {
			t.Errorf("%v: %v", c.key, got)
		}
	}
}
