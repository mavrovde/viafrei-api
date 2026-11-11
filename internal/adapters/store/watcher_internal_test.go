package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// slowTarget is a server connection over a slow link: its socket is one
// end of a pipe nobody writes to, and its cancel request takes as long as
// its context allows.
type slowTarget struct {
	conn     net.Conn
	cancelAt chan time.Time // when the cancel request's context ends
}

func (s *slowTarget) Conn() net.Conn { return s.conn }

func (s *slowTarget) CancelRequest(ctx context.Context) error {
	<-ctx.Done()
	s.cancelAt <- time.Now()
	return ctx.Err()
}

func newSlowTarget(t *testing.T) *slowTarget {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return &slowTarget{conn: client, cancelAt: make(chan time.Time, 1)}
}

// TestACutOffReadReturnsAtOnce is the slow-storage case: when the request's
// context ends while a read waits on a slow link, the read returns at once,
// not after db.cancel_grace, and neither the cancel nor the unwatch waits
// for the cancel request. The watcher pgconn ships (DeadlineDelay = grace,
// unwatch waiting for the cancel request) fails both bounds.
func TestACutOffReadReturnsAtOnce(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	const grace = 400 * time.Millisecond
	const bound = 50 * time.Millisecond
	target := newSlowTarget(t)
	gone := &abandoned{}
	w := newDeadlineWatcher(target, target, grace, gone)
	read := make(chan error, 1)
	go func() {
		_, err := target.conn.Read(make([]byte, 1))
		read <- err
	}()
	start := time.Now()
	w.HandleCancel(context.Background())
	if took := time.Since(start); took > bound {
		t.Fatalf("HandleCancel took %s; it must not wait for the cancel request", took)
	}
	select {
	case err := <-read:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("the read ended with %v, want the connection deadline", err)
		}
		if took := time.Since(start); took > bound {
			t.Fatalf("the read returned %s after its context ended; the bound is %s", took, bound)
		}
	case <-time.After(grace / 2):
		t.Fatal("the read still waits on the slow link after its context ended")
	}
	unwatch := time.Now()
	w.HandleUnwatchAfterCancel()
	if took := time.Since(unwatch); took > bound {
		t.Fatalf("HandleUnwatchAfterCancel took %s; it must not wait for the cancel request", took)
	}
	// The cancel request still runs, bounded by the grace.
	select {
	case at := <-target.cancelAt:
		if d := at.Sub(start); d < grace || d > grace+250*time.Millisecond {
			t.Fatalf("the cancel request was given %s, want db.cancel_grace %s", d, grace)
		}
	case <-time.After(grace + time.Second):
		t.Fatal("no cancel request was sent")
	}
	if !gone.take(target) {
		t.Fatal("a cut-off connection was not marked for destruction")
	}
	// The deadline is lifted for a connection that is still open.
	if err := target.conn.SetDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// TestACutOffConnectionIsNeverReused holds the pool rule: a connection whose
// read was cut off is destroyed on release, whatever its state, because a
// cancel request still on its way could stop the next caller's statement.
func TestACutOffConnectionIsNeverReused(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	gone := &abandoned{}
	pg := &pgconn.PgConn{}
	gone.mark(pg)
	if !gone.take(pg) || gone.take(pg) {
		t.Fatal("a mark is taken exactly once")
	}
	gone.mark(pg)
	gone.forget(pg)
	if gone.take(pg) {
		t.Fatal("a closed connection's mark is forgotten")
	}
	w := newDeadlineWatcher(newSlowTarget(t), pg, time.Second, gone)
	if got := fmt.Sprint(w); got != "deadline watcher, cancel grace 1s" {
		t.Fatalf("watcher %q", got)
	}
}
