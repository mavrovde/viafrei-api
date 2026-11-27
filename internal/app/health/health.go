// Package health is the readiness use case (FR-SVC-004): storage must answer
// within the readiness timeout and state a compatible read-contract version.
package health

import (
	"context"
	"sync"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/service"
)

// ContractReader is the port to storage that readiness needs.
type ContractReader interface {
	// ContractVersion returns the read-contract version storage publishes.
	ContractVersion(ctx context.Context) (string, error)
}

// Check names, and their outcomes.
const (
	CheckStorage  = "storage"
	CheckContract = "contract"
	Pass          = "pass"
	Fail          = "fail"
)

// Result is the outcome of one readiness check. It never carries error text,
// versions or addresses: the health answers are public.
type Result struct {
	Ready  bool
	Checks map[string]string
}

// Checker runs the readiness check.
type Checker struct {
	reader   ContractReader
	expected service.ContractVersion
	timeout  time.Duration
}

// NewChecker builds a Checker. timeout is health.ready_timeout.
func NewChecker(reader ContractReader, expected service.ContractVersion, timeout time.Duration) *Checker {
	return &Checker{reader: reader, expected: expected, timeout: timeout}
}

// Check asks storage for its contract version within the timeout.
func (c *Checker) Check(ctx context.Context) Result {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	res := Result{Checks: map[string]string{CheckStorage: Fail, CheckContract: Fail}}
	raw, err := c.reader.ContractVersion(ctx)
	if err != nil {
		return res
	}
	res.Checks[CheckStorage] = Pass
	got, err := service.ParseContractVersion(raw)
	if err != nil || !c.expected.Accepts(got) {
		return res
	}
	res.Checks[CheckContract] = Pass
	res.Ready = true
	return res
}

// ExpectedVersion resolves the read-contract version readiness requires:
// contract.expected when set, otherwise the version built into the binary.
func ExpectedVersion(configured, builtin string) (service.ContractVersion, error) {
	if configured == "" {
		configured = builtin
	}
	return service.ParseContractVersion(configured)
}

// Prober is what Memo memoises: a readiness check.
type Prober interface {
	Check(ctx context.Context) Result
}

// Memo answers readiness from one check per ttl (#155): readiness is
// served on the public listener without a per-address budget, so however
// many calls arrive, an instance reads storage for it at most once per
// ttl. Calls that arrive while a check runs wait for that check and take
// its result (one flight); the check runs without the caller's
// cancellation, so one caller that gives up never fails the others, and is
// bounded by the checker's own timeout.
type Memo struct {
	probe Prober
	ttl   time.Duration
	now   func() time.Time

	mu   sync.Mutex
	at   time.Time
	last Result
	held bool
}

// Memoise returns probe's result memoised for ttl on the clock now.
func Memoise(probe Prober, ttl time.Duration, now func() time.Time) *Memo {
	return &Memo{probe: probe, ttl: ttl, now: now}
}

// Check returns the memoised result while it is younger than ttl, else
// checks once more.
func (m *Memo) Check(ctx context.Context) Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held && m.now().Sub(m.at) < m.ttl {
		return m.last
	}
	m.last = m.probe.Check(context.WithoutCancel(ctx))
	m.at, m.held = m.now(), true
	return m.last
}
