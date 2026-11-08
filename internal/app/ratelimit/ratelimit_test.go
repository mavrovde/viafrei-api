package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/domain/limits"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// 10:00 UTC on 10 March 2026 is 11:00 in Berlin.
var t0 = time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestTheAddressLimiterHoldsEachAddressToItsBucket(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	clk := &clock{t0}
	l := ratelimit.NewIPLimiter(ratelimit.IPSettings{Rate: limits.Rate{Per: 1, Burst: 2}, TTL: time.Minute, Size: 10}, clk.now)
	for i := range 2 {
		if !l.Take("192.0.2.1").Allowed {
			t.Fatalf("request %d inside the burst refused", i)
		}
	}
	d := l.Take("192.0.2.1")
	if d.Allowed || d.RetryAfter != time.Second {
		t.Fatalf("over the burst: %+v", d)
	}
	if !l.Take("192.0.2.2").Allowed {
		t.Fatal("another address shares the bucket")
	}
	clk.t = clk.t.Add(time.Second)
	if !l.Take("192.0.2.1").Allowed {
		t.Fatal("the token did not come back")
	}
}

func TestTheAddressLimiterSwitchesOff(t *testing.T) {
	req.Covers(t, "RATE-001")
	l := ratelimit.NewIPLimiter(ratelimit.IPSettings{Rate: limits.Rate{Per: 0, Burst: 1}, TTL: time.Minute, Size: 10}, nil)
	for range 10 {
		if !l.Take("192.0.2.1").Allowed {
			t.Fatal("refused with the limit off")
		}
	}
	if l.Tracked() != 0 {
		t.Fatal("an address is tracked with the limit off")
	}
}

func TestAddressStateExpiresAndIsBounded(t *testing.T) {
	req.Covers(t, "RATE-015")
	clk := &clock{t0}
	l := ratelimit.NewIPLimiter(ratelimit.IPSettings{Rate: limits.Rate{Per: 1, Burst: 1}, TTL: time.Minute, Size: 2}, clk.now)
	l.Take("a")
	clk.t = clk.t.Add(30 * time.Second)
	l.Take("b")
	// Full: c pushes out the least recently seen, a, and keeps b.
	l.Take("c")
	if l.Tracked() != 2 {
		t.Fatalf("tracked %d", l.Tracked())
	}
	if l.Take("b").Allowed {
		t.Fatal("b's state was dropped instead of a's")
	}
	// Idle entries go first when full: at +101 s, b and c (seen at +30 s)
	// are past the TTL; both go.
	clk.t = t0.Add(101 * time.Second)
	l.Take("d")
	if l.Tracked() != 1 {
		t.Fatalf("idle entries kept: %d", l.Tracked())
	}
	clk.t = clk.t.Add(59 * time.Second)
	l.Expire()
	if l.Tracked() != 1 {
		t.Fatal("expired inside the TTL")
	}
	clk.t = clk.t.Add(time.Second)
	l.Expire()
	if l.Tracked() != 0 {
		t.Fatal("not expired after the TTL")
	}
}

func TestRunExpiresUntilCancelled(t *testing.T) {
	req.Covers(t, "RATE-015")
	clk := &clock{t0}
	l := ratelimit.NewIPLimiter(ratelimit.IPSettings{Rate: limits.Rate{Per: 1, Burst: 1}, TTL: 20 * time.Millisecond, Size: 10}, func() time.Time { return clk.t })
	l.Take("a")
	clk.t = clk.t.Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for l.Tracked() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("Run did not expire within 1 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

// usage is a recorded-usage reader.
type usage struct {
	day, month int64
	err        error
	calls      int
}

func (u *usage) Used(context.Context, string, time.Time, time.Time) (int64, int64, error) {
	u.calls++
	return u.day, u.month, u.err
}

var plan = ratelimit.Plan{Rate: limits.Rate{Per: 10, Burst: 2}, Quota: limits.Quota{Day: 3, Month: 5}}

func TestTheKeyBucketRefusesFirstAndChargesNoUnit(t *testing.T) {
	req.Covers(t, "RATE-002", "RATE-004")
	clk := &clock{t0}
	l := ratelimit.NewKeyLimiter(berlin(t), nil, clk.now)
	ctx := context.Background()
	for i := range 2 {
		if v := l.Admit(ctx, "k", plan); v.Refused != "" || v.DayLeft != int64(2-i) {
			t.Fatalf("request %d: %+v", i, v)
		}
	}
	v := l.Admit(ctx, "k", plan)
	if v.Refused != ratelimit.RateLimited || v.RetryAfter != 100*time.Millisecond || v.DayLeft != 1 {
		t.Fatalf("over the bucket: %+v", v)
	}
	// Another key has a bucket of its own.
	if v := l.Admit(ctx, "other", plan); v.Refused != "" {
		t.Fatalf("other key: %+v", v)
	}
}

func TestTheQuotaRefusesUntilTheReset(t *testing.T) {
	req.Covers(t, "RATE-003", "RATE-004", "RATE-007")
	loc := berlin(t)
	clk := &clock{t0}
	l := ratelimit.NewKeyLimiter(loc, nil, clk.now)
	ctx := context.Background()
	for range 3 {
		clk.t = clk.t.Add(time.Second)
		if v := l.Admit(ctx, "k", plan); v.Refused != "" {
			t.Fatalf("inside the day quota: %+v", v)
		}
	}
	clk.t = clk.t.Add(time.Second)
	v := l.Admit(ctx, "k", plan)
	reset := limits.NextDay(clk.t, loc).Sub(clk.t)
	if v.Refused != ratelimit.QuotaExceeded || v.RetryAfter != reset || v.DayReset != reset || v.DayLeft != 0 || v.MonthLeft != 2 {
		t.Fatalf("the spent day: %+v", v)
	}
	if v.MonthReset != limits.NextMonth(clk.t, loc).Sub(clk.t) {
		t.Fatalf("month reset %v", v.MonthReset)
	}
	// The next Berlin day the day quota is back; the month runs out after
	// two more.
	clk.t = time.Date(2026, 3, 10, 23, 0, 0, 0, time.UTC)
	for range 2 {
		clk.t = clk.t.Add(time.Second)
		if v := l.Admit(ctx, "k", plan); v.Refused != "" {
			t.Fatalf("the next day: %+v", v)
		}
	}
	clk.t = clk.t.Add(time.Second)
	if v := l.Admit(ctx, "k", plan); v.Refused != ratelimit.QuotaExceeded || v.RetryAfter != limits.NextMonth(clk.t, loc).Sub(clk.t) {
		t.Fatalf("the spent month: %+v", v)
	}
}

func TestRecordedUsageSeedsANewWindow(t *testing.T) {
	req.Covers(t, "RATE-003", "RATE-014")
	clk := &clock{t0}
	u := &usage{day: 3, month: 3}
	l := ratelimit.NewKeyLimiter(berlin(t), u, clk.now)
	ctx := context.Background()
	// A restarted instance knows the three units already used today.
	if v := l.Admit(ctx, "k", plan); v.Refused != ratelimit.QuotaExceeded {
		t.Fatalf("the recorded day was ignored: %+v", v)
	}
	// Read once per window, not per request.
	clk.t = clk.t.Add(time.Second)
	l.Admit(ctx, "k", plan)
	if u.calls != 1 {
		t.Fatalf("read %d times", u.calls)
	}
	// A new day reads again; a recorded count below the held one keeps the
	// held one.
	u.day, u.month = 0, 1
	clk.t = time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC)
	if v := l.Admit(ctx, "k", plan); v.Refused != "" || v.MonthLeft != 1 || u.calls != 2 {
		t.Fatalf("the next day: %+v (%d reads)", v, u.calls)
	}
}

func TestAFailedUsageReadIsCountedAndDoesNotRefuse(t *testing.T) {
	req.Covers(t, "RATE-014")
	u := &usage{err: errors.New("store down")}
	l := ratelimit.NewKeyLimiter(berlin(t), u, nil)
	if v := l.Admit(context.Background(), "k", plan); v.Refused != "" {
		t.Fatalf("refused on a failed read: %+v", v)
	}
	if l.SeedFailures() != 1 {
		t.Fatalf("failures %d", l.SeedFailures())
	}
}
