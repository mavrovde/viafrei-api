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

func TestSettlesListensPokesAndForgets(t *testing.T) {
	c := newFakeConn()
	k := &connector{conns: []*fakeConn{c}, failFirst: true}
	h := newSettles(k.connect, time.Millisecond)
	defer h.close()

	p := h.add("refresh_a")
	execd(t, c, `listen "refresh_a"`)
	poked(t, p, "listening")
	c.notes <- &pgconn.Notification{Channel: "elsewhere"}
	c.notes <- &pgconn.Notification{Channel: "refresh_a"}
	poked(t, p, "the settle")
	h.remove("refresh_a", p)
	execd(t, c, `unlisten "refresh_a"`)
	if k.calls != 2 {
		t.Fatalf("%d connects, want a failed one and an open one", k.calls)
	}
}

func TestSettlesOpensALostConnectionAgain(t *testing.T) {
	for _, fail := range []string{"listen", "unlisten", ""} {
		t.Run(fail, func(t *testing.T) {
			c1, c2 := newFakeConn(), newFakeConn()
			c1.failOn = fail
			k := &connector{conns: []*fakeConn{c1, c2}}
			h := newSettles(k.connect, time.Millisecond)
			defer h.close()

			p := h.add("refresh_a")
			switch fail {
			case "listen":
				// c1 refused the LISTEN: c2 listens.
			case "unlisten":
				execd(t, c1, `listen "refresh_a"`)
				poked(t, p, "listening on c1")
				q := h.add("refresh_b")
				execd(t, c1, `listen "refresh_b"`)
				h.remove("refresh_b", q) // c1 refuses the UNLISTEN
			default:
				execd(t, c1, `listen "refresh_a"`)
				poked(t, p, "listening on c1")
				c1.waitErr <- errors.New("connection lost")
			}
			execd(t, c2, `listen "refresh_a"`)
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
	h.remove("refresh_none", make(chan struct{}))
}
