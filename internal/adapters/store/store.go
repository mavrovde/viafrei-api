// Package store is the only code that knows the read contract. It reads
// through the service's read-only role, inside read-only transactions whose
// statement timeout is the request's remaining budget, and cancels the
// statement on the server as soon as the request's context ends.
package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/adapters/store/contractdb"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/breaker"
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
	pace    pace
	log     *slog.Logger
	breaker *breaker.Breaker
}

// WithBreaker puts the circuit breaker in front of every read (NFR-RES-002):
// while it is open a read is refused at once with ErrUnavailable, wrapping
// the *breaker.Refusal that says when to retry.
func (s *Store) WithBreaker(b *breaker.Breaker) *Store {
	s.breaker = b
	return s
}

// WithLogger names the logger of a read's panic that no caller is left to
// receive (the default is slog's).
func (s *Store) WithLogger(l *slog.Logger) *Store {
	s.log = l
	return s
}

// WithAdmission puts the priority admission in front of every read: a read
// first takes one of its permits, which equal the pool, so no read waits in
// the pool itself (RATE-008).
func (s *Store) WithAdmission(c *admission.Controller) *Store {
	s.admit = c
	return s
}

// execModes maps db.query_exec_mode to the driver's modes.
var execModes = map[string]pgx.QueryExecMode{
	"cache_statement": pgx.QueryExecModeCacheStatement,
	"cache_describe":  pgx.QueryExecModeCacheDescribe,
	"describe_exec":   pgx.QueryExecModeDescribeExec,
	"exec":            pgx.QueryExecModeExec,
	"simple_protocol": pgx.QueryExecModeSimpleProtocol,
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
	// Statements are prepared once per connection and reused: the per-request
	// cost is a bind and an execute (Performance-and-Caching).
	pc.ConnConfig.DefaultQueryExecMode = execModes[cfg.DB.QueryExecMode]
	pc.ConnConfig.StatementCacheCapacity = cfg.DB.StatementCacheCapacity
	pc.ConnConfig.DescriptionCacheCapacity = cfg.DB.StatementCacheCapacity
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
		pace:    realPace,
		log:     slog.Default(),
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

// Read runs fn in one read-only transaction and answers its error; see
// ReadValue. fn must not write anything the caller reads after Read returns:
// past the deadline it may still be running. A read with a result uses
// ReadValue, which hands the result back only to a caller still waiting.
func (s *Store) Read(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	_, err := ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (struct{}, error) {
		return struct{}{}, fn(ctx, tx)
	})
	return err
}

// ReadValue runs fn in one read-only, repeatable-read transaction (one
// snapshot for every statement of fn) bounded by the context's
// deadline (or deadline.request when the context has none) and answers what
// fn returned. The statement timeout is set to the remaining budget, so the
// server stops on its own even if the client-side cancel were lost.
//
// The answer is due at the deadline whatever the connection is doing
// (NFR-PERF-001). When the context ends, the driver sends a cancel request and
// abandons the connection only after db.cancel_grace, and both travel the
// same path as the read: on a slow path the read itself would return past
// the deadline. So the read runs on its own goroutine and ReadValue answers
// deadline-exhausted, with the zero T, the moment the context ends. The
// result is handed over through a channel, and only to a caller still
// waiting, so nothing the caller holds is written after ReadValue returns.
// That goroutine abandons the connection and only then gives the permit
// back, so storage is never asked for more than the pool while a cancel is
// still landing (NFR-PERF-002).
//
// With a breaker (WithBreaker), a read it refuses is answered at once with
// ErrUnavailable and never asks storage. An admitted read tells it how the
// read went (verdict) when the read itself ends, on the read's goroutine:
// a read whose caller stopped waiting still counts, once storage has
// answered it or failed to.
func ReadValue[T any](ctx context.Context, s *Store, fn func(context.Context, pgx.Tx) (T, error)) (T, error) {
	var zero T
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.request)
		defer cancel()
	}
	report := func(stage, error) {}
	if s.breaker != nil {
		ticket, err := s.breaker.Allow()
		if err != nil {
			return zero, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		report = func(reached stage, err error) { ticket.Done(verdict(ctx, reached, err)) }
	}
	if _, ok := s.Budget(ctx); !ok {
		report(refused, ErrDeadlineExhausted)
		return zero, ErrDeadlineExhausted
	}
	release := func() {}
	if s.admit != nil {
		r, err := s.admit.Acquire(ctx)
		if err != nil {
			report(refused, err)
			if errors.Is(err, admission.ErrOverloaded) {
				return zero, err
			}
			return zero, fmt.Errorf("%w: %w", ErrDeadlineExhausted, err)
		}
		release = r
	}
	// done is unbuffered and gone is closed by a caller that stops waiting,
	// so exactly one of the two takes the outcome, even when the read ends
	// as the deadline arrives: the caller, or the goroutine itself, which
	// then logs a panic nobody else will see.
	done, gone := make(chan outcome[T]), make(chan struct{})
	go func() {
		o := guarded(ctx, s, fn)
		release()
		report(o.reached, o.err)
		select {
		case done <- o:
		case <-gone:
			if o.panic != nil {
				s.log.Error("store: a read panicked after its deadline had passed", "panic", o.panic.Value, "stack", string(o.panic.Stack))
			}
		}
	}()
	select {
	case o := <-done:
		if o.panic != nil {
			panic(o.panic)
		}
		return o.value, o.err
	case <-ctx.Done():
		close(gone)
		return zero, fmt.Errorf("%w: %w", ErrDeadlineExhausted, ctx.Err())
	}
}

// ReadPanic is a panic in a read, raised again on the caller's goroutine
// with the stack of the goroutine it happened on, so the server's panic
// handling logs where it happened.
type ReadPanic struct {
	Value any
	Stack []byte
}

func (p *ReadPanic) Error() string { return fmt.Sprintf("%v\n\n%s", p.Value, p.Stack) }

// outcome is what the read goroutine hands back: the result and error of
// fn, or its panic, and how far the read got towards storage.
type outcome[T any] struct {
	value   T
	err     error
	panic   *ReadPanic
	reached stage
}

// stage is how far a read got towards storage.
type stage int

const (
	refused stage = iota // refused before storage was asked, or panicked: says nothing about it
	begun                // the transaction could not be opened
	opened               // the transaction was opened: storage answered
)

// verdict is what a read tells the breaker. A read refused before storage
// was asked tells nothing; one whose transaction could not be opened is a
// failure unless its caller went away. Once the transaction is open,
// storage has answered: only an error that says storage itself is down
// (storageDown) is a failure. A statement that ran out of time, a read shed
// by the shared budget, an error the server raised for one statement and a
// row the code could not decode are storage answering, so a fault confined
// to one request shape never opens the breaker for every other.
func verdict(ctx context.Context, reached stage, err error) breaker.Outcome {
	switch {
	case reached == refused:
		return breaker.Ignored
	case reached == begun && errors.Is(ctx.Err(), context.Canceled):
		return breaker.Ignored
	case reached == begun:
		return breaker.Failure
	case err != nil && ctx.Err() == nil && storageDown(err):
		return breaker.Failure
	default:
		return breaker.Success
	}
}

// storageDown reports whether err says storage itself is unavailable rather
// than that it refused one statement: the connection was lost (an end of
// stream, a closed or failed network connection), or the server answered
// with a SQLSTATE of class 08 (connection exception), 53 (insufficient
// resources), 58 (system error) or XX (internal error), or 57P01 to 57P03
// (the server is shutting down or cannot connect now).
func storageDown(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false // a deadline, which a context error also reports as a network timeout
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return len(pgErr.Code) == 5 && (strings.Contains(" 08 53 58 XX ", " "+pgErr.Code[:2]+" ") || pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	_, isNet := errors.AsType[net.Error](err)
	return isNet
}

// guarded runs the transaction and turns a panic into an outcome.
func guarded[T any](ctx context.Context, s *Store, fn func(context.Context, pgx.Tx) (T, error)) (o outcome[T]) {
	defer func() {
		if v := recover(); v != nil {
			o = outcome[T]{panic: &ReadPanic{Value: v, Stack: debug.Stack()}}
		}
	}()
	v, reached, err := transact(ctx, s, fn)
	return outcome[T]{value: v, err: err, reached: reached}
}

// transact runs fn in one read-only transaction whose statement timeout is
// what is left of the budget, and says how far it got towards storage.
func transact[T any](ctx context.Context, s *Store, fn func(context.Context, pgx.Tx) (T, error)) (v T, reached stage, err error) {
	// The wait for a permit spent part of the budget: what is left is the
	// statement timeout.
	budget, ok := s.Budget(ctx)
	if !ok {
		return v, refused, ErrDeadlineExhausted
	}
	// One round trip opens the read-only transaction and sets its statement
	// timeout; the budget is an integer of milliseconds, never caller text.
	// The isolation is repeatable read, so every statement of fn reads one
	// snapshot (under read committed each statement takes its own): a page
	// and the lamp over its scope cannot disagree. JIT compilation is off: a
	// read answers within deadline.request, and a plan whose estimate crosses
	// jit_above_cost (PostGIS functions are costed high, so a corridor read
	// over a few rows does) spent over two seconds compiling for a millisecond
	// of execution, so the read ran out of its deadline before it started.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		BeginQuery: "begin transaction isolation level repeatable read read only; set local jit = off; set local statement_timeout = " + strconv.FormatInt(budget.Milliseconds(), 10),
	})
	if err != nil {
		return v, begun, classify(ctx, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if s.budget.Slots > 0 {
		if err := s.takeSlot(ctx, tx); err != nil {
			return v, opened, err
		}
	}
	if v, err = fn(ctx, tx); err != nil {
		return v, opened, classify(ctx, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return v, opened, classify(ctx, err)
	}
	return v, opened, nil
}

// takeSlot holds a slot of the shared budget for tx (NFR-CAP-002). A read
// that had to wait for its slot gets the statement timeout set again to
// what is left of its budget.
func (s *Store) takeSlot(ctx context.Context, tx slotTx) error {
	waited, err := s.budget.take(ctx, tx, admission.SlotTierOf(ctx), s.nowFn, s.pace)
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
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (string, error) {
		return contractdb.New(tx).ContractVersion(ctx)
	})
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
