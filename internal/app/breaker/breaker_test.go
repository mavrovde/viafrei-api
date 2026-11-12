package breaker_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/breaker"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

var settings = breaker.Settings{FailureThreshold: 3, Window: 30 * time.Second, OpenFor: 15 * time.Second, HalfOpenProbes: 2}

type changes struct {
	mu   sync.Mutex
	seen []string
	gens []uint64
}

func (c *changes) record(ch breaker.Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, ch.From.String()+">"+ch.To.String())
	c.gens = append(c.gens, ch.Generation)
}

func newBreaker() (*breaker.Breaker, *clock, *changes) {
	c := &clock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	ch := &changes{}
	return breaker.New(settings, c.now, ch.record), c, ch
}

func admit(t *testing.T, b *breaker.Breaker) *breaker.Ticket {
	t.Helper()
	tk, err := b.Allow()
	if err != nil {
		t.Fatalf("Allow refused: %v", err)
	}
	return tk
}

func refusal(t *testing.T, b *breaker.Breaker) *breaker.Refusal {
	t.Helper()
	_, err := b.Allow()
	var r *breaker.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("Allow admitted, want a refusal")
	}
	return r
}

func fail(t *testing.T, b *breaker.Breaker, n int) {
	t.Helper()
	for range n {
		admit(t, b).Done(breaker.Failure)
	}
}

func TestOpensAfterTheThresholdOfConsecutiveFailures(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	b, c, ch := newBreaker()
	fail(t, b, 2)
	if b.State() != breaker.Closed {
		t.Fatal("open below the threshold")
	}
	fail(t, b, 1)
	if b.State() != breaker.Open {
		t.Fatal("closed at the threshold")
	}
	c.advance(5 * time.Second)
	r := refusal(t, b)
	if r.State != breaker.Open || r.RetryAfter != 10*time.Second {
		t.Fatalf("refusal %+v, want open with 10s left", r)
	}
	if r.Error() != "breaker: storage circuit is open" {
		t.Fatalf("message %q", r.Error())
	}
	if len(ch.seen) != 1 || ch.seen[0] != "closed>open" {
		t.Fatalf("changes %v", ch.seen)
	}
}

func TestASuccessResetsTheRun(t *testing.T) {
	b, _, _ := newBreaker()
	fail(t, b, 2)
	admit(t, b).Done(breaker.Success)
	fail(t, b, 2)
	if b.State() != breaker.Closed {
		t.Fatal("a success did not reset the consecutive failures")
	}
}

func TestIgnoredOutcomesCountForNothing(t *testing.T) {
	b, _, _ := newBreaker()
	fail(t, b, 2)
	admit(t, b).Done(breaker.Ignored)
	fail(t, b, 1)
	if b.State() != breaker.Open {
		t.Fatal("an ignored read broke the run of failures")
	}
}

func TestFailuresOutsideTheWindowStartANewRun(t *testing.T) {
	b, c, _ := newBreaker()
	fail(t, b, 2)
	c.advance(30 * time.Second) // at the window's edge: still the same run
	fail(t, b, 1)
	if b.State() != breaker.Open {
		t.Fatal("a failure at the window's edge started a new run")
	}

	b, c, _ = newBreaker()
	fail(t, b, 2)
	c.advance(30*time.Second + time.Millisecond)
	fail(t, b, 2)
	if b.State() != breaker.Closed {
		t.Fatal("failures spread over more than the window opened it")
	}
	fail(t, b, 1)
	if b.State() != breaker.Open {
		t.Fatal("the new run did not count")
	}
}

func TestHalfOpenAdmitsTheConfiguredTrials(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	b, c, ch := newBreaker()
	fail(t, b, 3)
	c.advance(15 * time.Second)
	first := admit(t, b)
	second := admit(t, b)
	r := refusal(t, b)
	if r.State != breaker.HalfOpen || r.RetryAfter != 0 {
		t.Fatalf("refusal %+v, want half-open with no wait", r)
	}
	if b.State() != breaker.HalfOpen {
		t.Fatal("not half-open after the open period")
	}
	// An ignored trial gives its slot to another read.
	first.Done(breaker.Ignored)
	third := admit(t, b)
	_ = refusal(t, b)
	third.Done(breaker.Success)
	if b.State() != breaker.Closed {
		t.Fatal("a successful trial did not close it")
	}
	// The other trial's outcome is from before the change: ignored.
	second.Done(breaker.Failure)
	if b.State() != breaker.Closed {
		t.Fatal("a stale trial reopened it")
	}
	// Two trials, then a third after the ignored one; two reads refused.
	if snap := b.Snapshot(); snap != (breaker.Snapshot{State: breaker.Closed, Opened: 1, Refused: 2, Trials: 3}) {
		t.Fatalf("snapshot %+v", snap)
	}
	want := []string{"closed>open", "open>half-open", "half-open>closed"}
	if len(ch.seen) != len(want) {
		t.Fatalf("changes %v, want %v", ch.seen, want)
	}
	for i := range want {
		if ch.seen[i] != want[i] {
			t.Fatalf("changes %v, want %v", ch.seen, want)
		}
	}
}

func TestAFailedTrialOpensItAgainForAWholePeriod(t *testing.T) {
	b, c, _ := newBreaker()
	fail(t, b, 3)
	c.advance(20 * time.Second)
	admit(t, b).Done(breaker.Failure)
	if b.State() != breaker.Open {
		t.Fatal("a failed trial did not reopen it")
	}
	if r := refusal(t, b); r.RetryAfter != 15*time.Second {
		t.Fatalf("retry after %v, want a whole period", r.RetryAfter)
	}
	c.advance(15 * time.Second)
	if snap := b.Snapshot(); snap != (breaker.Snapshot{State: breaker.HalfOpen, Opened: 2, Refused: 1, Trials: 1}) {
		t.Fatalf("snapshot %+v: the reopening counts, and the period's end shows as half-open", snap)
	}
}

func TestAStaleReadNeitherClosesNorReopens(t *testing.T) {
	b, c, _ := newBreaker()
	slow := admit(t, b)
	fail(t, b, 3)
	slow.Done(breaker.Success)
	if b.State() != breaker.Open {
		t.Fatal("a read from before the opening closed it")
	}
	c.advance(15 * time.Second)
	trial := admit(t, b)
	slow.Done(breaker.Failure)
	if b.State() != breaker.HalfOpen {
		t.Fatal("a read from before the opening reopened it")
	}
	trial.Done(breaker.Success)
	if b.State() != breaker.Closed {
		t.Fatal("the trial did not close it")
	}
}

func TestDefaultsAndStateNames(t *testing.T) {
	b := breaker.New(settings, nil, nil)
	for range 3 {
		tk, err := b.Allow()
		if err != nil {
			t.Fatal(err)
		}
		tk.Done(breaker.Failure)
	}
	if b.State() != breaker.Open {
		t.Fatal("a breaker on the real clock without a listener did not open")
	}
	for s, want := range map[breaker.State]string{breaker.Closed: "closed", breaker.Open: "open", breaker.HalfOpen: "half-open"} {
		if s.String() != want {
			t.Fatalf("%d is %q, want %q", s, s.String(), want)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	b, _, _ := newBreaker()
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() {
			if tk, err := b.Allow(); err == nil {
				tk.Done(breaker.Outcome(i % 3))
			}
		})
	}
	wg.Wait()
	if s := b.State(); s != breaker.Open && s != breaker.Closed {
		t.Fatalf("state %v after a mixed run on a closed breaker", s)
	}
}

// TestConcurrentTrialsAreBounded races many reads at a half-open breaker:
// exactly HalfOpenProbes are admitted, whatever the interleaving, and every
// other read is refused (criterion 2 under concurrency).
func TestConcurrentTrialsAreBounded(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	for round := range 20 {
		b, c, _ := newBreaker()
		fail(t, b, settings.FailureThreshold)
		c.advance(settings.OpenFor)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var held []*breaker.Ticket
		refused := 0
		start := make(chan struct{})
		for range 64 {
			wg.Go(func() {
				<-start
				tk, err := b.Allow()
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					refused++
					return
				}
				held = append(held, tk)
			})
		}
		close(start)
		wg.Wait()
		if len(held) != settings.HalfOpenProbes || refused != 64-settings.HalfOpenProbes {
			t.Fatalf("round %d: %d trials admitted and %d refused, want exactly %d admitted", round, len(held), refused, settings.HalfOpenProbes)
		}
		if snap := b.Snapshot(); snap.Trials != int64(settings.HalfOpenProbes) || snap.State != breaker.HalfOpen {
			t.Fatalf("round %d: snapshot %+v", round, snap)
		}
		held[0].Done(breaker.Success)
		held[1].Done(breaker.Failure) // stale: the breaker closed on the first
		if b.State() != breaker.Closed {
			t.Fatalf("round %d: a successful trial did not close the breaker", round)
		}
	}
}

func TestATicketCountsOnce(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	b, c, _ := newBreaker()
	fail(t, b, settings.FailureThreshold)
	c.advance(settings.OpenFor)
	first, second := admit(t, b), admit(t, b)
	// Reported twice, an ignored trial frees its slot once: one more read is
	// admitted, the next is refused.
	first.Done(breaker.Ignored)
	first.Done(breaker.Ignored)
	third := admit(t, b)
	_ = refusal(t, b)
	// A trial reported failed and then succeeded stays failed.
	second.Done(breaker.Failure)
	third.Done(breaker.Success)
	if b.State() != breaker.Open {
		t.Fatalf("breaker %v, want open after the failed trial", b.State())
	}
}
