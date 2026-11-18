package places

import (
	"slices"
	"testing"
)

func TestCoverageFamiliesAreEachNamedOnce(t *testing.T) {
	c := Coverage{Kinds: []KindCount{{Family: "g"}, {Family: "o"}, {Family: "g"}}, Lands: []Land{{Family: "b"}, {Family: "g"}}, Addresses: []AddressLand{{Family: "o"}, {Family: "a"}}}
	if got := c.Families(); !slices.Equal(got, []string{"g", "o", "b", "a"}) {
		t.Errorf("families %v", got)
	}
	if got := (Coverage{}).Families(); got != nil {
		t.Errorf("empty: %v", got)
	}
}
