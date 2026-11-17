package accessstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// fakeConn is a listener connection driven by the test.
type fakeConn struct {
	mu        sync.Mutex
	execs     []string
	failOn    string // an Exec whose SQL starts with it fails
	notes     chan *pgconn.Notification
	waitErr   chan error
	closed    bool
	listening chan string
}

func newFakeConn() *fakeConn {
	return &fakeConn{notes: make(chan *pgconn.Notification, 4), waitErr: make(chan error, 1), listening: make(chan string, 16)}
}

func (c *fakeConn) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.execs = append(c.execs, sql)
	if c.failOn != "" && strings.HasPrefix(sql, c.failOn) {
		return pgconn.CommandTag{}, errors.New("exec failed")
	}
	c.listening <- sql
	return pgconn.CommandTag{}, nil
}

func (c *fakeConn) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	select {
	case n := <-c.notes:
		return n, nil
	case err := <-c.waitErr:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *fakeConn) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// connector hands out the given connections in turn, failing first when
// failFirst is set.
type connector struct {
	mu        sync.Mutex
	conns     []*fakeConn
	failFirst bool
	calls     int
}

func (k *connector) connect(context.Context) (listenConn, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls++
	if k.failFirst && k.calls == 1 {
		return nil, errors.New("no connection")
	}
	c := k.conns[0]
	if len(k.conns) > 1 {
		k.conns = k.conns[1:]
	}
	return c, nil
}

func poked(t *testing.T, p chan struct{}, what string) {
	t.Helper()
	select {
	case <-p:
	case <-time.After(2 * time.Second):
		t.Fatalf("not poked: %s", what)
	}
}

func execd(t *testing.T, c *fakeConn, want string) {
	t.Helper()
	select {
	case got := <-c.listening:
		if got != want {
			t.Fatalf("exec %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no exec, want %q", want)
	}
}

const listenSQL = `listen "live_refresh_settled"`

func TestSettlesListensPokesAndForgets(t *testing.T) {
	c := newFakeConn()
	k := &connector{conns: []*fakeConn{c}, failFirst: true}
	h := newSettles(k.connect, time.Millisecond)
	defer h.close()

	p := h.add("7")
	q := h.add("8")
	execd(t, c, listenSQL)
	poked(t, p, "listening")
	poked(t, q, "listening")
	c.notes <- &pgconn.Notification{Channel: "elsewhere", Payload: "7"}
	c.notes <- &pgconn.Notification{Channel: "live_refresh_settled", Payload: "8"}
	poked(t, q, "the settle of 8")
	c.notes <- &pgconn.Notification{Channel: "live_refresh_settled", Payload: "7"}
	poked(t, p, "the settle of 7")
	h.remove("7", p)
	h.remove("8", q)
	h.mu.Lock()
	left := len(h.waiters)
	h.mu.Unlock()
	if left != 0 || k.calls != 2 {
		t.Fatalf("%d ids left, %d connects, want none and a failed one and an open one", left, k.calls)
	}
}

func TestSettlesOpensALostConnectionAgain(t *testing.T) {
	for _, fail := range []string{"listen", ""} {
		t.Run(fail, func(t *testing.T) {
			c1, c2 := newFakeConn(), newFakeConn()
			c1.failOn = fail
			k := &connector{conns: []*fakeConn{c1, c2}}
			h := newSettles(k.connect, time.Millisecond)
			defer h.close()

			p := h.add("7")
			if fail == "" {
				execd(t, c1, listenSQL)
				poked(t, p, "listening on c1")
				c1.waitErr <- errors.New("connection lost")
			}
			execd(t, c2, listenSQL)
			poked(t, p, "listening on c2")
			c1.mu.Lock()
			closed := c1.closed
			c1.mu.Unlock()
			if !closed {
				t.Fatal("the lost connection was not closed")
			}
		})
	}
}

func TestSettlesCloseWithoutAWait(t *testing.T) {
	h := newSettles((&connector{}).connect, time.Millisecond)
	h.close()
	h.remove("1", make(chan struct{}))
}
