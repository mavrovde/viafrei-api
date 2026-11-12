package store_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
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

// halfOpen is a breaker with one trial slot, half-open: opened by its
// threshold of failures, its open period over.
func halfOpen(t *testing.T) *breaker.Breaker {
	t.Helper()
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	b := breaker.New(trip, func() time.Time { return at }, nil)
	for range trip.FailureThreshold {
		tk, err := b.Allow()
		if err != nil {
			t.Fatal(err)
		}
		tk.Done(breaker.Failure)
	}
	at = at.Add(trip.OpenFor)
	if b.State() != breaker.HalfOpen {
		t.Fatalf("breaker %v, want half-open", b.State())
	}
	return b
}

// trialFree: the breaker's one trial slot is free again, so a later read
// is admitted.
func trialFree(t *testing.T, b *breaker.Breaker) {
	t.Helper()
	tk, err := b.Allow()
	if err != nil {
		t.Fatalf("the trial slot was kept by a read refused before storage: %v", err)
	}
	tk.Done(breaker.Ignored)
}

// A trial refused before storage gives its slot back: kept, the breaker
// would refuse every read once half_open_probes such reads had happened.
func TestATrialRefusedBeforeStorageGivesItsSlotBack(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	// Refused by the deadline floor.
	b := halfOpen(t)
	st := store.New(nil, config.Defaults()).WithBreaker(b)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	err := st.Read(ctx, untouched)
	cancel()
	if !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("budget: %v, want ErrDeadlineExhausted", err)
	}
	trialFree(t, b)
	// Refused by the admission.
	b = halfOpen(t)
	st, _, _ = held(t, "VFAPI_TIERS_BULK_QUEUE_LEN=0")
	st.WithBreaker(b)
	ctx = admission.WithCaller(context.Background(), admission.Caller{Tier: admission.Bulk, Key: "k"})
	var r *admission.Refusal
	if err := st.Read(ctx, untouched); !errors.As(err, &r) {
		t.Fatalf("admission: %v, want a refusal", err)
	}
	trialFree(t, b)
}

// silentURL is a storage address that accepts connections and never
// answers, so a transaction cannot be opened before the deadline. The
// returned hang-up closes the listener and every connection it accepted,
// including one accepted after the hang-up: the caller registers it AFTER
// the store's Close, so it runs first and Close never waits out the connect
// timeout for a connection the pool opened late (a per-connection cleanup
// registered at accept time ran after Close and cost CI the full 2 s).
func silentURL(t *testing.T) (string, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu     sync.Mutex
		conns  []net.Conn
		hungUp bool
	)
	hangUp := func() {
		mu.Lock()
		defer mu.Unlock()
		hungUp = true
		_ = l.Close()
		for _, c := range conns {
			_ = c.Close()
		}
	}
	t.Cleanup(hangUp)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if hungUp {
				_ = c.Close()
			} else {
				conns = append(conns, c)
			}
			mu.Unlock()
		}
	}()
	return "postgres://reader:pw@" + l.Addr().String() + "/contract?sslmode=disable", hangUp
}

// A transaction that could not be opened by the deadline is a failure, the
// storage's own: unlike a statement that ran out of time, which is storage
// answering. A read whose caller went away while opening it counts for
// nothing.
func TestATransactionNotOpenedInTimeCountsAgainstTheBreaker(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	url, hangUp := silentURL(t)
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + url}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	t.Cleanup(hangUp) // runs before Close, which waits for a connection still opening
	one := breaker.Settings{FailureThreshold: 1, Window: time.Minute, OpenFor: time.Minute, HalfOpenProbes: 1}

	b := breaker.New(one, nil, nil)
	st.WithBreaker(b)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if err := st.Read(ctx, untouched); err == nil {
		t.Fatal("a read against silent storage succeeded")
	}
	if b.State() != breaker.Closed {
		t.Fatalf("breaker %v after a read its caller abandoned, want closed", b.State())
	}

	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := st.Read(ctx, untouched); !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("read: %v, want ErrDeadlineExhausted", err)
	}
	// The outcome is reported on the read's goroutine, a moment after the
	// caller's answer.
	for deadline := time.Now().Add(2 * time.Second); b.State() != breaker.Open && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if b.State() != breaker.Open {
		t.Fatalf("breaker %v after a transaction not opened in time, want open", b.State())
	}
}
