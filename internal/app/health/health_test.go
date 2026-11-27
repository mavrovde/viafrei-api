package health_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/domain/service"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type reader struct {
	version string
	err     error
	delay   time.Duration
}

func (r reader) ContractVersion(ctx context.Context) (string, error) {
	select {
	case <-time.After(r.delay):
		return r.version, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

var want = service.ContractVersion{Major: 0, Minor: 1}

func TestReadyWhenStorageAnswersACompatibleContract(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	r := health.NewChecker(reader{version: "0.2"}, want, time.Second).Check(context.Background())
	if !r.Ready || r.Checks[health.CheckStorage] != health.Pass || r.Checks[health.CheckContract] != health.Pass {
		t.Fatalf("%+v", r)
	}
}

func TestNotReady(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	cases := map[string]struct {
		r        reader
		storage  string
		contract string
	}{
		"storage error":      {reader{err: errors.New("down")}, health.Fail, health.Fail},
		"incompatible major": {reader{version: "1.1"}, health.Pass, health.Fail},
		"older minor":        {reader{version: "0.0"}, health.Pass, health.Fail},
		"unparsable version": {reader{version: "x"}, health.Pass, health.Fail},
	}
	for name, c := range cases {
		r := health.NewChecker(c.r, want, time.Second).Check(context.Background())
		if r.Ready || r.Checks[health.CheckStorage] != c.storage || r.Checks[health.CheckContract] != c.contract {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestReadyTimeoutBoundsTheCheck(t *testing.T) {
	req.Covers(t, "FR-SVC-004", "CFG-006")
	slow := reader{version: "0.1", delay: 300 * time.Millisecond}
	start := time.Now()
	r := health.NewChecker(slow, want, 50*time.Millisecond).Check(context.Background())
	if r.Ready || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("a 50ms ready_timeout let a 300ms answer through: %+v after %s", r, time.Since(start))
	}
	if r := health.NewChecker(slow, want, time.Second).Check(context.Background()); !r.Ready {
		t.Fatalf("a 1s ready_timeout refused a 300ms answer: %+v", r)
	}
}

func TestExpectedVersion(t *testing.T) {
	req.Covers(t, "CFG-006")
	if v, err := health.ExpectedVersion("", "0.1"); err != nil || v != want {
		t.Fatalf("built-in: %v %v", v, err)
	}
	if v, err := health.ExpectedVersion("0.3", "0.1"); err != nil || v.Minor != 3 {
		t.Fatalf("configured contract.expected ignored: %v %v", v, err)
	}
}

// countingReader answers the contract version after delay and counts the
// reads.
type countingReader struct {
	reads atomic.Int64
	delay time.Duration
}

func (c *countingReader) ContractVersion(ctx context.Context) (string, error) {
	c.reads.Add(1)
	time.Sleep(c.delay)
	return "0.1", ctx.Err()
}

// Any number of readiness calls, at once and one after another, read
// storage once per ttl (#155); one caller giving up fails no one.
func TestReadinessReadsStorageOncePerInterval(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	r := &countingReader{delay: 20 * time.Millisecond}
	var mu sync.Mutex
	at := time.Unix(0, 0)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return at }
	m := health.Memoise(health.NewChecker(r, want, time.Second), time.Second, now)
	// The first caller has given up already: the check it starts still
	// answers every other caller.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	m.Check(gone)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if !m.Check(context.Background()).Ready {
				t.Error("a concurrent call is not ready")
			}
		})
	}
	wg.Wait()
	for range 50 {
		if !m.Check(context.Background()).Ready {
			t.Fatal("a sequential call is not ready")
		}
	}
	if n := r.reads.Load(); n != 1 {
		t.Fatalf("%d storage reads inside the interval, want 1", n)
	}
	mu.Lock()
	at = at.Add(time.Second)
	mu.Unlock()
	m.Check(context.Background())
	m.Check(context.Background())
	if n := r.reads.Load(); n != 2 {
		t.Fatalf("%d storage reads after the interval, want 2", n)
	}
}
