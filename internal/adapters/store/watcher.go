package store

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
)

// cancelTarget is the part of a server connection the watcher uses;
// *pgconn.PgConn is one.
type cancelTarget interface {
	Conn() net.Conn
	CancelRequest(ctx context.Context) error
}

// abandoned remembers the connections whose read was cut off by its
// context, so the pool destroys them instead of handing them out again: a
// cancel request still on its way could otherwise stop the next caller's
// statement.
type abandoned struct{ conns sync.Map }

func (a *abandoned) mark(c any)      { a.conns.Store(c, struct{}{}) }
func (a *abandoned) take(c any) bool { _, ok := a.conns.LoadAndDelete(c); return ok }
func (a *abandoned) forget(c any)    { a.conns.Delete(c) }
func (a *abandoned) reusable(c *pgx.Conn) bool {
	return !a.take(c.PgConn()) && c.PgConn().TxStatus() == 'I'
}

// deadlineWatcher ends a read the moment its context ends (NFR-PERF-001).
// The connection's own deadline is set to now, so a call waiting on a slow
// network returns at once and never past the request's deadline; the
// statement is stopped on the server by a cancel request sent on its own
// connection, bounded by db.cancel_grace, that nobody waits for. pgconn's
// CancelRequestContextWatcherHandler instead kept the connection open for
// the grace period after the context ended and made the caller wait for the
// cancel request to finish, so over a slow link a read returned up to the
// grace after the request's deadline.
type deadlineWatcher struct {
	target  cancelTarget
	key     any
	grace   time.Duration
	gone    *abandoned
	sending func(func()) // runs the cancel request; a goroutine outside tests
}

func newDeadlineWatcher(target cancelTarget, key any, grace time.Duration, gone *abandoned) *deadlineWatcher {
	return &deadlineWatcher{target: target, key: key, grace: grace, gone: gone, sending: func(f func()) { go f() }}
}

// String names the watcher and its grace, for configuration checks.
func (w *deadlineWatcher) String() string {
	return fmt.Sprintf("deadline watcher, cancel grace %s", w.grace)
}

func (w *deadlineWatcher) HandleCancel(ended context.Context) {
	w.gone.mark(w.key)
	// A connection already closed has nothing to interrupt.
	_ = w.target.Conn().SetDeadline(time.Now())
	w.sending(func() {
		// The read's context has ended; the cancel request keeps its values
		// and gets a bound of its own.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ended), w.grace)
		defer cancel()
		// The statement timeout stops the statement if this never lands.
		_ = w.target.CancelRequest(ctx)
	})
}

func (w *deadlineWatcher) HandleUnwatchAfterCancel() {
	_ = w.target.Conn().SetDeadline(time.Time{})
}

// watcherFor builds the context watcher of each new server connection.
func watcherFor(grace time.Duration, gone *abandoned) func(*pgconn.PgConn) ctxwatch.Handler {
	return func(c *pgconn.PgConn) ctxwatch.Handler { return newDeadlineWatcher(c, c, grace, gone) }
}
