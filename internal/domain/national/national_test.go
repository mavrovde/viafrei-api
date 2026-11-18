package national

import (
	"slices"
	"testing"
)

func TestFamiliesAreListedOnceInOrder(t *testing.T) {
	got := Families([]Row{{Family: "b"}, {Family: "a"}, {Family: "b"}})
	if !slices.Equal(got, []string{"b", "a"}) {
		t.Fatal(got)
	}
	if Families(nil) != nil {
		t.Fatal("no rows, no families")
	}
}
