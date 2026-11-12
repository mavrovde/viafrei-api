//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/app/breaker"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// Recovery (QAS-REL-02): once the open period is over, one trial read that
// storage answers closes the breaker, and reads flow again. A statement
// that runs out of time is storage answering, not a failure.
func TestTheBreakerRecoversThroughOneTrialRead(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	ctx := bounded(t)
	now := time.Now()
	clock := func() time.Time { return now }
	b := breaker.New(breaker.Settings{FailureThreshold: 1, Window: time.Minute, OpenFor: time.Minute, HalfOpenProbes: 1}, clock, nil)
	// A wide reserve, so the statement timeout fires well before the
	// client's deadline even on a loaded host.
	st := openStore(t, ctx, "VFAPI_DEADLINE_RESERVE=400ms").WithBreaker(b)

	tk, err := b.Allow()
	if err != nil {
		t.Fatal(err)
	}
	tk.Done(breaker.Failure)
	if err := st.Read(ctx, func(context.Context, pgx.Tx) error { return nil }); !errors.As(err, new(*breaker.Refusal)) {
		t.Fatalf("read while open: %v, want the refusal", err)
	}

	now = now.Add(time.Minute)
	if err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "select 1")
		return err
	}); err != nil {
		t.Fatalf("trial read: %v", err)
	}
	if b.State() != breaker.Closed {
		t.Fatalf("breaker %v after a successful trial, want closed", b.State())
	}

	// A statement stopped by its timeout leaves the breaker closed: one is
	// enough, since one failure (FailureThreshold 1) would open it. Its
	// budget is its own, not what is left of the test's bound, so a slow
	// setup cannot shrink it below the reserve and the floor.
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = st.Read(short, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "select pg_sleep(5)")
		return err
	})
	if sqlState(err) != "57014" {
		t.Fatalf("a five-second statement under a one-second budget: %v, want the statement timeout (57014)", err)
	}
	if b.State() != breaker.Closed {
		t.Fatalf("breaker %v after a statement that ran out of time, want closed", b.State())
	}
}
