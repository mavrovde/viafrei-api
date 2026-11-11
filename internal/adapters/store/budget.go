package store

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
)

// SharedBudget is the storage concurrency budget this service shares with
// the other readers of the store (NFR-CAP-002). The budget is Slots
// transaction-scoped advisory locks with the two-key form (LockSpace, n),
// n from 1 to Slots: a read takes the first free one inside its read-only
// transaction and holds it until the transaction ends, so the store never
// runs more statements of all readers together than there are slots. Any
// other reader joins the budget by taking its slots the same way from the
// same lock space; a reader that crashes releases its slot with its
// session. The highest CriticalReserved slots are kept for the critical
// tier.
type SharedBudget struct {
	// Slots is the size of the budget; 0 switches it off.
	Slots int
	// LockSpace is the first key of every slot's advisory lock.
	LockSpace int32
	// CriticalReserved slots only a critical read may take.
	CriticalReserved int
	// MaxWait is the longest a read waits for a slot.
	MaxWait time.Duration
	// RetryInterval is the pause between two attempts.
	RetryInterval time.Duration
	// RetryAfter is the Retry-After of a read that got no slot.
	RetryAfter time.Duration
}

// slotSQL takes the first free slot of 1..$1 in lock space $2 and returns
// its number, or no row when every one is held.
//
// That a read holds one slot at most rests on how PostgreSQL evaluates this
// exact shape: the WHERE filter runs lazily, row by row, under LIMIT 1, so
// the scan stops at the first lock taken and tries no later one. The
// PostgreSQL manual warns that advisory locks taken in a query with LIMIT
// are not guaranteed this way in general: an ORDER BY, a materialised CTE,
// a subquery the planner evaluates first, or any plan that runs the filter
// on more rows than it returns would take several locks and hold them all
// to the end of the transaction. Every reader of the budget, this one and
// any other, must therefore send exactly this statement, with no ORDER BY
// and no CTE (docs/rate-limits.md, gate 5, step 1).
// TestAReadHoldsOneSharedSlotUntilItsTransactionEnds pins it against a real
// server: a read holds exactly one slot while it runs.
const slotSQL = `select s from generate_series(1, $1::int) s where pg_try_advisory_xact_lock($2::int, s) limit 1`

// rowQuerier is the part of a transaction a slot is taken through.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// slotTx is the part of a transaction a read takes its slot through and, if
// it waited for one, sets its statement timeout again.
type slotTx interface {
	rowQuerier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// WithSharedBudget makes every read take a slot of b first.
func (s *Store) WithSharedBudget(b SharedBudget) *Store {
	s.budget = b
	return s
}

// jittered spreads a retry interval over ±50 %: frac in [0, 1) maps to
// [d/2, 3d/2), so readers that found every slot held at the same moment do
// not try again in lockstep.
func jittered(d time.Duration, frac float64) time.Duration {
	return d/2 + time.Duration(frac*float64(d))
}

// take holds one slot of the budget for the transaction q runs in. It
// retries after RetryInterval, ±50 %, while all slots open to tier are held, and
// gives up with an *admission.Refusal once another attempt would end after
// MaxWait. waited reports whether a first attempt failed, so the caller
// knows the statement budget has shrunk.
func (b SharedBudget) take(ctx context.Context, q rowQuerier, tier admission.Tier, now func() time.Time) (waited bool, err error) {
	open := b.Slots
	if tier != admission.Critical {
		open -= b.CriticalReserved
	}
	start := now()
	for {
		var slot int32
		err := q.QueryRow(ctx, slotSQL, open, b.LockSpace).Scan(&slot)
		if !errors.Is(err, pgx.ErrNoRows) {
			return waited, err
		}
		pause := jittered(b.RetryInterval, rand.Float64()) //nolint:gosec // jitter, not a secret
		if now().Sub(start)+pause > b.MaxWait {
			return waited, &admission.Refusal{RetryAfter: b.RetryAfter}
		}
		waited = true
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return waited, ctx.Err()
		case <-timer.C:
		}
	}
}
