// Package health is the readiness use case (FR-SVC-004): storage must answer
// within the readiness timeout and publish a compatible read contract.
package health

import (
	"context"
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
