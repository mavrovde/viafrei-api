// Package store is the only code that knows the read contract. It reads
// through the service's read-only role, inside read-only transactions whose
// statement timeout is the request's remaining budget, and cancels the
// statement on the server as soon as the request's context ends.
package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/adapters/store/contractdb"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/invariant"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

var (
	// ErrDeadlineExhausted means the request budget ran out before or during
	// the read. The handler answers 503 deadline-exhausted.
	ErrDeadlineExhausted = errors.New("store: deadline exhausted")
	// ErrUnavailable means storage could not be reached or failed. The
	// handler answers 503 upstream-unavailable.
	ErrUnavailable = errors.New("store: storage unavailable")
)

// Store reads the read contract.
type Store struct {
	pool    *pgxpool.Pool
	request time.Duration
	reserve time.Duration
	floor   time.Duration
	nowFn   func() time.Time
	admit   *admission.Controller
	budget  SharedBudget
}

// WithAdmission puts the priority admission in front of every read: a read
// first takes one of its permits, which equal the pool, so no read waits in
// the pool itself (RATE-008).
func (s *Store) WithAdmission(c *admission.Controller) *Store {
	s.admit = c
	return s
}

// PoolConfig translates the configuration into a pgx pool configuration.
// Exposed so tests can observe that each db.* key reaches the driver.
func PoolConfig(cfg *config.Config) (*pgxpool.Config, error) {
	pc, err := pgxpool.ParseConfig(cfg.DB.URL.Reveal())
	if err != nil {
		// Never wrap the parse error: it may quote the URL, password included.
		return nil, errors.New("store: db.url is not a valid connection URL")
	}
	pc.MaxConns = int32(cfg.DB.PoolMax) // #nosec G115 -- bounded 1..16 by the declaration
	pc.MinConns = int32(cfg.DB.PoolMin) // #nosec G115 -- bounded 0..16 by the declaration
	pc.MaxConnLifetime = cfg.DB.MaxConnLifetime
	pc.MaxConnIdleTime = cfg.DB.MaxConnIdle
	pc.ConnConfig.ConnectTimeout = cfg.DB.ConnectTimeout
	pc.ConnConfig.RuntimeParams["application_name"] = cfg.DB.ApplicationName
	grace := cfg.DB.CancelGrace
	// When a request's context ends, send a PostgreSQL cancel request at once
	// and give it the grace period to land before the connection is abandoned.
	pc.ConnConfig.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, CancelRequestDelay: 0, DeadlineDelay: grace}
	}
	// A connection that comes back in any state other than idle is destroyed
	// instead of being returned to the pool.
	pc.AfterRelease = func(c *pgx.Conn) bool { return c.PgConn().TxStatus() == 'I' }
	return pc, nil
}

// Open creates the pool. It does not wait for storage: readiness reports it.
func Open(ctx context.Context, cfg *config.Config) (*Store, error) {
	pc, err := PoolConfig(cfg)
	if err != nil {
		return nil, err
	}
	// NewWithConfig fails only on a pool configuration PoolConfig never
	// builds (pool bounds are validated); it does not connect.
	return New(invariant.Must(pgxpool.NewWithConfig(ctx, pc)), cfg), nil
}

// New wraps an existing pool.
func New(pool *pgxpool.Pool, cfg *config.Config) *Store {
	return &Store{
		pool:    pool,
		request: cfg.Deadline.Request,
		reserve: cfg.Deadline.Reserve,
		floor:   cfg.DB.StatementTimeoutFloor,
		nowFn:   time.Now,
	}
}

// Close closes the pool.
func (s *Store) Close() { s.pool.Close() }

// StatementBudget returns the statement timeout for a read that must finish
// by deadline: the remaining time minus the reserve. ok is false when the
// budget is below the floor, so the read is refused without touching storage.
func StatementBudget(now, deadline time.Time, reserve, floor time.Duration) (budget time.Duration, ok bool) {
	budget = deadline.Sub(now) - reserve
	return budget, budget >= floor
}

// Budget is the statement timeout a read started now under ctx would get,
// and whether it reaches db.statement_timeout_floor. A context without a
// deadline is given deadline.request.
func (s *Store) Budget(ctx context.Context) (time.Duration, bool) {
	now := s.nowFn()
	deadline, has := ctx.Deadline()
	if !has {
		deadline = now.Add(s.request)
	}
	return StatementBudget(now, deadline, s.reserve, s.floor)
}

// Read runs fn in one read-only transaction bounded by the context's
// deadline (or deadline.request when the context has none). The statement
// timeout is set to the remaining budget, so the server stops on its own even
// if the client-side cancel were lost.
func (s *Store) Read(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.request)
		defer cancel()
	}
	if _, ok := s.Budget(ctx); !ok {
		return ErrDeadlineExhausted
	}
	if s.admit != nil {
		release, err := s.admit.Acquire(ctx)
		if err != nil {
			if errors.Is(err, admission.ErrOverloaded) {
				return err
			}
			return fmt.Errorf("%w: %w", ErrDeadlineExhausted, err)
		}
		defer release()
	}
	// The wait for a permit spent part of the budget: what is left is the
	// statement timeout.
	budget, ok := s.Budget(ctx)
	if !ok {
		return ErrDeadlineExhausted
	}
	// One round trip opens the read-only transaction and sets its statement
	// timeout; the budget is an integer of milliseconds, never caller text.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		BeginQuery: "begin transaction read only; set local statement_timeout = " + strconv.FormatInt(budget.Milliseconds(), 10),
	})
	if err != nil {
		return classify(ctx, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if s.budget.Slots > 0 {
		if err := s.takeSlot(ctx, tx); err != nil {
			return err
		}
	}
	if err := fn(ctx, tx); err != nil {
		return classify(ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return classify(ctx, err)
	}
	return nil
}

// takeSlot holds a slot of the shared budget for tx (NFR-CAP-002). A read
// that had to wait for its slot gets the statement timeout set again to
// what is left of its budget.
func (s *Store) takeSlot(ctx context.Context, tx slotTx) error {
	waited, err := s.budget.take(ctx, tx, admission.SlotTierOf(ctx), s.nowFn)
	if errors.Is(err, admission.ErrOverloaded) {
		return err
	}
	if err != nil {
		return classify(ctx, err)
	}
	if !waited {
		return nil
	}
	budget, ok := s.Budget(ctx)
	if !ok {
		return ErrDeadlineExhausted
	}
	if _, err := tx.Exec(ctx, "select set_config('statement_timeout', $1, true)", strconv.FormatInt(budget.Milliseconds(), 10)); err != nil {
		return classify(ctx, err)
	}
	return nil
}

// IsDeadline reports whether err means the request budget ran out.
func IsDeadline(err error) bool { return errors.Is(err, ErrDeadlineExhausted) }

// ContractVersion implements health.ContractReader.
func (s *Store) ContractVersion(ctx context.Context) (string, error) {
	var v string
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		v, err = contractdb.New(tx).ContractVersion(ctx)
		return err
	})
	return v, err
}

func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrDeadlineExhausted, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" { // query_canceled: the statement timeout fired
		return fmt.Errorf("%w: %w", ErrDeadlineExhausted, err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}
