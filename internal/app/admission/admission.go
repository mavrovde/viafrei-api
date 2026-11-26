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
	// PerKey is the most permits one key of the tier's callers may hold,
	// in place of Settings.PerKey; 0 keeps Settings.PerKey. A caller's own
	// tier decides it, not the tier a long wait promoted it to.
	PerKey int
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
// critical and under no key's share (in-process admission; its shared slot
// is standard, SlotTierOf).
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey{}).(Caller)
	return c
}

// TierOf returns the tier the storage reads under ctx are admitted in: the
// caller's, or critical for a read without one (in-process admission; its
// shared slot is standard, SlotTierOf).
func TierOf(ctx context.Context) Tier { return callerFrom(ctx).Tier }

// SlotTierOf returns the tier the storage reads under ctx take a slot of the
// shared budget in (NFR-CAP-002): the caller's, or standard for a read
// without one. A service read with no caller (readiness, which anyone may
// ask for without a key, and background reads) must not take the slots the
// budget keeps for critical callers, which the other readers of the store
// rely on too.
func SlotTierOf(ctx context.Context) Tier {
	if c, ok := ctx.Value(callerKey{}).(Caller); ok {
		return c.Tier
	}
	return Standard
}

type tallyKey struct{}

// Tally is what the reads of one request spent: their permit-held time
// (storage), the time they waited for one, and the named statement
// a read had started last when the request's deadline cut it.
type Tally struct {
	storage atomic.Int64
	wait    atomic.Int64

	mu  sync.Mutex
	cut string
}

// TallyOf is the tally of ctx (WithTally), or nil; a nil tally reads zero.
func TallyOf(ctx context.Context) *Tally {
	t, _ := ctx.Value(tallyKey{}).(*Tally)
	return t
}

// Wait is the time the reads waited for a permit, granted or not.
func (t *Tally) Wait() time.Duration {
	if t == nil {
		return 0
	}
	return time.Duration(t.wait.Load())
}

// Storage is the permit-held time, summed over the request's reads: it
// includes opening each transaction, taking its slot and the Go work inside
// the read, and exceeds the request's duration when reads run at the same
// time.
func (t *Tally) Storage() time.Duration {
	if t == nil {
		return 0
	}
	return time.Duration(t.storage.Load())
}

// Cut is the statement a deadline cut (NoteCut), or "".
func (t *Tally) Cut() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cut
}

// WithTally returns ctx counting the storage time of its reads, for usage
// metering (RATE-014), their wait for a permit and the statement a deadline
// cut, for the request's log line. A ctx that already counts keeps its
// tally, so the log line and the metering read the same one.
func WithTally(ctx context.Context) context.Context {
	if TallyOf(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, tallyKey{}, new(Tally))
}

// StorageTime is the time the reads under ctx held a permit; zero without a
// tally.
func StorageTime(ctx context.Context) time.Duration { return TallyOf(ctx).Storage() }

// AdmissionWait is the time the reads under ctx waited for a permit, granted
// or not; zero without a tally.
func AdmissionWait(ctx context.Context) time.Duration { return TallyOf(ctx).Wait() }

// NoteCut records name as the statement a read under ctx was running when
// the request's deadline cut it; the first one noted is kept. An empty name
// (no statement had started) records nothing.
func NoteCut(ctx context.Context, name string) {
	t := TallyOf(ctx)
	if t == nil || name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cut == "" {
		t.cut = name
	}
}

// CutStatement is the statement NoteCut recorded under ctx, or "".
func CutStatement(ctx context.Context) string { return TallyOf(ctx).Cut() }

func addStorageTime(ctx context.Context, d time.Duration) {
	if t := TallyOf(ctx); t != nil {
		t.storage.Add(int64(d))
	}
}

func addWait(ctx context.Context, d time.Duration) {
	if t := TallyOf(ctx); t != nil {
		t.wait.Add(int64(d))
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

// AcquireRead waits for a permit for the caller in ctx. It answers a
// *Refusal (errors.Is ErrOverloaded) when the request is shed, or ctx's
// error when the request ends while waiting. release must be called exactly
// once when the read is over, with whether storage answered it: only an
// answered read's time enters the moving average the wait estimate uses
// (RATE-011). A read cut at its deadline, or one whose transaction could not
// be opened, measured the deadline or the failure, not storage: counted, it
// would keep the estimate at the deadline after storage has recovered and
// shed healthy requests at once.
func (c *Controller) AcquireRead(ctx context.Context) (release func(answered bool), err error) {
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
	addWait(ctx, c.now().Sub(w.at))
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

// keyShare is the most permits one key of tier t may hold.
func (c *Controller) keyShare(t Tier) int {
	if n := c.s.Tiers[t].PerKey; n > 0 {
		return n
	}
	return c.s.PerKey
}

// eligible reports whether w may take a free permit now.
func (c *Controller) eligible(w *waiter, now time.Time) bool {
	t := w.caller.Tier
	if c.inflight[t] >= c.s.Tiers[t].MaxInflight {
		return false
	}
	if w.caller.Key != "" && c.perKey[w.caller.Key] >= c.keyShare(t) {
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
// request's tally and, for a read storage answered, the moving average, and
// the permit goes to the next waiter.
func (c *Controller) releaser(ctx context.Context, w *waiter) func(answered bool) {
	start := c.now()
	var once sync.Once
	return func(answered bool) {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			now := c.now()
			took := now.Sub(start)
			addStorageTime(ctx, took)
			switch {
			case !answered:
			case c.ewma == 0:
				c.ewma = took
			default:
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

// Saturated reports whether every slot is taken and a read waits for one:
// the pressure word the site's shell reads (the platform's pressure route).
func (c *Controller) Saturated() bool {
	s := c.Snapshot()
	waiting := 0
	for _, n := range s.Waiting {
		waiting += n
	}
	return s.Free == 0 && waiting > 0
}
