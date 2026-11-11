package config_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestTheTiersStayInsideThePermitPool(t *testing.T) {
	req.Covers(t, "CFG-004", "RATE-008", "RATE-009")
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_POOL_MAX=2",
		"VFAPI_TIERS_CRITICAL_RESERVED=2",
		"VFAPI_TIERS_STANDARD_RESERVED=1",
		"VFAPI_TIERS_BULK_MAX_INFLIGHT=3",
		"VFAPI_TIERS_PER_KEY_INFLIGHT=3",
		"VFAPI_DEADLINE_REQUEST=1s",
		"VFAPI_TIERS_CRITICAL_QUEUE_MAX_WAIT=1s",
	}})
	assertRefused(t, err,
		"tiers.*.reserved: 3 reserved permits are more than db.pool_max 2",
		"tiers.bulk.max_inflight: 3 is more than db.pool_max 2",
		"tiers.per_key_inflight: 3 is more than db.pool_max 2",
		"tiers.critical.queue_max_wait: 1s must be less than deadline.request 1s",
	)
}

func TestAFloatKeyReadsEveryNumberShape(t *testing.T) {
	req.Covers(t, "CFG-003")
	for raw, want := range map[string]float64{"1": 1, "0.5": 0.5} {
		c, err := config.Load(config.Options{File: writeFile(t, "admission:\n  ewma_alpha: "+raw+"\n")})
		if err != nil || c.Admission.EWMAAlpha != want {
			t.Errorf("%s: %v %v", raw, c, err)
		}
	}
	c, err := config.Load(config.Options{Environ: []string{"VFAPI_ADMISSION_EWMA_ALPHA= 0.25"}})
	if err != nil || c.Admission.EWMAAlpha != 0.25 {
		t.Fatalf("from the environment: %v", err)
	}
	_, err = config.Load(config.Options{File: writeFile(t, "admission:\n  ewma_alpha: [1]\n")})
	assertRefused(t, err, "admission.ewma_alpha: [1] is not a number")
	_, err = config.Load(config.Options{Environ: []string{"VFAPI_ADMISSION_EWMA_ALPHA=half"}})
	assertRefused(t, err, `admission.ewma_alpha: "half" is not a number`)
}

func TestTheSharedBudgetLeavesASlotAndEndsInsideTheDeadline(t *testing.T) {
	req.Covers(t, "CFG-004", "NFR-CAP-002")
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_SHARED_BUDGET_SLOTS=2",
		"VFAPI_DB_SHARED_BUDGET_CRITICAL_RESERVED=2",
		"VFAPI_DEADLINE_REQUEST=1s",
		"VFAPI_DB_SHARED_BUDGET_MAX_WAIT=1s",
	}})
	assertRefused(t, err,
		"db.shared_budget.critical_reserved: 2 leaves no slot of db.shared_budget.slots 2 to the other tiers",
		"db.shared_budget.max_wait: 1s must be less than deadline.request 1s",
	)
	if _, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_SHARED_BUDGET_CRITICAL_RESERVED=5"}}); err != nil {
		t.Errorf("a reserve is not checked while the budget is off: %v", err)
	}
}
