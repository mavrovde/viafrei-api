package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// held returns a store with a nil pool behind a one-permit controller whose
// permit is already taken, and the release of that permit. A read that
// reached the pool would panic.
func held(t *testing.T, env ...string) (*store.Store, func(), *config.Config) {
	t.Helper()
	cfg, err := config.Load(config.Options{Environ: append([]string{"VFAPI_DB_POOL_MAX=1", "VFAPI_TIERS_CRITICAL_RESERVED=0", "VFAPI_TIERS_PER_KEY_INFLIGHT=1"}, env...)})
	if err != nil {
		t.Fatal(err)
	}
	c := admission.New(wire.AdmissionSettings(cfg), nil)
	release, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return store.New(nil, cfg).WithAdmission(c), release, cfg
}

func untouched(context.Context, pgx.Tx) error { panic("storage was reached") }

func TestAShedReadIsOverloaded(t *testing.T) {
	req.Covers(t, "RATE-011")
	st, _, _ := held(t, "VFAPI_TIERS_BULK_QUEUE_LEN=0")
	ctx := admission.WithCaller(context.Background(), admission.Caller{Tier: admission.Bulk, Key: "k"})
	err := st.Read(ctx, untouched)
	var r *admission.Refusal
	if !errors.As(err, &r) || errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("got %v", err)
	}
}

func TestAReadEndedWhileWaitingIsDeadlineExhausted(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	st, _, _ := held(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	err := st.Read(ctx, untouched)
	if !errors.Is(err, store.ErrDeadlineExhausted) || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// TestAWaitThatSpentTheBudgetNeverTouchesStorage: the permit comes in time
// for the admission, but the wait left less than the statement floor, so
// the read is refused before storage.
func TestAWaitThatSpentTheBudgetNeverTouchesStorage(t *testing.T) {
	req.Covers(t, "NFR-PERF-004", "RATE-008")
	st, release, cfg := held(t)
	reserve, floor := cfg.Deadline.Reserve, cfg.DB.StatementTimeoutFloor
	ctx, cancel := context.WithTimeout(context.Background(), reserve+floor+100*time.Millisecond)
	defer cancel()
	go func() { time.Sleep(100*time.Millisecond + floor/2); release() }()
	if err := st.Read(ctx, untouched); !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("got %v", err)
	}
}
