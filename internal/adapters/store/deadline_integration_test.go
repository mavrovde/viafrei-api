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
// a one-permit admission in front of it.
func slowStore(t *testing.T, ctx context.Context) (*store.Store, *slowPath) {
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
	return st.WithAdmission(admission.New(wire.AdmissionSettings(cfg), nil)), p
}

func selectOne(ctx context.Context, tx pgx.Tx) error {
	var one int
	return tx.QueryRow(ctx, "select 1").Scan(&one)
}

// TestTheAnswerDoesNotWaitForASlowConnection: with every byte from storage
// held back past the deadline, the read still ends at the deadline. The
// driver abandons the connection only after db.cancel_grace (and its cancel
// request crosses the same slow path), so a read that waited for the
// connection answered past the deadline: the slow-storage defect of #19.
func TestTheAnswerDoesNotWaitForASlowConnection(t *testing.T) {
	req.Covers(t, "NFR-PERF-001", "NFR-PERF-002")
	ctx := bounded(t)
	st, p := slowStore(t, ctx)
	// A pooled connection first, so the slowed read is a query, not a connect.
	if err := st.Read(ctx, selectOne); err != nil {
		t.Fatal(err)
	}
	p.delay.Store(int64(time.Second))
	const deadline = 300 * time.Millisecond
	short, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	start := time.Now()
	err := st.Read(short, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "select pg_sleep(5)")
		return err
	})
	took := time.Since(start)
	if !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("want deadline exhausted, got %v", err)
	}
	// The deadline and the cancel grace are 300ms and 100ms: the answer is
	// due at the first, not after the second.
	if took > deadline+50*time.Millisecond {
		t.Fatalf("the read returned %s after start against a %s deadline", took, deadline)
	}
	// The permit comes back once the abandoned connection is gone, and the
	// next read gets a fresh connection: nothing is poisoned.
	p.delay.Store(0)
	if err := st.Read(ctx, selectOne); err != nil {
		t.Fatalf("the read after the slow one: %v", err)
	}
}

// TestAPanicInAReadReachesTheCaller: fn runs on the read's own goroutine,
// and its panic is raised again on the caller's, where the server's panic
// handling answers it.
func TestAPanicInAReadReachesTheCaller(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	ctx := bounded(t)
	st := openStore(t, ctx)
	defer func() {
		if v := recover(); v != "boom" {
			t.Fatalf("want the panic of fn, got %v", v)
		}
	}()
	_ = st.Read(ctx, func(context.Context, pgx.Tx) error { panic("boom") })
	t.Fatal("Read returned after fn panicked")
}
