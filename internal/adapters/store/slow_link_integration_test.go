//go:build integration

package store_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// slowLink is a TCP proxy to the test database whose answers, while slow is
// set, reach the client only after delay: a slow network path to storage.
type slowLink struct {
	ln    net.Listener
	to    string
	delay time.Duration
	slow  atomic.Bool
	done  chan struct{}
	wg    sync.WaitGroup
}

func newSlowLink(t *testing.T, upstream string, delay time.Duration) *slowLink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &slowLink{ln: ln, to: upstream, delay: delay, done: make(chan struct{})}
	l.wg.Go(l.accept)
	t.Cleanup(func() {
		close(l.done)
		_ = ln.Close()
		l.wg.Wait()
	})
	return l
}

func (l *slowLink) accept() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			return
		}
		u, err := net.Dial("tcp", l.to)
		if err != nil {
			_ = c.Close()
			continue
		}
		go func() { <-l.done; _ = c.Close(); _ = u.Close() }()
		l.wg.Go(func() { _, _ = io.Copy(u, c); _ = u.Close() })
		l.wg.Go(func() { l.answer(c, u) })
	}
}

// answer copies the server's bytes to the client, each chunk held back by
// the delay when it arrived while the link was slow, in order.
func (l *slowLink) answer(client, server net.Conn) {
	type chunk struct {
		due time.Time
		b   []byte
	}
	q := make(chan chunk, 1024)
	l.wg.Go(func() {
		defer func() { _ = client.Close() }()
		for c := range q {
			select {
			case <-time.After(time.Until(c.due)):
			case <-l.done:
				return
			}
			if _, err := client.Write(c.b); err != nil {
				return
			}
		}
	})
	defer close(q)
	buf := make([]byte, 32<<10)
	for {
		n, err := server.Read(buf)
		if n > 0 {
			due := time.Now()
			if l.slow.Load() {
				due = due.Add(l.delay)
			}
			q <- chunk{due: due, b: append([]byte(nil), buf[:n]...)}
		}
		if err != nil {
			return
		}
	}
}

// TestASlowLinkNeverHoldsAReadPastItsDeadline is the slow-storage case of
// the load tests: the server answers in time, but the answer is still on
// the wire when the request's deadline passes. The read must end at the
// deadline. With pgconn's own cancel watcher the connection stayed open for
// db.cancel_grace after the deadline (and the caller waited for the cancel
// request), so the read ended at the deadline plus the grace, which is what
// put answers past the 2 s ceiling.
func TestASlowLinkNeverHoldsAReadPastItsDeadline(t *testing.T) {
	req.Covers(t, "NFR-PERF-001", "NFR-PERF-004")
	ctx := bounded(t)
	u, err := url.Parse(db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	link := newSlowLink(t, u.Host, 1500*time.Millisecond)
	u.Host = link.ln.Addr().String()
	st, err := store.Open(ctx, configFor(t, u.String(), "VFAPI_DB_CANCEL_GRACE=400ms"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	const deadline = 500 * time.Millisecond
	short, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	start := time.Now()
	err = st.Read(short, func(ctx context.Context, tx pgx.Tx) error {
		link.slow.Store(true)
		var one int
		return tx.QueryRow(ctx, "select 1").Scan(&one)
	})
	took := time.Since(start)
	link.slow.Store(false)
	if !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("want deadline exhausted, got %v", err)
	}
	// Well inside the grace: the old watcher returned at deadline + 400ms.
	if took > deadline+50*time.Millisecond {
		t.Fatalf("the read returned %s after start; its deadline was %s", took, deadline)
	}
	// The cut-off connection is never handed out again: the next read gets a
	// fresh one and succeeds.
	var one int
	if err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select 1").Scan(&one)
	}); err != nil || one != 1 {
		t.Fatalf("the read after a cut-off failed: %v", err)
	}
}

func configFor(t *testing.T, dbURL string, extra ...string) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.Options{Environ: append([]string{"VFAPI_DB_URL=" + dbURL}, extra...), RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
