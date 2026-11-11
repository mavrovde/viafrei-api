package admission_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// clock is a settable clock: the controller reads it for arrival times,
// aging and service times; the waits themselves use real timers.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

// newClock starts at the real time, so a deadline taken from it is a real
// deadline too.
func newClock() *clock { return &clock{t: time.Now()} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// settings are permits permits, every tier with a queue of 8 and a wait of
// 300 ms, no reservations, no bulk cap below the permits.
func settings(permits int) admission.Settings {
	t := admission.TierSettings{QueueLen: 8, MaxWait: 300 * time.Millisecond, MaxInflight: permits}
	return admission.Settings{
		Permits: permits, Tiers: [3]admission.TierSettings{t, t, t},
		PerKey: permits, AgingAfter: time.Hour, Alpha: 0.5,
	}
}

func as(tier admission.Tier, key string) context.Context {
	return admission.WithCaller(context.Background(), admission.Caller{Tier: tier, Key: key})
}

func acquire(t *testing.T, c *admission.Controller, ctx context.Context) func() {
	t.Helper()
	release, err := c.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	return release
}

// waitUntil polls cond for at most one second.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within 1 s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type result struct {
	name    string
	release func()
	err     error
}

// queue starts an Acquire for ctx in the background and waits until it is
// queued.
func queue(t *testing.T, c *admission.Controller, ctx context.Context, name string, out chan<- result) {
	t.Helper()
	before := waiting(c)
	go func() {
		r, err := c.Acquire(ctx)
		out <- result{name, r, err}
	}()
	waitUntil(t, name+" queued", func() bool { return waiting(c) > before })
}

func waiting(c *admission.Controller) int {
	s := c.Snapshot()
	return s.Waiting[0] + s.Waiting[1] + s.Waiting[2]
}

func TestTierNames(t *testing.T) {
	req.Covers(t, "RATE-009")
	for _, tier := range []admission.Tier{admission.Critical, admission.Standard, admission.Bulk} {
		if admission.TierNamed(tier.String()) != tier {
			t.Errorf("%v does not round-trip", tier)
		}
	}
	if admission.TierNamed("gold") != admission.Bulk {
		t.Error("an unknown tier is not bulk")
	}
}

func TestAPermitIsGrantedAndReleasedOnce(t *testing.T) {
	req.Covers(t, "RATE-008")
	clk := newClock()
	c := admission.New(settings(2), clk.now)
	ctx := admission.WithTally(as(admission.Standard, "k"))
	release := acquire(t, c, ctx)
	if s := c.Snapshot(); s.Free != 1 || s.Inflight[admission.Standard] != 1 {
		t.Fatalf("after acquire: %+v", s)
	}
	clk.advance(40 * time.Millisecond)
	release()
	release()
	if s := c.Snapshot(); s.Free != 2 || s.Inflight[admission.Standard] != 0 || s.Average != 40*time.Millisecond {
		t.Fatalf("after release: %+v", s)
	}
	if got := admission.StorageTime(ctx); got != 40*time.Millisecond {
		t.Fatalf("storage time %v", got)
	}
	// The moving average weighs the newest service time by alpha.
	release = acquire(t, c, ctx)
	clk.advance(80 * time.Millisecond)
	release()
	if s := c.Snapshot(); s.Average != 60*time.Millisecond {
		t.Fatalf("average %v, want 60ms", s.Average)
	}
	if got := admission.StorageTime(ctx); got != 120*time.Millisecond {
		t.Fatalf("storage time adds up: %v", got)
	}
	if admission.StorageTime(context.Background()) != 0 {
		t.Fatal("storage time without a tally")
	}
	// A read without a caller is critical and under no key.
	release = acquire(t, c, context.Background())
	if s := c.Snapshot(); s.Inflight[admission.Critical] != 1 {
		t.Fatalf("a read without a caller: %+v", s)
	}
	release()
}

func TestAReleasedPermitGoesToTheHighestTierFirstInFirstOut(t *testing.T) {
	req.Covers(t, "RATE-009", "RATE-016")
	c := admission.New(settings(1), newClock().now)
	hold := acquire(t, c, as(admission.Standard, "a"))
	got := make(chan result, 4)
	queue(t, c, as(admission.Bulk, "b"), "bulk", got)
	queue(t, c, as(admission.Standard, "s1"), "standard 1", got)
	queue(t, c, as(admission.Standard, "s2"), "standard 2", got)
	queue(t, c, as(admission.Critical, "c"), "critical", got)
	hold()
	for _, want := range []string{"critical", "standard 1", "standard 2", "bulk"} {
		r := <-got
		if r.err != nil || r.name != want {
			t.Fatalf("served %s (%v), want %s", r.name, r.err, want)
		}
		r.release()
	}
}

func TestReservationsKeepPermitsForHigherTiers(t *testing.T) {
	req.Covers(t, "RATE-009")
	s := settings(2)
	s.Tiers[admission.Critical].Reserved = 1
	s.Tiers[admission.Standard].MaxWait = 30 * time.Millisecond
	c := admission.New(s, newClock().now)
	first := acquire(t, c, as(admission.Standard, "a"))
	// One permit is free, but it is critical's reservation.
	_, err := c.Acquire(as(admission.Standard, "b"))
	var refusal *admission.Refusal
	if !errors.As(err, &refusal) || !errors.Is(err, admission.ErrOverloaded) || err.Error() != admission.ErrOverloaded.Error() {
		t.Fatalf("standard took the reserved permit: %v", err)
	}
	crit := acquire(t, c, as(admission.Critical, "c"))
	defer crit()
	// While critical holds its reservation, nothing more is kept for it: a
	// released permit is standard's again.
	first()
	second := acquire(t, c, as(admission.Standard, "b"))
	second()
}

func TestTheBulkCapAndThePerKeyCapHoldWithFreePermits(t *testing.T) {
	req.Covers(t, "RATE-009", "RATE-016")
	s := settings(3)
	s.Tiers[admission.Bulk].MaxInflight = 1
	s.PerKey = 1
	s.Tiers[admission.Bulk].MaxWait = 20 * time.Millisecond
	c := admission.New(s, newClock().now)
	bulk := acquire(t, c, as(admission.Bulk, "a"))
	if _, err := c.Acquire(as(admission.Bulk, "b")); !errors.Is(err, admission.ErrOverloaded) {
		t.Fatalf("a second bulk read passed the cap: %v", err)
	}
	bulk()
	// The per-key cap: key a's second read waits while b is served.
	first := acquire(t, c, as(admission.Standard, "a"))
	got := make(chan result, 1)
	queue(t, c, as(admission.Standard, "a"), "a again", got)
	other := acquire(t, c, as(admission.Standard, "b"))
	other()
	if s := c.Snapshot(); s.Waiting[admission.Standard] != 1 {
		t.Fatalf("key a's second read did not wait: %+v", s)
	}
	first()
	r := <-got
	if r.err != nil || r.name != "a again" {
		t.Fatalf("key a's second read: %v", r.err)
	}
	r.release()
	if s := c.Snapshot(); s.Free != 3 {
		t.Fatalf("permits leaked: %+v", s)
	}
}

func TestAnOldWaiterIsPromotedOneTier(t *testing.T) {
	req.Covers(t, "RATE-010")
	clk := newClock()
	s := settings(1)
	s.AgingAfter = 100 * time.Millisecond
	c := admission.New(s, clk.now)
	hold := acquire(t, c, as(admission.Critical, "x"))
	got := make(chan result, 3)
	queue(t, c, as(admission.Bulk, "old bulk"), "old bulk", got)
	clk.advance(100 * time.Millisecond)
	// The bulk waiter now counts as standard and is older than this one.
	queue(t, c, as(admission.Standard, "new standard"), "new standard", got)
	queue(t, c, as(admission.Critical, "critical"), "critical", got)
	hold()
	for _, want := range []string{"critical", "old bulk", "new standard"} {
		r := <-got
		if r.err != nil || r.name != want {
			t.Fatalf("served %s, want %s", r.name, want)
		}
		r.release()
	}
}

func TestAWaiterBelowTheAgingThresholdIsNotPromoted(t *testing.T) {
	req.Covers(t, "RATE-010")
	clk := newClock()
	s := settings(1)
	s.AgingAfter = 100 * time.Millisecond
	c := admission.New(s, clk.now)
	hold := acquire(t, c, as(admission.Critical, "x"))
	got := make(chan result, 2)
	queue(t, c, as(admission.Bulk, "bulk"), "bulk", got)
	clk.advance(99 * time.Millisecond)
	queue(t, c, as(admission.Standard, "standard"), "standard", got)
	hold()
	for _, want := range []string{"standard", "bulk"} {
		r := <-got
		if r.name != want {
			t.Fatalf("served %s, want %s", r.name, want)
		}
		r.release()
	}
}

func TestARequestIsRefusedAtOnceWhenItsWaitWillNotFit(t *testing.T) {
	req.Covers(t, "RATE-011", "RATE-007")
	clk := newClock()
	s := settings(1)
	s.Reserve = 50 * time.Millisecond
	s.Tiers[admission.Standard].MaxWait = time.Second
	c := admission.New(s, clk.now)
	// One read of 200 ms sets the moving average.
	r := acquire(t, c, as(admission.Standard, "a"))
	clk.advance(200 * time.Millisecond)
	r()
	hold := acquire(t, c, as(admission.Standard, "a"))
	defer hold()
	// Position 1 at 200 ms a read on one permit is 200 ms: a deadline of
	// 240 ms minus the reserve of 50 ms leaves 190 ms, so it is refused
	// without waiting.
	ctx, cancel := context.WithDeadline(as(admission.Standard, "b"), clk.now().Add(240*time.Millisecond))
	defer cancel()
	start := time.Now()
	_, err := c.Acquire(ctx)
	var refusal *admission.Refusal
	if !errors.As(err, &refusal) || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("not refused at once: %v after %v", err, time.Since(start))
	}
	// Retry-After is the drain of what waits now plus this request.
	if refusal.RetryAfter != 200*time.Millisecond {
		t.Fatalf("retry after %v", refusal.RetryAfter)
	}
	// A deadline inside the reserve is refused too, whatever the average.
	ctx2, cancel2 := context.WithDeadline(as(admission.Standard, "b"), clk.now().Add(50*time.Millisecond))
	defer cancel2()
	if _, err := c.Acquire(ctx2); !errors.Is(err, admission.ErrOverloaded) {
		t.Fatalf("a budget inside the reserve: %v", err)
	}
	// With room for the wait the request queues and times out at its
	// deadline instead.
	ctx3, cancel3 := context.WithDeadline(as(admission.Standard, "b"), clk.now().Add(300*time.Millisecond))
	defer cancel3()
	if _, err := c.Acquire(ctx3); !errors.Is(err, admission.ErrOverloaded) || time.Since(start) < 200*time.Millisecond {
		t.Fatalf("a request that fits did not wait out its budget: %v", err)
	}
}

func TestAFullQueueShedsAndTheWaitIsBounded(t *testing.T) {
	req.Covers(t, "RATE-012")
	s := settings(1)
	s.Tiers[admission.Bulk].QueueLen = 1
	s.Tiers[admission.Bulk].MaxWait = 50 * time.Millisecond
	c := admission.New(s, newClock().now)
	hold := acquire(t, c, as(admission.Critical, "x"))
	defer hold()
	got := make(chan result, 1)
	queue(t, c, as(admission.Bulk, "b1"), "b1", got)
	// The queue holds one: the second bulk request is shed at once.
	if _, err := c.Acquire(as(admission.Bulk, "b2")); !errors.Is(err, admission.ErrOverloaded) {
		t.Fatalf("over the queue: %v", err)
	}
	// The queued one is shed when its wait ends, and leaves the queue.
	r := <-got
	if !errors.Is(r.err, admission.ErrOverloaded) || r.release != nil {
		t.Fatalf("after its wait: %v", r.err)
	}
	if waiting(c) != 0 {
		t.Fatal("a shed waiter stayed in the queue")
	}
}

func TestARequestThatEndsWhileWaitingLeavesTheQueue(t *testing.T) {
	req.Covers(t, "RATE-011")
	c := admission.New(settings(1), newClock().now)
	hold := acquire(t, c, as(admission.Critical, "x"))
	ctx, cancel := context.WithCancel(as(admission.Standard, "a"))
	got := make(chan result, 1)
	queue(t, c, ctx, "a", got)
	cancel()
	r := <-got
	if !errors.Is(r.err, context.Canceled) || errors.Is(r.err, admission.ErrOverloaded) {
		t.Fatalf("a cancelled wait: %v", r.err)
	}
	hold()
	if s := c.Snapshot(); s.Free != 1 || waiting(c) != 0 {
		t.Fatalf("after a cancelled wait: %+v", s)
	}
}

func TestTierOfReadsTheCallersTier(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	if got := admission.TierOf(context.Background()); got != admission.Critical {
		t.Errorf("a read without a caller is admitted as %v", got)
	}
	if got := admission.TierOf(admission.WithCaller(context.Background(), admission.Caller{Tier: admission.Bulk, Key: "k"})); got != admission.Bulk {
		t.Errorf("a bulk caller reads as %v", got)
	}
}
