package roads_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The chain loop of a jam's sections (motorway.go, jamExtent: `for len(left)
// > 0`) ends because every pass either takes a section off the left ones or
// returns. A mutant of that bound (`next := +1`) keeps appending a section it
// already took and never returns, and a test that only waits runs into
// Gremlins' timeout, 30 times the coverage run (#255). The tests that reach
// the loop call it under returnsWithin, so a broken bound fails within two
// seconds, whatever the order of the files.

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

// A fork: the second section chains to the first, the third starts where the
// second starts, so after the second no left section touches the chain's
// end. The loop must give up there (no tail, no head), not run on.
func TestJamChainEndsWhereNoLeftSectionTouches(t *testing.T) {
	ext := []*string{extent("50,8,50.01,8"), extent("50.01,8,50.02,8"), extent("50.01,8,50.02,8.1")}
	r := roads.MotorwayEventRow{Kind: "warning", TrafficType: ptr("x"), SiteExtents: ext}
	var j *roads.JamDetail
	returnsWithin(t, "the chain of a forked jam", func() {
		j = roads.MotorwayAnswerOf(roads.MotorwayRead{Events: []roads.MotorwayEventRow{r}}, 0, at("2026-11-25T08:00:00Z")).Entries[0].Jam
	})
	if j.LengthM == nil || j.Tail != nil || j.Head != nil {
		t.Errorf("forked jam %+v", j)
	}
}
