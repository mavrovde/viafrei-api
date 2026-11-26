package places

import (
	"slices"
	"testing"
	"time"
)

// The loops of the resolver end by their bound alone: a bound that breaks
// (a mutant of a loop condition, a counter or a break) never returns, and a
// test that only waits would run into Gremlins' timeout, 30 times the
// coverage run (#255). These tests give each loop its own deadline and
// assert the bound's effect on an input built to reach it, so a broken
// bound fails within two seconds. The tests that reach the loops through
// the resolver call it under returnsWithin too, so whichever runs first
// fails at the deadline, whatever the order of the files.

const loopDeadline = 2 * time.Second

// returnsWithin fails the test when f does not return within loopDeadline.
func returnsWithin(t *testing.T, what string, f func()) {
	t.Helper()
	if !finishes(loopDeadline, f) {
		t.Fatalf("%s did not return within %s: its loop bound is broken", what, loopDeadline)
	}
}

// finishes reports whether f returns within the deadline. A loop that never
// ends keeps its goroutine; -failfast then ends the test binary.
func finishes(deadline time.Duration, f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(deadline):
		return false
	}
}

func TestFinishesTellsALoopThatNeverEnds(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	if finishes(10*time.Millisecond, func() { <-stop }) {
		t.Error("a loop that never ends finished")
	}
	if !finishes(time.Second, func() {}) {
		t.Error("a loop that ends did not finish")
	}
}

func ptr(s string) *string { return &s }

// separatingTier tries the tiers below the deepest of the group
// (resolve_labels.go, `t < maxDepth`): the first that tells the rows apart,
// and none when the rows have no tier at all.
func TestSeparatingTierStopsAtTheDeepestTier(t *testing.T) {
	apart := []distinguishable{{name: "Au", tiers: []*string{ptr("Bayern"), ptr("Freising")}}, {name: "Au", tiers: []*string{ptr("Bayern"), ptr("Kelheim")}}}
	bare := []distinguishable{{name: "Au"}, {name: "Au"}}
	var got, none int
	returnsWithin(t, "separatingTier", func() {
		got = separatingTier(apart, []int{0, 1}, map[int]bool{})
		none = separatingTier(bare, []int{0, 1}, map[int]bool{})
	})
	if got != 1 || none != -1 {
		t.Errorf("separating tier %d (want 1), of rows without a tier %d (want -1)", got, none)
	}
}

// labelReadings reads a label back with every subset of the row's tiers
// (resolve_labels.go, `mask < 1<<len(tiers)`): 2^n readings, each once.
func TestLabelReadingsAreEverySubsetOfTheTiers(t *testing.T) {
	row := distinguishable{name: "Au", tiers: []*string{ptr("Bayern"), ptr("Freising")}}
	var got []string
	returnsWithin(t, "labelReadings", func() { got = labelReadings(row) })
	if want := []string{"Au", "Au (Bayern)", "Au (Freising)", "Au (Bayern, Freising)"}; !slices.Equal(got, want) {
		t.Errorf("readings %q, want %q", got, want)
	}
}

// answerablePass gives an unkeyed row its own tier in the first round only
// (resolve_labels.go, `round <= depth`): a tier without a value leaves the
// label as it was, and a later round must not spend it again.
func TestAnswerablePassSpendsAnOwnTierOnce(t *testing.T) {
	own := 0
	rows := []distinguishable{{name: "Au", tiers: []*string{nil}, unkeyed: true, ownTier: &own}}
	spent := make([][]int, len(rows))
	returnsWithin(t, "answerablePass", func() { answerablePass(rows, spent, nil) })
	if !slices.Equal(spent[0], []int{0}) {
		t.Errorf("spent %v, want [0]", spent[0])
	}
}

// withoutStationWord replaces until nothing changes (resolve.go, the break
// of its `for {}`): a run of station words takes more than one pass.
func TestWithoutStationWordEndsWhenNothingChanges(t *testing.T) {
	var got *string
	returnsWithin(t, "withoutStationWord", func() { got = withoutStationWord("Altdorf Bahnhof Bf Station") })
	if got == nil || *got != "Altdorf" {
		t.Errorf("got %v, want Altdorf", got)
	}
}
