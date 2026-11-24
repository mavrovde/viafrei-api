package admission_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// faultSettings are the load stack's: four permits, the standard tier's
// queue_max_wait of 400 ms, ewma_alpha 0.2.
func faultSettings() admission.Settings {
	s := settings(4)
	s.Alpha = 0.2
	for i := range s.Tiers {
		s.Tiers[i].MaxWait = 400 * time.Millisecond
	}
	return s
}

// readFor holds a permit for d on clk and releases it as answered or not.
func readFor(t *testing.T, c *admission.Controller, clk *clock, d time.Duration, answered bool) {
	t.Helper()
	release, err := c.AcquireRead(as(admission.Standard, "k"))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	clk.advance(d)
	release(answered)
}

// queuedBehindFull: with every permit held, two requests arrive one after
// the other; it returns how many were shed at once.
func queuedBehindFull(t *testing.T, c *admission.Controller) int {
	t.Helper()
	var held []func()
	for range 4 {
		held = append(held, acquire(t, c, as(admission.Standard, "k")))
	}
	got := make(chan result, 2)
	shed := 0
	for _, name := range []string{"first", "second"} {
		before := waiting(c)
		go func() {
			r, err := c.AcquireRead(as(admission.Standard, name))
			got <- result{name, r, err}
		}()
		waitUntil(t, name+" queued or shed", func() bool { return waiting(c) > before || len(got) > 0 })
		select {
		case r := <-got:
			if !errors.Is(r.err, admission.ErrOverloaded) {
				t.Fatalf("%s: %v", name, r.err)
			}
			shed++
		default:
		}
	}
	for _, r := range held {
		r()
	}
	for range 2 - shed {
		r := <-got
		if r.err != nil {
			t.Fatalf("%s: %v", r.name, r.err)
		}
		r.release(true)
	}
	return shed
}

// TestReadsCutAtTheDeadlineLeaveTheEstimateAlone: a fault whose reads were
// cut at the deadline (2.1 s each, the permit held until the cancel landed)
// does not count; two answered probes later, a request queued on healthy
// storage is not shed at once (RATE-011, NFR-RES-007).
func TestReadsCutAtTheDeadlineLeaveTheEstimateAlone(t *testing.T) {
	req.Covers(t, "RATE-011", "NFR-RES-007")
	clk := newClock()
	c := admission.New(faultSettings(), clk.now)
	for range 10 {
		readFor(t, c, clk, 30*time.Millisecond, true)
	}
	before := c.Snapshot().Average
	for range 8 {
		readFor(t, c, clk, 2100*time.Millisecond, false)
	}
	if got := c.Snapshot().Average; got != before {
		t.Errorf("unanswered reads moved the average: %s, was %s", got, before)
	}
	readFor(t, c, clk, 30*time.Millisecond, true)
	readFor(t, c, clk, 30*time.Millisecond, true)
	if shed := queuedBehindFull(t, c); shed != 0 {
		t.Errorf("%d of 2 shed at once on healthy storage", shed)
	}
}

// TestAnsweredSlowReadsStillRaiseTheEstimate: slow reads storage answered
// are real service time, so a real overload is shed at once as before
// (RATE-011).
func TestAnsweredSlowReadsStillRaiseTheEstimate(t *testing.T) {
	req.Covers(t, "RATE-011")
	clk := newClock()
	c := admission.New(faultSettings(), clk.now)
	for range 10 {
		readFor(t, c, clk, 30*time.Millisecond, true)
	}
	for range 8 {
		readFor(t, c, clk, 2100*time.Millisecond, true)
	}
	if got := c.Snapshot().Average; got < time.Second {
		t.Errorf("answered slow reads left the average at %s", got)
	}
	if shed := queuedBehindFull(t, c); shed == 0 {
		t.Error("a real overload was not shed at once")
	}
}

// TestAQueuedRequestIsShedWithinQueueMaxWait: with an estimate that lets a
// request queue, a permit that never comes sheds it by queue_max_wait
// (RATE-011).
func TestAQueuedRequestIsShedWithinQueueMaxWait(t *testing.T) {
	req.Covers(t, "RATE-011")
	s := faultSettings()
	s.Tiers[admission.Standard].MaxWait = 50 * time.Millisecond
	c := admission.New(s, nil)
	var held []func()
	for range 4 {
		held = append(held, acquire(t, c, as(admission.Standard, "k")))
	}
	defer func() {
		for _, r := range held {
			r()
		}
	}()
	start := time.Now()
	if _, err := c.AcquireRead(as(admission.Standard, "late")); !errors.Is(err, admission.ErrOverloaded) {
		t.Fatalf("got %v", err)
	}
	if took := time.Since(start); took < 50*time.Millisecond || took > 50*time.Millisecond+200*time.Millisecond {
		t.Errorf("shed after %s, want queue_max_wait 50ms", took)
	}
	if _, err := c.AcquireRead(admission.WithCaller(cancelled(), admission.Caller{Tier: admission.Standard, Key: "gone"})); err == nil {
		t.Error("an ended request got a permit")
	}
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
