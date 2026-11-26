package store

import (
	"maps"
	"slices"
	"testing"
)

// landsOf answers the junction Länder as roadStates reads them (sorted, a
// road with none left out) only when the read is current and holds every
// road; else the lamps read them live.
func TestLandsOfAnswersTheWarmLaenderOnlyWhenEveryRoadIsThere(t *testing.T) {
	if got, ok := landsOf(nil, []string{"A1"}); ok || got != nil {
		t.Fatalf("no current read: %v %v, want nil false", got, ok)
	}
	lands := map[string]roadLands{
		"A1": {states: []string{"05", "03", "01"}},
		"A2": {states: []string{}},
	}
	if got, ok := landsOf(lands, []string{"A1", "A3"}); ok || got != nil {
		t.Fatalf("a road missing: %v %v, want nil false", got, ok)
	}
	got, ok := landsOf(lands, []string{"A1", "A2"})
	want := map[string][]string{"A1": {"01", "03", "05"}}
	if !ok || !maps.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("landsOf = %v %v, want %v true", got, ok, want)
	}
	if lands["A1"].states[0] != "05" {
		t.Fatalf("landsOf sorted the read in place: %v", lands["A1"].states)
	}
}
