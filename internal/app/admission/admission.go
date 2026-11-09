// Package admission is the priority admission in front of storage (Spec:
// Rate Limiting, Quotas and Priority, gate 4). Its permits equal the storage
// connection pool, so nothing ever waits inside the pool (RATE-008): every
// wait happens in a bounded queue per tier, where priority applies.
//
// A released permit goes to the highest non-empty tier, minus the permits
// higher tiers hold in reserve and within the bulk tier's in-flight cap
// (RATE-009). A waiter older than the aging threshold counts one tier higher
// (RATE-010). A request whose estimated wait does not fit what is left of
// its deadline is refused at once (RATE-011). Within a tier waiters are
// served first in, first out, and no key holds more than its share of
// permits (RATE-016).
package admission

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Tier is a priority tier; a smaller value is served first.
type Tier int

// The tiers, highest first.
const (
	Critical Tier = iota
	Standard
	Bulk
	tierCount
)

var tierNames = [tierCount]string{"critical", "standard", "bulk"}

// String returns the tier's name.
func (t Tier) String() string { return tierNames[t] }

// TierNamed returns the tier of a plan's tier name. A name it does not know
// is the bulk tier: the least a misconfigured plan can take.
func TierNamed(name string) Tier {
	for i, n := range tierNames {
		if n == name {
			return Tier(i)
		}
	}
	return Bulk
}

// TierSettings are one tier's queue and caps.
type TierSettings struct {
	// Reserved permits are kept free for this tier and the ones above it.
	Reserved int
	// QueueLen waiters of the tier may wait at once.
	QueueLen int
	// MaxWait is the longest a waiter of the tier waits.
	MaxWait time.Duration
	// MaxInflight is the most permits the tier may hold at once.
	MaxInflight int
}

// Settings configure a controller.
type Settings struct {
	// Permits equals the storage pool size (RATE-008).
	Permits int
	Tiers   [tierCount]TierSettings
	// PerKey is the most permits one key may hold (RATE-016).
	PerKey int
	// AgingAfter promotes a waiter one tier (RATE-010).
	AgingAfter time.Duration
	// Alpha weighs the newest service time in the moving average.
	Alpha float64
	// Reserve is kept back from the deadline: a wait must leave it free.
	Reserve time.Duration
}

// ErrOverloaded is the refusal of an overloaded controller; errors.As finds
// the *Refusal carrying the Retry-After estimate.
var ErrOverloaded = errors.New("admission: overloaded")

// Refusal is a shed request.
type Refusal struct {
	// RetryAfter estimates when the queues will have drained.
	RetryAfter time.Duration
}

func (r *Refusal) Error() string { return ErrOverloaded.Error() }

// Unwrap makes errors.Is(err, ErrOverloaded) hold.
func (r *Refusal) Unwrap() error { return ErrOverloaded }

// Caller is who asks for a permit: the tier and the key of the request.
type Caller struct {
	Tier Tier
	Key  string
}

type callerKey struct{}

// WithCaller returns ctx carrying the caller of its storage reads. A read
// without a caller (a service operation such as readiness) is admitted as
// critical and under no key's share.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey{}).(Caller)
	return c
}

type tallyKey struct{}

// WithTally returns ctx counting the storage time of its reads, for usage
// metering (RATE-014).
func WithTally(ctx context.Context) context.Context {
	return context.WithValue(ctx, tallyKey{}, new(atomic.Int64))
}

// StorageTime is the time the reads under ctx held a permit; zero without a
// tally.
func StorageTime(ctx context.Context) time.Duration {
	if t, ok := ctx.Value(tallyKey{}).(*atomic.Int64); ok {
		return time.Duration(t.Load())
	}
	return 0
}

func addStorageTime(ctx context.Context, d time.Duration) {
	if t, ok := ctx.Value(tallyKey{}).(*atomic.Int64); ok {
		t.Add(int64(d))
	}
}

type waiter struct {
	caller  Caller
	at      time.Time
	ready   chan struct{}
	granted bool
	elem    *list.Element
}

// Controller hands out the permits.
type Controller struct {
	s   Settings
	now func() time.Time

	mu       sync.Mutex
	free     int
	inflight [tierCount]int
	perKey   map[string]int
	queues   [tierCount]*list.List
	ewma     time.Duration
}

// New returns a controller with every permit free. now is the clock
// (time.Now when nil).
func New(s Settings, now func() time.Time) *Controller {
	if now == nil {
		now = time.Now
	}
	c := &Controller{s: s, now: now, free: s.Permits, perKey: map[string]int{}}
	for i := range c.queues {
		c.queues[i] = list.New()
	}
	return c
}

// Acquire waits for a permit for the caller in ctx. It answers a *Refusal
// (errors.Is ErrOverloaded) when the request is shed, or ctx's error when the
// request ends while waiting. release must be called exactly once when the
// read is over.
func (c *Controller) Acquire(ctx context.Context) (release func(), err error) {
	caller := callerFrom(ctx)
	ts := c.s.Tiers[caller.Tier]
	w := &waiter{caller: caller, ready: make(chan struct{})}

	c.mu.Lock()
	now := c.now()
	w.at = now
	ahead := c.ahead(caller.Tier, now)
	w.elem = c.queues[caller.Tier].PushBack(w)
	c.dispatch(now)
	if w.granted {
		c.mu.Unlock()
		return c.releaser(ctx, w), nil
	}
	budget := ts.MaxWait
	if d, ok := ctx.Deadline(); ok {
		budget = min(budget, d.Sub(now)-c.s.Reserve)
	}
	estimate := c.estimate(ahead + 1)
	if c.queues[caller.Tier].Len() > ts.QueueLen || budget <= 0 || estimate > budget {
		c.queues[caller.Tier].Remove(w.elem)
		r := c.refusal()
		c.mu.Unlock()
		return nil, r
	}
	c.mu.Unlock()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-w.ready:
	case <-timer.C:
	case <-ctx.Done():
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if w.granted {
		// Granted, possibly in the same moment the wait ended: the permit
		// is this request's either way.
		return c.releaser(ctx, w), nil
	}
	c.queues[caller.Tier].Remove(w.elem)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, c.refusal()
}

// ahead counts the waiters served before a new waiter of tier t: those
// whose effective tier is t or higher.
func (c *Controller) ahead(t Tier, now time.Time) int {
	n := 0
	for i := range c.queues {
		for e := c.queues[i].Front(); e != nil; e = e.Next() {
			if c.effective(e.Value.(*waiter), now) <= t {
				n++
			}
		}
	}
	return n
}

// estimate is the time n waiters take to be served by every permit at the
// moving average of service time (RATE-011).
func (c *Controller) estimate(n int) time.Duration {
	return time.Duration(n) * c.ewma / time.Duration(c.s.Permits)
}

// refusal is the answer to a shed request: Retry-After is the time the
// queues as they stand now take to drain.
func (c *Controller) refusal() *Refusal {
	waiting := 0
	for i := range c.queues {
		waiting += c.queues[i].Len()
	}
	return &Refusal{RetryAfter: c.estimate(waiting + 1)}
}

// effective is the tier a waiter counts as: one higher once it has waited
// longer than the aging threshold (RATE-010).
func (c *Controller) effective(w *waiter, now time.Time) Tier {
	if w.caller.Tier > Critical && now.Sub(w.at) >= c.s.AgingAfter {
		return w.caller.Tier - 1
	}
	return w.caller.Tier
}

// eligible reports whether w may take a free permit now.
func (c *Controller) eligible(w *waiter, now time.Time) bool {
	t := w.caller.Tier
	if c.inflight[t] >= c.s.Tiers[t].MaxInflight {
		return false
	}
	if w.caller.Key != "" && c.perKey[w.caller.Key] >= c.s.PerKey {
		return false
	}
	held := 0
	for above := Critical; above < c.effective(w, now); above++ {
		held += max(0, c.s.Tiers[above].Reserved-c.inflight[above])
	}
	return c.free > held
}

// dispatch hands free permits to waiters: the highest effective tier
// first, the oldest waiter first within it (RATE-009, RATE-016).
func (c *Controller) dispatch(now time.Time) {
	for c.free > 0 {
		var best *waiter
		for i := range c.queues {
			for e := c.queues[i].Front(); e != nil; e = e.Next() {
				w := e.Value.(*waiter)
				if !c.eligible(w, now) {
					continue
				}
				if best == nil || c.effective(w, now) < c.effective(best, now) ||
					(c.effective(w, now) == c.effective(best, now) && w.at.Before(best.at)) {
					best = w
				}
				break // the first eligible waiter of a queue is its oldest
			}
		}
		if best == nil {
			return
		}
		c.queues[best.caller.Tier].Remove(best.elem)
		c.free--
		c.inflight[best.caller.Tier]++
		c.perKey[best.caller.Key]++
		best.granted = true
		close(best.ready)
	}
}

// releaser returns the release of w's permit: the service time updates the
// moving average and the request's tally, and the permit goes to the next
// waiter.
func (c *Controller) releaser(ctx context.Context, w *waiter) func() {
	start := c.now()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			now := c.now()
			took := now.Sub(start)
			addStorageTime(ctx, took)
			if c.ewma == 0 {
				c.ewma = took
			} else {
				c.ewma = time.Duration(c.s.Alpha*float64(took) + (1-c.s.Alpha)*float64(c.ewma))
			}
			c.free++
			c.inflight[w.caller.Tier]--
			if c.perKey[w.caller.Key]--; c.perKey[w.caller.Key] == 0 {
				delete(c.perKey, w.caller.Key)
			}
			c.dispatch(now)
		})
	}
}

// Snapshot is the controller's state, for operators and tests.
type Snapshot struct {
	Free     int
	Inflight [tierCount]int
	Waiting  [tierCount]int
	Average  time.Duration
}

// Snapshot returns the current state.
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{Free: c.free, Inflight: c.inflight, Average: c.ewma}
	for i := range c.queues {
		s.Waiting[i] = c.queues[i].Len()
	}
	return s
}
