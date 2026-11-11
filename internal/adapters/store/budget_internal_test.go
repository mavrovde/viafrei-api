package store

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type row struct{ err error }

func (r row) Scan(...any) error { return r.err }

// slots answers each attempt from a script: ErrNoRows while every slot is
// held, nil when one is taken, any other error as a failed statement. It
// records the slot count and lock space each attempt asked with.
type slots struct {
	mu      sync.Mutex
	script  []error
	asked   [][]any
	execErr error
	execs   [][]any
}

func (s *slots) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sql != "select set_config('statement_timeout', $1, true)" {
		panic("unexpected statement " + sql)
	}
	s.execs = append(s.execs, args)
	return pgconn.CommandTag{}, s.execErr
}

func (s *slots) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sql != slotSQL {
		panic("unexpected statement " + sql)
	}
	s.asked = append(s.asked, args)
	err := pgx.ErrNoRows
	if len(s.script) > 0 {
		err, s.script = s.script[0], s.script[1:]
	}
	return row{err: err}
}

func budget() SharedBudget {
	return SharedBudget{Slots: 5, LockSpace: 42, CriticalReserved: 2, MaxWait: 50 * time.Millisecond, RetryInterval: time.Millisecond, RetryAfter: 3 * time.Second}
}

func TestASlotIsTakenAtOnceWhenOneIsFree(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	q := &slots{script: []error{nil}}
	waited, err := budget().take(context.Background(), q, admission.Critical, time.Now)
	if err != nil || waited || len(q.asked) != 1 {
		t.Fatalf("waited %v, err %v, attempts %d", waited, err, len(q.asked))
	}
	if q.asked[0][0] != 5 || q.asked[0][1] != int32(42) {
		t.Errorf("a critical read asks every slot of the lock space: %v", q.asked[0])
	}
}

func TestOnlyACriticalReadMayTakeTheReservedSlots(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	for _, tier := range []admission.Tier{admission.Standard, admission.Bulk} {
		q := &slots{script: []error{nil}}
		if _, err := budget().take(context.Background(), q, tier, time.Now); err != nil {
			t.Fatal(err)
		}
		if q.asked[0][0] != 3 {
			t.Errorf("%v asks %v slots, want the 3 below the reserve", tier, q.asked[0][0])
		}
	}
}

func TestAReadWaitsForAFreedSlot(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	q := &slots{script: []error{pgx.ErrNoRows, pgx.ErrNoRows, nil}}
	waited, err := budget().take(context.Background(), q, admission.Standard, time.Now)
	if err != nil || !waited || len(q.asked) != 3 {
		t.Fatalf("waited %v, err %v, attempts %d", waited, err, len(q.asked))
	}
}

func TestAReadWithNoFreeSlotInMaxWaitIsShed(t *testing.T) {
	req.Covers(t, "NFR-CAP-002", "NFR-CAP-003")
	b := budget()
	start := time.Now()
	waited, err := b.take(context.Background(), &slots{}, admission.Standard, time.Now)
	var r *admission.Refusal
	if !errors.As(err, &r) || r.RetryAfter != 3*time.Second || !waited {
		t.Fatalf("waited %v, err %v", waited, err)
	}
	if took := time.Since(start); took > b.MaxWait+20*time.Millisecond {
		t.Errorf("the wait ran %v past max_wait %v", took, b.MaxWait)
	}
	b.MaxWait = 0
	if waited, err := b.take(context.Background(), &slots{}, admission.Critical, time.Now); !errors.Is(err, admission.ErrOverloaded) || waited {
		t.Errorf("max_wait 0 refuses after one attempt: waited %v, err %v", waited, err)
	}
}

func TestAFailedAttemptIsReturned(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	boom := errors.New("boom")
	if _, err := budget().take(context.Background(), &slots{script: []error{boom}}, admission.Critical, time.Now); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

func TestTheWaitForASlotEndsWithTheContext(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	b := budget()
	b.MaxWait, b.RetryInterval = time.Second, 500*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	waited, err := b.take(ctx, &slots{}, admission.Critical, time.Now)
	if !errors.Is(err, context.DeadlineExceeded) || !waited {
		t.Fatalf("waited %v, err %v", waited, err)
	}
}

// t0 is where the clock of a budgetedStore stands; deadlines are set from it
// a second ahead, so the real waits between attempts fit in them.
var t0 = time.Now()

// budgetedStore is a store whose clock stands still at t0 unless step moves
// it, with a 50ms reserve and floor.
func budgetedStore(step *time.Duration) *Store {
	return &Store{
		reserve: 50 * time.Millisecond, floor: 50 * time.Millisecond, request: time.Second,
		nowFn:  func() time.Time { return t0.Add(*step) },
		budget: budget(),
	}
}

func TestASlotTakenAtOnceKeepsTheStatementTimeout(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	var step time.Duration
	q := &slots{script: []error{nil}}
	if err := budgetedStore(&step).WithSharedBudget(budget()).takeSlot(context.Background(), q); err != nil || len(q.execs) != 0 {
		t.Fatalf("err %v, statement timeout set again %d times", err, len(q.execs))
	}
}

func TestAReadThatWaitedGetsWhatIsLeftOfItsDeadline(t *testing.T) {
	req.Covers(t, "NFR-CAP-002", "NFR-PERF-004")
	var step time.Duration
	st := budgetedStore(&step)
	ctx, cancel := context.WithDeadline(context.Background(), t0.Add(5*time.Second))
	defer cancel()
	q := &slots{script: []error{pgx.ErrNoRows, nil}}
	step = 300 * time.Millisecond
	if err := st.takeSlot(ctx, q); err != nil {
		t.Fatal(err)
	}
	if len(q.execs) != 1 || q.execs[0][0] != strconv.Itoa(4650) {
		t.Fatalf("statement timeout set to %v, want 4650 (5s - 300ms waited - 50ms reserve)", q.execs)
	}
}

func TestAReadThatWaitedPastItsFloorIsDeadlineExhausted(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	var step time.Duration
	st := budgetedStore(&step)
	ctx, cancel := context.WithDeadline(context.Background(), t0.Add(5*time.Second))
	defer cancel()
	step = 4950 * time.Millisecond
	q := &slots{script: []error{pgx.ErrNoRows, nil}}
	if err := st.takeSlot(ctx, q); !errors.Is(err, ErrDeadlineExhausted) || len(q.execs) != 0 {
		t.Fatalf("err %v, execs %d", err, len(q.execs))
	}
}

func TestAFailedSlotStatementIsStorageUnavailable(t *testing.T) {
	req.Covers(t, "NFR-CAP-002")
	var step time.Duration
	boom := &pgconn.PgError{Code: "08006"}
	if err := budgetedStore(&step).takeSlot(context.Background(), &slots{script: []error{boom}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a failed slot attempt: %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), t0.Add(5*time.Second))
	defer cancel()
	q := &slots{script: []error{pgx.ErrNoRows, nil}, execErr: boom}
	if err := budgetedStore(&step).takeSlot(ctx, q); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a failed statement timeout: %v", err)
	}
}

func TestARefusedSlotIsPassedOnAsOverloaded(t *testing.T) {
	req.Covers(t, "NFR-CAP-003")
	var step time.Duration
	st := budgetedStore(&step)
	st.budget.MaxWait = 0
	var r *admission.Refusal
	if err := st.takeSlot(context.Background(), &slots{}); !errors.As(err, &r) || r.RetryAfter != 3*time.Second {
		t.Fatalf("err %v", err)
	}
}
