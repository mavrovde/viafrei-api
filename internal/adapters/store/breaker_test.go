package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/breaker"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var trip = breaker.Settings{FailureThreshold: 3, Window: time.Minute, OpenFor: time.Minute, HalfOpenProbes: 1}

// The breaker opens after its threshold of failed reads against storage
// that refuses connections, and then answers at once without asking it
// (NFR-RES-002, QAS-REL-02: within 50 ms).
func TestTheBreakerOpensOnUnreachableStorage(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + closedPortURL(t)}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := breaker.New(trip, nil, nil)
	st.WithBreaker(b)
	read := func() (error, bool) {
		called := false
		err := st.Read(context.Background(), func(context.Context, pgx.Tx) error { called = true; return nil })
		return err, called
	}
	for i := range trip.FailureThreshold {
		if err, _ := read(); !errors.Is(err, store.ErrUnavailable) {
			t.Fatalf("read %d: %v, want ErrUnavailable", i+1, err)
		}
	}
	if b.State() != breaker.Open {
		t.Fatalf("breaker %v after %d failed reads, want open", b.State(), trip.FailureThreshold)
	}
	start := time.Now()
	err, called := read()
	elapsed := time.Since(start)
	var r *breaker.Refusal
	if !errors.Is(err, store.ErrUnavailable) || !errors.As(err, &r) || called {
		t.Fatalf("open breaker: %v (called %v), want ErrUnavailable wrapping the refusal", err, called)
	}
	if r.RetryAfter <= 0 || r.RetryAfter > trip.OpenFor {
		t.Fatalf("retry after %v, want what is left of %v", r.RetryAfter, trip.OpenFor)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("the refusal took %v, want under 50 ms", elapsed)
	}
}

// A read refused before storage is asked tells the breaker nothing.
func TestAReadRefusedBeforeStorageLeavesTheBreakerClosed(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	b := breaker.New(trip, nil, nil)
	st := store.New(nil, config.Defaults()).WithBreaker(b) // a nil pool would panic if reached
	for range 2 * trip.FailureThreshold {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		err := st.Read(ctx, func(context.Context, pgx.Tx) error { return nil })
		cancel()
		if !errors.Is(err, store.ErrDeadlineExhausted) {
			t.Fatalf("read: %v, want ErrDeadlineExhausted", err)
		}
	}
	if b.State() != breaker.Closed {
		t.Fatalf("breaker %v, want closed", b.State())
	}
}
