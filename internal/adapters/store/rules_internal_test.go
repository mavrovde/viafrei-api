package store

import (
	"strings"
	"testing"
)

// trafficEventsOf matches the source rules once per family, not once per
// situation: a change to trafficEvents that moved or reworded the per-row
// match would leave it in place unreplaced, and the speed-up would be lost
// with every answer still the same.
func TestTrafficEventsOfMatchesTheRulesOncePerFamily(t *testing.T) {
	for _, in := range []string{"", "$3"} { // no Land, and a Land as messagesInState asks it
		sql := trafficEventsOf(in)
		if strings.Contains(sql, eventRulePerRow) {
			t.Errorf("%q: the per-situation rule match is still there", in)
		}
		if !strings.Contains(sql, rulesOfFamilies("b.source_ref")) {
			t.Errorf("%q: no rule match per family", in)
		}
		if !strings.Contains(trafficEvents(in), eventRulePerRow) {
			t.Errorf("%q: trafficEvents no longer holds the per-situation match it replaces", in)
		}
	}
}
