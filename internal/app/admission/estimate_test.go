package admission_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestTheWaitEstimateCountsOnlyTheWaitersServedFirst pins the estimate of
// RATE-011 to its numbers: two permits, a moving average of 100 ms, so each
// waiter served before a request adds 50 ms. Two bulk waiters and one
// standard waiter are queued; a standard request is behind the standard one
// only, a critical request behind none. A budget equal to the estimate
// waits; one millisecond less is refused at once, with the drain of every
// waiter as its Retry-After.
func TestTheWaitEstimateCountsOnlyTheWaitersServedFirst(t *testing.T) {
	req.Covers(t, "RATE-011", "RATE-007")
	clk := newClock()
	s := settings(2)
	s.Alpha = 1
	for i := range s.Tiers {
		s.Tiers[i].MaxWait = time.Second
	}
	c := admission.New(s, clk.now)
	r := acquire(t, c, as(admission.Standard, "a"))
	clk.advance(100 * time.Millisecond)
	r()
	holdA := acquire(t, c, as(admission.Standard, "a"))
	holdB := acquire(t, c, as(admission.Standard, "b"))
	bg, end := context.WithCancel(context.Background())
	got := make(chan result, 8)
	for _, w := range []struct {
		tier admission.Tier
		key  string
	}{{admission.Bulk, "c"}, {admission.Bulk, "d"}, {admission.Standard, "e"}} {
		queue(t, c, admission.WithCaller(bg, admission.Caller{Tier: w.tier, Key: w.key}), w.key, got)
	}
	defer func() { end(); holdA(); holdB() }()

	probe := func(tier admission.Tier, budget time.Duration) (refused *admission.Refusal) {
		t.Helper()
		ctx, cancel := context.WithDeadline(admission.WithCaller(bg, admission.Caller{Tier: tier, Key: "p"}), clk.now().Add(budget))
		before := waiting(c)
		done := make(chan error, 1)
		go func() {
			release, err := c.Acquire(ctx)
			if release != nil {
				release()
			}
			done <- err
		}()
		// Either it is refused at once, or it queues; a queued probe is
		// ended at once.
		deadline := time.Now().Add(time.Second)
		for {
			select {
			case err := <-done:
				cancel()
				if !errors.As(err, &refused) {
					t.Fatalf("tier %v, budget %v: %v", tier, budget, err)
				}
				return refused
			default:
			}
			if waiting(c) > before {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("tier %v, budget %v, queued: %v", tier, budget, err)
				}
				return nil
			}
			if time.Now().After(deadline) {
				t.Fatal("the probe neither queued nor was refused within 1 s")
			}
			time.Sleep(time.Millisecond)
		}
	}
	// Standard: one waiter ahead, position 2, 2 × 50 ms.
	if r := probe(admission.Standard, 100*time.Millisecond); r != nil {
		t.Fatalf("a budget equal to the estimate was refused: %v", r.RetryAfter)
	}
	r2 := probe(admission.Standard, 99*time.Millisecond)
	if r2 == nil {
		t.Fatal("a budget below the estimate was queued")
	}
	// Three waiting plus this one, 4 × 50 ms.
	if r2.RetryAfter != 200*time.Millisecond {
		t.Fatalf("retry after %v", r2.RetryAfter)
	}
	// Critical: nobody ahead, position 1, 50 ms.
	if r := probe(admission.Critical, 50*time.Millisecond); r != nil {
		t.Fatalf("critical behind lower tiers: %v", r.RetryAfter)
	}
	if r := probe(admission.Critical, 49*time.Millisecond); r == nil {
		t.Fatal("a critical budget below the estimate was queued")
	}
	// Bulk: all three ahead, position 4, 200 ms.
	if r := probe(admission.Bulk, 200*time.Millisecond); r != nil {
		t.Fatalf("bulk at its estimate: %v", r.RetryAfter)
	}
	if r := probe(admission.Bulk, 199*time.Millisecond); r == nil {
		t.Fatal("a bulk budget below the estimate was queued")
	}
}
