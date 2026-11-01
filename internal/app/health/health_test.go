package health_test

import (
	"context"
	"errors"
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
