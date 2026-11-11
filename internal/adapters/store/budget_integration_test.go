//go:build integration

package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// budgetStore opens a store whose reads take a slot of a shared budget of
// slots slots in lock space space.
func budgetStore(t *testing.T, ctx context.Context, space, slots, reserved int, extra ...string) *store.Store {
	t.Helper()
	cfg := readerConfig(t, append([]string{
		"VFAPI_DB_SHARED_BUDGET_SLOTS=" + strconv.Itoa(slots),
		"VFAPI_DB_SHARED_BUDGET_LOCK_SPACE=" + strconv.Itoa(space),
		"VFAPI_DB_SHARED_BUDGET_CRITICAL_RESERVED=" + strconv.Itoa(reserved),
		"VFAPI_DB_SHARED_BUDGET_MAX_WAIT=100ms",
		"VFAPI_DB_SHARED_BUDGET_RETRY_INTERVAL=5ms",
	}, extra...)...)
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st.WithSharedBudget(wire.SharedBudget(cfg))
}

// otherReader is another reader of the store sharing the budget, as the MCP
// server does: it holds slots the same way, by the two-key advisory lock.
type otherReader struct {
	conn  *pgx.Conn
	space int
}

func newOtherReader(t *testing.T, ctx context.Context, space int) *otherReader {
	t.Helper()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
	return &otherReader{conn: conn, space: space}
}

func (o *otherReader) hold(t *testing.T, ctx context.Context, slots ...int) {
	t.Helper()
	for _, s := range slots {
		if _, err := o.conn.Exec(ctx, "select pg_advisory_lock($1::int, $2::int)", o.space, s); err != nil {
			t.Fatal(err)
		}
	}
}

func (o *otherReader) release(ctx context.Context, slot int) error {
	_, err := o.conn.Exec(ctx, "select pg_advisory_unlock($1::int, $2::int)", o.space, slot)
	return err
}

// heldSlots counts the slots of space held by anyone, read from pg_locks.
func (o *otherReader) heldSlots(ctx context.Context) (int, error) {
	var n int
	err := o.conn.QueryRow(ctx, `select count(*) from pg_locks
where locktype = 'advisory' and classid = $1::int and objsubid = 2 and granted`, o.space).Scan(&n)
	return n, err
}

func TestAReadHoldsOneSharedSlotUntilItsTransactionEnds(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	ctx := bounded(t)
	st := budgetStore(t, ctx, 7401, 3, 0)
	other := newOtherReader(t, ctx, 7401)
	var during int
	if err := st.Read(ctx, func(ctx context.Context, _ pgx.Tx) error {
		var err error
		during, err = other.heldSlots(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, err := other.heldSlots(ctx)
	if err != nil || during != 1 || after != 0 {
		t.Fatalf("slots held during the read %d, after it %d (%v)", during, after, err)
	}
}

func TestAReadIsShedWhileOtherReadersHoldEverySlot(t *testing.T) {
	req.Covers(t, "NFR-CAP-002", "NFR-CAP-003")
	ctx := bounded(t)
	st := budgetStore(t, ctx, 7402, 2, 0, "VFAPI_DB_SHARED_BUDGET_RETRY_AFTER=4s")
	other := newOtherReader(t, ctx, 7402)
	other.hold(t, ctx, 1, 2)
	reached := false
	start := time.Now()
	err := st.Read(ctx, func(context.Context, pgx.Tx) error { reached = true; return nil })
	var r *admission.Refusal
	if !errors.As(err, &r) || r.RetryAfter != 4*time.Second || reached {
		t.Fatalf("err %v, the read reached its statement: %v", err, reached)
	}
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Errorf("the refusal took %v, past max_wait 100ms", took)
	}
	if err := other.release(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Read(ctx, func(context.Context, pgx.Tx) error { return nil }); err != nil {
		t.Fatalf("a freed slot is taken: %v", err)
	}
}

func TestAReadWaitsForASlotAndKeepsWhatIsLeftOfItsBudget(t *testing.T) {
	req.Covers(t, "NFR-CAP-002", "NFR-PERF-001")
	ctx := bounded(t)
	st := budgetStore(t, ctx, 7403, 1, 0, "VFAPI_DB_SHARED_BUDGET_MAX_WAIT=1s")
	other := newOtherReader(t, ctx, 7403)
	other.hold(t, ctx, 1)
	freed := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		freed <- other.release(ctx, 1)
	}()
	read, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var ms int
	err := st.Read(read, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select setting::int from pg_settings where name = 'statement_timeout'").Scan(&ms)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-freed; err != nil {
		t.Fatal(err)
	}
	// 1500ms minus the 50ms reserve, less the 300ms the slot was held.
	if ms > 1200 || ms < 100 {
		t.Fatalf("statement_timeout %dms is not what is left after the wait", ms)
	}
}

func TestTheCriticalReserveIsKeptFromLowerTiers(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	ctx := bounded(t)
	st := budgetStore(t, ctx, 7404, 3, 1)
	other := newOtherReader(t, ctx, 7404)
	other.hold(t, ctx, 1, 2)
	standard := admission.WithCaller(ctx, admission.Caller{Tier: admission.Standard, Key: "k"})
	if err := st.Read(standard, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, admission.ErrOverloaded) {
		t.Fatalf("a standard read took the reserved slot: %v", err)
	}
	if err := st.Read(ctx, func(context.Context, pgx.Tx) error { return nil }); err != nil {
		t.Fatalf("a critical read is refused the reserved slot: %v", err)
	}
}
