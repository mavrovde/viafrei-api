//go:build integration

package store_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// slowPath is a TCP relay in front of storage that, once slowed, holds every
// chunk storage sends back for delay before passing it on: the latency on
// every storage byte of the slow-storage load scenario (TST-007), here in
// the test's own process. The cancel request travels it too.
type slowPath struct {
	ln    net.Listener
	delay atomic.Int64
	wg    sync.WaitGroup
}

func newSlowPath(t *testing.T, upstream string) *slowPath {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &slowPath{ln: ln}
	p.wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Go(func() { p.relay(c, upstream) })
		}
	})
	t.Cleanup(func() { _ = ln.Close(); p.wg.Wait() })
	return p
}

func (p *slowPath) relay(c net.Conn, upstream string) {
	defer func() { _ = c.Close() }()
	u, err := net.Dial("tcp", upstream)
	if err != nil {
		return
	}
	defer func() { _ = u.Close() }()
	go func() { _, _ = io.Copy(u, c); _ = u.Close() }()
	buf := make([]byte, 32<<10)
	for {
		n, err := u.Read(buf)
		if n > 0 {
			time.Sleep(time.Duration(p.delay.Load()))
			if _, werr := c.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// slowStore opens a store whose storage is reached through a slowPath, with
// a one-permit admission in front of it, and answers that admission too.
func slowStore(t *testing.T, ctx context.Context) (*store.Store, *slowPath, *admission.Controller) {
	t.Helper()
	u, err := url.Parse(db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	p := newSlowPath(t, u.Host)
	u.Host = p.ln.Addr().String()
	cfg, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_URL=" + u.String(), "VFAPI_DB_CANCEL_GRACE=100ms",
		"VFAPI_DB_POOL_MAX=1", "VFAPI_TIERS_CRITICAL_RESERVED=0", "VFAPI_TIERS_PER_KEY_INFLIGHT=1",
	}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	c := admission.New(wire.AdmissionSettings(cfg), nil)
	return st.WithAdmission(c), p, c
}

func selectOne(ctx context.Context, tx pgx.Tx) error {
	var one int
	return tx.QueryRow(ctx, "select 1").Scan(&one)
}

// TestTheAnswerDoesNotWaitForASlowConnection: storage slows down inside the
// read, after its transaction has begun, past the deadline. The read still
// ends at the deadline, hands the caller nothing (run with -race: fn's result
// is built after the deadline and must never reach a variable the caller
// reads), and keeps its permit until the connection has been abandoned.
//
// The driver abandons the connection only after db.cancel_grace, and its
// cancel request crosses the same slow path, so a read that waited for the
// connection answered past the deadline: the slow-storage defect of #19.
func TestTheAnswerDoesNotWaitForASlowConnection(t *testing.T) {
	req.Covers(t, "NFR-PERF-001", "NFR-PERF-002")
	ctx := bounded(t)
	st, p, c := slowStore(t, ctx)
	// A pooled connection first, so the slowed read is a query, not a connect.
	if err := st.Read(ctx, selectOne); err != nil {
		t.Fatal(err)
	}
	if free := c.Snapshot().Free; free != 1 {
		t.Fatalf("%d permits free before the slow read, want 1", free)
	}
	const deadline = 300 * time.Millisecond
	short, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	ran := make(chan struct{})
	start := time.Now()
	got, err := store.ReadValue(short, st, func(ctx context.Context, tx pgx.Tx) ([]int, error) {
		close(ran)
		// The begin has crossed at full speed; from here every byte storage
		// sends back is held for a second.
		p.delay.Store(int64(time.Second))
		var out []int
		rows, _ := tx.Query(ctx, "select generate_series(1, 3)")
		out, err := pgx.CollectRows(rows, pgx.RowTo[int])
		out = append(out, len(out))
		return out, err
	})
	took := time.Since(start)
	free := c.Snapshot().Free
	select {
	case <-ran:
	default:
		t.Fatal("fn never ran: the slowed path stalled the begin, not the read")
	}
	if !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("want deadline exhausted, got %v", err)
	}
	if got != nil {
		t.Fatalf("a read past its deadline answered %v, want nothing", got)
	}
	// The deadline and the cancel grace are 300ms and 100ms: the answer is
	// due at the first, not after the second.
	if took > deadline+50*time.Millisecond {
		t.Fatalf("the read returned %s after start against a %s deadline", took, deadline)
	}
	// The connection is abandoned only a cancel grace after the deadline, so
	// at the answer the permit must still be held (NFR-PERF-002); it comes
	// back once the connection is gone.
	if free != 0 {
		t.Fatalf("%d permits free at the early answer: the permit came back before the connection was abandoned", free)
	}
	for c.Snapshot().Free != 1 {
		if time.Since(start) > 1500*time.Millisecond {
			t.Fatal("the permit never came back after the connection was abandoned")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The next read gets a fresh connection: nothing is poisoned.
	p.delay.Store(0)
	if err := st.Read(ctx, selectOne); err != nil {
		t.Fatalf("the read after the slow one: %v", err)
	}
}

// TestAPanicInAReadReachesTheCaller: fn runs on the read's own goroutine,
// and its panic is raised again on the caller's, where the server's panic
// handling answers it, carrying the stack of the goroutine it happened on.
func TestAPanicInAReadReachesTheCaller(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	ctx := bounded(t)
	st := openStore(t, ctx)
	defer func() {
		p, ok := recover().(*store.ReadPanic)
		if !ok || p.Value != "boom" {
			t.Fatalf("want the panic of fn as a ReadPanic, got %v", p)
		}
		if !strings.Contains(string(p.Stack), "panicsInRead") || !strings.Contains(p.Error(), "boom") || !strings.Contains(p.Error(), "panicsInRead") {
			t.Fatalf("the re-raised panic lost the stack it happened on:\n%s", p.Error())
		}
	}()
	_ = st.Read(ctx, panicsInRead)
	t.Fatal("Read returned after fn panicked")
}

func panicsInRead(context.Context, pgx.Tx) error { panic("boom") }

// lockedBuffer is a log sink the read goroutine and the test share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestAPanicAfterTheDeadlineIsLogged: a read that panics when its caller has
// already been answered has nobody to raise the panic to; it is logged with
// its stack instead of being lost.
func TestAPanicAfterTheDeadlineIsLogged(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	ctx := bounded(t)
	var sink lockedBuffer
	st := openStore(t, ctx).WithLogger(slog.New(slog.NewJSONHandler(&sink, nil)))
	// A pooled connection first, so the read below reaches fn in time even
	// on a loaded machine.
	if err := st.Read(ctx, selectOne); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	entered, answered := make(chan struct{}), make(chan struct{})
	err := st.Read(short, func(ctx context.Context, _ pgx.Tx) error {
		close(entered)
		// fn panics only once its caller has been answered.
		<-answered
		panicLate()
		return nil
	})
	close(answered)
	select {
	case <-entered:
	default:
		t.Fatalf("fn never ran before the deadline (%v)", err)
	}
	if !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("want deadline exhausted, got %v", err)
	}
	for {
		if out := sink.String(); strings.Contains(out, "panicked after its deadline") {
			if !strings.Contains(out, "late boom") || !strings.Contains(out, "panicLate") {
				t.Fatalf("the log lacks the panic or its stack: %s", out)
			}
			return
		}
		if ctx.Err() != nil {
			t.Fatal("the panic after the deadline was never logged")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func panicLate() { panic("late boom") }
