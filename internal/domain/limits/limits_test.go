package limits_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/limits"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var t0 = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestBucketAllowsTheBurstThenTheRate(t *testing.T) {
	req.Covers(t, "RATE-002")
	r := limits.Rate{Per: 2, Burst: 3}
	if r.Window() != 1500*time.Millisecond {
		t.Fatalf("window %v", r.Window())
	}
	var b limits.Bucket
	if !b.Idle(t0) {
		t.Fatal("a new bucket is not full")
	}
	for i, want := range []int{2, 1, 0} {
		d := b.Take(t0, r)
		if !d.Allowed || d.Remaining != want || d.RetryAfter != 0 {
			t.Fatalf("take %d: %+v", i, d)
		}
		if d.Reset != time.Duration(i+1)*500*time.Millisecond {
			t.Fatalf("take %d: reset %v", i, d.Reset)
		}
	}
	d := b.Take(t0, r)
	if d.Allowed || d.RetryAfter != 500*time.Millisecond || d.Remaining != 0 || d.Reset != 1500*time.Millisecond {
		t.Fatalf("over the burst: %+v", d)
	}
	// A refusal changes nothing: the same refusal a moment later, and a
	// token exactly one interval later.
	if d := b.Take(t0.Add(499*time.Millisecond), r); d.Allowed || d.RetryAfter != time.Millisecond {
		t.Fatalf("just before the token: %+v", d)
	}
	if d := b.Take(t0.Add(500*time.Millisecond), r); !d.Allowed || d.Remaining != 0 {
		t.Fatalf("at the token: %+v", d)
	}
	if b.Idle(t0.Add(time.Second)) || !b.Idle(t0.Add(2*time.Second)) {
		t.Fatal("idle reports the wrong instant")
	}
	// A long pause refills the bucket to its burst and no further.
	if d := b.Take(t0.Add(time.Hour), r); !d.Allowed || d.Remaining != 2 {
		t.Fatalf("after a pause: %+v", d)
	}
}

func TestQuotaWindowsFollowBerlinMidnight(t *testing.T) {
	req.Covers(t, "RATE-003")
	loc := berlin(t)
	cases := []struct {
		name            string
		at              time.Time
		day, next       time.Time
		month, nextMnth time.Time
	}{
		// 22:30 UTC on 9 March is 23:30 in Berlin (CET): still the 9th.
		{"winter evening", time.Date(2026, 3, 9, 22, 30, 0, 0, time.UTC),
			time.Date(2026, 3, 8, 23, 0, 0, 0, time.UTC), time.Date(2026, 3, 9, 23, 0, 0, 0, time.UTC),
			time.Date(2026, 2, 28, 23, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 22, 0, 0, 0, time.UTC)},
		// 29 March 2026 is the spring change: a 23-hour day.
		{"spring change", time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC), time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC),
			time.Date(2026, 2, 28, 23, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 22, 0, 0, 0, time.UTC)},
		// 25 October 2026 is the autumn change: a 25-hour day.
		{"autumn change", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
			time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC), time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)},
		// The year rolls over with the month.
		{"new year", time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC),
			time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 23, 0, 0, 0, time.UTC),
			time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC), time.Date(2027, 1, 31, 23, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := limits.DayStart(c.at, loc); !got.Equal(c.day) {
			t.Errorf("%s: day start %v", c.name, got.UTC())
		}
		if got := limits.NextDay(c.at, loc); !got.Equal(c.next) {
			t.Errorf("%s: next day %v", c.name, got.UTC())
		}
		if got := limits.MonthStart(c.at, loc); !got.Equal(c.month) {
			t.Errorf("%s: month start %v", c.name, got.UTC())
		}
		if got := limits.NextMonth(c.at, loc); !got.Equal(c.nextMnth) {
			t.Errorf("%s: next month %v", c.name, got.UTC())
		}
	}
}

func TestUsageRollsAndRefusesPastTheQuota(t *testing.T) {
	req.Covers(t, "RATE-003", "RATE-004")
	loc := berlin(t)
	var u limits.Usage
	u.Roll(t0, loc)
	u.Day, u.Month = 2, 5
	q := limits.Quota{Day: 3, Month: 10}
	if over, _ := u.Exceeded(t0, q, loc); over {
		t.Fatal("refused under both quotas")
	}
	u.Day = 3
	if over, reset := u.Exceeded(t0, q, loc); !over || reset != limits.NextDay(t0, loc).Sub(t0) {
		t.Fatalf("a spent day: %v %v", over, reset)
	}
	// A spent month decides over a spent day.
	u.Month = 10
	if over, reset := u.Exceeded(t0, q, loc); !over || reset != limits.NextMonth(t0, loc).Sub(t0) {
		t.Fatalf("a spent month: %v %v", over, reset)
	}
	// No quota is never exceeded.
	if over, _ := u.Exceeded(t0, limits.Quota{}, loc); over {
		t.Fatal("a plan without quotas refused")
	}
	// The same day keeps the counts; the next day resets the day only; the
	// next month resets both.
	u.Roll(t0.Add(time.Hour), loc)
	if u.Day != 3 || u.Month != 10 {
		t.Fatalf("same day: %+v", u)
	}
	u.Roll(t0.Add(24*time.Hour), loc)
	if u.Day != 0 || u.Month != 10 {
		t.Fatalf("next day: %+v", u)
	}
	u.Day = 1
	u.Roll(time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC), loc)
	if u.Day != 0 || u.Month != 0 || !u.MonthStart.Equal(time.Date(2026, 3, 31, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("next month: %+v", u)
	}
}

func TestSecondsRoundsUpToAtLeastOne(t *testing.T) {
	req.Covers(t, "RATE-007")
	for d, want := range map[time.Duration]int{0: 1, time.Millisecond: 1, time.Second: 1, 1001 * time.Millisecond: 2, 90 * time.Second: 90} {
		if got := limits.Seconds(d); got != want {
			t.Errorf("Seconds(%v) = %d, want %d", d, got, want)
		}
	}
}
