// Package ratelimit runs the cheap gates in front of every operation (Spec:
// Rate Limiting, Quotas and Priority, gates 1 to 3): the per-address token
// bucket for requests without a key, and the per-key token bucket and the
// daily and monthly quota of an authenticated key. Every state is held in
// memory; a client address is never stored (RATE-015).
package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/limits"
)

// IPSettings configure the per-address limiter.
type IPSettings struct {
	// Rate is the bucket; Per 0 switches the limiter off.
	Rate limits.Rate
	// TTL is how long an idle address's state is kept.
	TTL time.Duration
	// Size bounds the addresses tracked at once.
	Size int
}

type ipState struct {
	bucket limits.Bucket
	seen   time.Time
}

// IPLimiter is the per-address flood control (RATE-001, SEC-051).
type IPLimiter struct {
	s   IPSettings
	now func() time.Time

	mu    sync.Mutex
	state map[string]*ipState
}

// NewIPLimiter returns a limiter; now is the clock (time.Now when nil).
func NewIPLimiter(s IPSettings, now func() time.Time) *IPLimiter {
	if now == nil {
		now = time.Now
	}
	return &IPLimiter{s: s, now: now, state: map[string]*ipState{}}
}

// Take charges one request of client. A refusal carries the time until a
// token is free again.
func (l *IPLimiter) Take(client string) limits.Decision {
	if l.s.Rate.Per == 0 {
		return limits.Decision{Allowed: true}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st, ok := l.state[client]
	if !ok {
		l.makeRoom(now)
		st = &ipState{}
		l.state[client] = st
	}
	st.seen = now
	return st.bucket.Take(now, l.s.Rate)
}

// makeRoom keeps the table inside its size: entries idle longer than the
// TTL go first, then, if none had, the least recently seen one.
func (l *IPLimiter) makeRoom(now time.Time) {
	if len(l.state) < l.s.Size {
		return
	}
	var oldest string
	for k, v := range l.state {
		if now.Sub(v.seen) >= l.s.TTL {
			delete(l.state, k)
			continue
		}
		if oldest == "" || v.seen.Before(l.state[oldest].seen) {
			oldest = k
		}
	}
	if len(l.state) >= l.s.Size {
		delete(l.state, oldest)
	}
}

// Expire drops the state of every address idle longer than the TTL
// (RATE-015). Run calls it once per TTL.
func (l *IPLimiter) Expire() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, v := range l.state {
		if now.Sub(v.seen) >= l.s.TTL {
			delete(l.state, k)
		}
	}
}

// Tracked returns the number of addresses held.
func (l *IPLimiter) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.state)
}

// Run expires idle addresses every TTL until ctx ends.
func (l *IPLimiter) Run(ctx context.Context) {
	t := time.NewTicker(l.s.TTL)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Expire()
		}
	}
}

// Plan is the part of a key's plan the key gates read (RATE-002).
type Plan struct {
	Rate  limits.Rate
	Quota limits.Quota
}

// UsageReader reads a key's units already recorded in the current windows,
// so a restarted instance does not hand out a spent quota again.
type UsageReader interface {
	Used(ctx context.Context, key string, dayStart, monthStart time.Time) (day, month int64, err error)
}

// Reason is why a key gate refused.
type Reason string

// The refusals of the key gates.
const (
	RateLimited   Reason = "rate_limited"
	QuotaExceeded Reason = "quota_exceeded"
)

// Verdict is the answer of the key gates for one request, with what the
// RateLimit fields report.
type Verdict struct {
	// Refused is empty when the request passes.
	Refused Reason
	// RetryAfter is set when refused.
	RetryAfter time.Duration
	// Burst is the token bucket after this request.
	Burst limits.Decision
	// DayLeft and MonthLeft are the quota units left; DayReset and
	// MonthReset the time until the windows reset. Unused for a plan
	// without that quota.
	DayLeft, MonthLeft   int64
	DayReset, MonthReset time.Duration
}

type keyState struct {
	mu     sync.Mutex
	bucket limits.Bucket
	usage  limits.Usage
	seeded bool
}

// KeyLimiter runs the per-key bucket and the quotas.
type KeyLimiter struct {
	loc   *time.Location
	usage UsageReader
	now   func() time.Time

	mu    sync.Mutex
	state map[string]*keyState
	// seedFailures counts quota windows started without the recorded
	// usage because the access store could not answer.
	seedFailures int
}

// NewKeyLimiter returns the key gates; quota windows reset at midnight in
// loc. usage may be nil (no recorded usage is read).
func NewKeyLimiter(loc *time.Location, usage UsageReader, now func() time.Time) *KeyLimiter {
	if now == nil {
		now = time.Now
	}
	return &KeyLimiter{loc: loc, usage: usage, now: now, state: map[string]*keyState{}}
}

func (l *KeyLimiter) stateOf(key string) *keyState {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.state[key]
	if !ok {
		st = &keyState{}
		l.state[key] = st
	}
	return st
}

// Admit runs the bucket, then the quota, for one request of key under p. A
// request that passes both is charged one quota unit (RATE-004); a refused
// one is charged no quota unit.
func (l *KeyLimiter) Admit(ctx context.Context, key string, p Plan) Verdict {
	st := l.stateOf(key)
	st.mu.Lock()
	defer st.mu.Unlock()
	now := l.now()
	before := st.usage
	st.usage.Roll(now, l.loc)
	if !st.seeded || !before.DayStart.Equal(st.usage.DayStart) {
		l.seed(ctx, key, st)
	}
	v := Verdict{Burst: st.bucket.Take(now, p.Rate)}
	if !v.Burst.Allowed {
		v.Refused, v.RetryAfter = RateLimited, v.Burst.RetryAfter
	} else if over, reset := st.usage.Exceeded(now, p.Quota, l.loc); over {
		v.Refused, v.RetryAfter = QuotaExceeded, reset
	} else {
		st.usage.Day++
		st.usage.Month++
	}
	v.DayLeft = max(0, p.Quota.Day-st.usage.Day)
	v.MonthLeft = max(0, p.Quota.Month-st.usage.Month)
	v.DayReset = limits.NextDay(now, l.loc).Sub(now)
	v.MonthReset = limits.NextMonth(now, l.loc).Sub(now)
	return v
}

// seed reads the units recorded for the current windows. A store that does
// not answer leaves the in-memory count, and the failure is counted.
func (l *KeyLimiter) seed(ctx context.Context, key string, st *keyState) {
	st.seeded = true
	if l.usage == nil {
		return
	}
	day, month, err := l.usage.Used(ctx, key, st.usage.DayStart, st.usage.MonthStart)
	if err != nil {
		l.mu.Lock()
		l.seedFailures++
		l.mu.Unlock()
		return
	}
	st.usage.Day = max(st.usage.Day, day)
	st.usage.Month = max(st.usage.Month, month)
}

// SeedFailures returns how often recorded usage could not be read.
func (l *KeyLimiter) SeedFailures() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seedFailures
}
