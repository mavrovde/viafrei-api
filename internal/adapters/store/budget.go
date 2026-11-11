package store

import (
	"context"
	"errors"
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
// its number, or no row when every one is held. The filter runs row by row
// and LIMIT stops at the first lock taken, so a read holds one slot at most.
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

// take holds one slot of the budget for the transaction q runs in. It
// retries every RetryInterval while all slots open to tier are held, and
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
		if now().Sub(start)+b.RetryInterval > b.MaxWait {
			return waited, &admission.Refusal{RetryAfter: b.RetryAfter}
		}
		waited = true
		timer := time.NewTimer(b.RetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return waited, ctx.Err()
		case <-timer.C:
		}
	}
}
