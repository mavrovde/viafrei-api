// Package limits holds the rules of the rate and quota gates (Spec: Rate
// Limiting, Quotas and Priority): the token bucket in its generic cell rate
// algorithm form, and the quota windows that reset at midnight of a time
// zone. It keeps no state of its own and reads no clock.
package limits

import (
	"math"
	"time"
)

// Rate is a token bucket's shape: Per tokens a second, at most Burst at once.
type Rate struct {
	Per   int
	Burst int
}

// interval is the time one token takes to come back.
func (r Rate) interval() time.Duration { return time.Second / time.Duration(r.Per) }

// Window is the time an empty bucket takes to fill up again: the window of
// the burst policy in the RateLimit-Policy field.
func (r Rate) Window() time.Duration { return time.Duration(r.Burst) * r.interval() }

// Bucket is one token bucket, kept as its theoretical arrival time (GCRA):
// equivalent to a bucket of Burst tokens refilled at Per a second, with
// nothing to update between requests (RATE-002).
type Bucket struct {
	tat time.Time
}

// Decision is the answer of one Take.
type Decision struct {
	// Allowed is true when the request took a token.
	Allowed bool
	// Remaining is the number of tokens left after this request.
	Remaining int
	// RetryAfter is the time until a token is free again; zero when
	// Allowed.
	RetryAfter time.Duration
	// Reset is the time until the bucket is full again.
	Reset time.Duration
}

// Take asks the bucket for one token at now. A refused request changes
// nothing.
func (b *Bucket) Take(now time.Time, r Rate) Decision {
	t := r.interval()
	window := r.Window()
	tat := b.tat
	if tat.Before(now) {
		tat = now
	}
	next := tat.Add(t)
	if over := next.Sub(now) - window; over > 0 {
		return Decision{RetryAfter: over, Reset: tat.Sub(now), Remaining: 0}
	}
	b.tat = next
	used := next.Sub(now)
	return Decision{Allowed: true, Remaining: int((window - used) / t), Reset: used}
}

// Idle reports whether the bucket is full at now, so its state can be
// forgotten without changing any later answer.
func (b *Bucket) Idle(now time.Time) bool { return !b.tat.After(now) }

// DayStart is the start of the quota day holding t: midnight of t's date in
// loc, whatever the offset that day (RATE-003).
func DayStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// NextDay is the start of the quota day after the one holding t.
func NextDay(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc)
}

// MonthStart is the start of the quota month holding t: midnight of the
// first of t's month in loc.
func MonthStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, loc)
}

// NextMonth is the start of the quota month after the one holding t.
func NextMonth(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month()+1, 1, 0, 0, 0, 0, loc)
}

// Seconds renders a wait as the whole seconds of Retry-After and of the t
// parameter of the RateLimit field: rounded up, and at least one, so a
// client that waits as told is never refused again for the same reason.
func Seconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	return max(s, 1)
}

// Quota is a plan's daily and monthly allowance; zero means none.
type Quota struct {
	Day   int64
	Month int64
}

// Usage counts one key's units in its current quota windows.
type Usage struct {
	DayStart, MonthStart time.Time
	Day, Month           int64
}

// Roll moves u to the windows holding now: a window that has ended starts
// again from zero.
func (u *Usage) Roll(now time.Time, loc *time.Location) {
	if d := DayStart(now, loc); !d.Equal(u.DayStart) {
		u.DayStart, u.Day = d, 0
	}
	if m := MonthStart(now, loc); !m.Equal(u.MonthStart) {
		u.MonthStart, u.Month = m, 0
	}
}

// Exceeded reports whether one more unit would exceed q, and if so the time
// until the window that refuses it resets. A spent month outlasts a spent
// day, so the month decides when both are spent.
func (u Usage) Exceeded(now time.Time, q Quota, loc *time.Location) (bool, time.Duration) {
	if q.Month > 0 && u.Month >= q.Month {
		return true, NextMonth(now, loc).Sub(now)
	}
	if q.Day > 0 && u.Day >= q.Day {
		return true, NextDay(now, loc).Sub(now)
	}
	return false, 0
}
