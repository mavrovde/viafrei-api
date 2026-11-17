package roads_test

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

func TestAWatchRequestIsReadAsThePlatformReadsIt(t *testing.T) {
	for _, c := range []struct {
		road         bool
		request, key string
		want         roads.WatchAsk
	}{
		{true, "Sag mir Bescheid, wenn sich ein Stau bildet", "A102", roads.AskJamOnly},
		{true, "", "Stau A7", roads.AskJamOnly},
		{false, "", "Stau", roads.AskPlain},
		{true, "Feierabendstau oder Sperrung?", "A1", roads.AskJamAndWatchable},
		{true, "traffic jam or road closed", "A1", roads.AskJamAndWatchable},
		{true, "Staufen", "B31", roads.AskPlain},
		{true, "zähfließender Verkehr", "A3", roads.AskJamOnly},
		{true, "", "", roads.AskPlain},
	} {
		if got := roads.AskOf(c.road, c.request, c.key); got != c.want {
			t.Errorf("%q %q: %s, want %s", c.request, c.key, got, c.want)
		}
	}
	for _, c := range []struct {
		explicit []string
		ask      roads.WatchAsk
		want     []string
	}{
		{nil, roads.AskJamOnly, []string{"jams"}},
		{nil, roads.AskJamAndWatchable, []string{"restrictions", "jams"}},
		{nil, roads.AskPlain, nil},
		{[]string{"restrictions"}, roads.AskJamOnly, []string{"restrictions"}},
	} {
		if got := roads.WatchEvents(c.explicit, c.ask); !slices.Equal(got, c.want) {
			t.Errorf("%v %s: %v, want %v", c.explicit, c.ask, got, c.want)
		}
	}
}
