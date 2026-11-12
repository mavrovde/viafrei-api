// Package breaker is the circuit breaker in front of storage (NFR-RES-002).
//
// Closed, it lets every read through and counts consecutive failures: after
// Settings.FailureThreshold of them within Settings.Window it opens. Open, it
// refuses every read at once, without touching storage, for Settings.OpenFor.
// Then it is half-open: it lets Settings.HalfOpenProbes trial reads through
// and refuses the rest; the first trial that succeeds closes it, the first
// that fails opens it again.
//
// A read reports its outcome through the Ticket it was given. An outcome
// reported after the breaker changed state since the ticket was issued is
// ignored, so a slow read that started while it was closed cannot close it
// again while a trial is running, nor reopen it.
package breaker

import (
	"sync"
	"time"
)

// State is the breaker's state.
type State int

const (
	// Closed lets every read through.
	Closed State = iota
	// Open refuses every read.
	Open
	// HalfOpen lets a limited number of trial reads through.
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	default:
		return "half-open"
	}
}

// Outcome is what a read reports.
type Outcome int

const (
	// Ignored: the read never reached storage (it was refused before), or its
	// caller went away; it says nothing about storage.
	Ignored Outcome = iota
	// Success: storage answered.
	Success
	// Failure: storage could not be reached or failed.
	Failure
)

// Settings are the breaker.* keys.
type Settings struct {
	FailureThreshold int
	Window           time.Duration
	OpenFor          time.Duration
	HalfOpenProbes   int
}

// Refusal is the error of a read the breaker refused. RetryAfter is the
// time until the breaker admits a trial read: what is left of the open
// period, or zero while the trials are running.
type Refusal struct {
	State      State
	RetryAfter time.Duration
}

func (r *Refusal) Error() string { return "breaker: storage circuit is " + r.State.String() }

// Breaker is safe for concurrent use.
type Breaker struct {
	settings Settings
	now      func() time.Time
	onChange func(Change)

	mu         sync.Mutex
	state      State
	generation uint64
	failures   int
	firstFail  time.Time
	openedAt   time.Time
	trials     int
	counts     Snapshot
}

// Change is one change of state. Generation counts the changes since the
// breaker was built: the listener is called outside the lock, so two
// changes in quick succession may reach it out of order, and the
// generation is what orders them.
type Change struct {
	From, To   State
	Generation uint64
}

// Snapshot is the breaker's state and counters since it was built, for
// operators and tests, like the admission controller's.
type Snapshot struct {
	State State
	// Opened counts the times it opened, a failed trial's reopening included.
	Opened int64
	// Refused counts the reads refused without asking storage.
	Refused int64
	// Trials counts the trial reads admitted while half-open.
	Trials int64
}

// Snapshot returns the current state and counters.
func (b *Breaker) Snapshot() Snapshot {
	b.mu.Lock()
	ch := b.expire()
	snap := b.counts
	snap.State = b.state
	b.mu.Unlock()
	b.changed(ch)
	return snap
}

// New returns a closed breaker. now is the clock (time.Now when nil);
// onChange, when not nil, is called on every change of state, outside the
// breaker's lock.
func New(s Settings, now func() time.Time, onChange func(Change)) *Breaker {
	if now == nil {
		now = time.Now
	}
	return &Breaker{settings: s, now: now, onChange: onChange}
}

// State returns the current state; an open breaker whose period is over is
// reported half-open.
func (b *Breaker) State() State {
	b.mu.Lock()
	ch := b.expire()
	st := b.state
	b.mu.Unlock()
	b.changed(ch)
	return st
}

// Ticket is one admitted read.
type Ticket struct {
	b          *Breaker
	generation uint64
	trial      bool
}

// Allow admits a read or refuses it with a *Refusal.
func (b *Breaker) Allow() (Ticket, error) {
	b.mu.Lock()
	ch := b.expire()
	t, err := b.allow()
	b.mu.Unlock()
	b.changed(ch)
	return t, err
}

func (b *Breaker) allow() (Ticket, error) {
	switch b.state {
	case Open:
		b.counts.Refused++
		return Ticket{}, &Refusal{State: Open, RetryAfter: b.openedAt.Add(b.settings.OpenFor).Sub(b.now())}
	case HalfOpen:
		if b.trials >= b.settings.HalfOpenProbes {
			b.counts.Refused++
			return Ticket{}, &Refusal{State: HalfOpen}
		}
		b.trials++
		b.counts.Trials++
		return Ticket{b: b, generation: b.generation, trial: true}, nil
	default:
		return Ticket{b: b, generation: b.generation}, nil
	}
}

// Done reports the read's outcome. It may be called once per ticket.
func (t Ticket) Done(o Outcome) {
	b := t.b
	b.mu.Lock()
	var ch *Change
	if t.generation == b.generation {
		ch = b.done(t.trial, o)
	}
	b.mu.Unlock()
	b.changed(ch)
}

func (b *Breaker) done(trial bool, o Outcome) *Change {
	now := b.now()
	switch {
	case trial && o == Ignored:
		b.trials-- // the trial slot goes to another read
	case trial && o == Success:
		return b.set(Closed, now)
	case o == Failure && (trial || b.count(now)):
		return b.set(Open, now)
	case o == Success:
		b.failures = 0
	}
	return nil
}

// count records a failure while closed and reports whether the threshold
// is reached: a failure after the window since the first of the run starts
// a new run.
func (b *Breaker) count(now time.Time) bool {
	if b.failures == 0 || now.Sub(b.firstFail) > b.settings.Window {
		b.failures, b.firstFail = 0, now
	}
	b.failures++
	return b.failures >= b.settings.FailureThreshold
}

// expire turns an open breaker whose period is over into a half-open one.
func (b *Breaker) expire() *Change {
	if b.state == Open && !b.now().Before(b.openedAt.Add(b.settings.OpenFor)) {
		return b.set(HalfOpen, b.now())
	}
	return nil
}

func (b *Breaker) set(s State, now time.Time) *Change {
	from := b.state
	b.state = s
	b.generation++
	b.failures, b.trials = 0, 0
	if s == Open {
		b.openedAt = now
		b.counts.Opened++
	}
	return &Change{From: from, To: s, Generation: b.generation}
}

// changed calls onChange for a change of state, if there was one.
func (b *Breaker) changed(ch *Change) {
	if ch != nil && b.onChange != nil {
		b.onChange(*ch)
	}
}
