package store

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// fakeServer speaks just enough of the PostgreSQL protocol for a pool: a
// trusted startup with a process id of its own per connection, an empty
// answer to every simple query, and cancel requests, which it counts.
type fakeServer struct {
	ln      net.Listener
	nextPID atomic.Uint32
	cancels atomic.Int32
	wg      sync.WaitGroup
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln}
	s.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Go(func() { s.serve(conn) })
		}
	})
	t.Cleanup(func() { _ = ln.Close(); s.wg.Wait() })
	return s
}

func (s *fakeServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	be := pgproto3.NewBackend(conn, conn)
	msg, err := be.ReceiveStartupMessage()
	if _, ssl := msg.(*pgproto3.SSLRequest); ssl {
		if _, err := conn.Write([]byte("N")); err != nil {
			return
		}
		msg, err = be.ReceiveStartupMessage()
	}
	if err != nil {
		return
	}
	if _, cancel := msg.(*pgproto3.CancelRequest); cancel {
		s.cancels.Add(1)
		return
	}
	be.Send(&pgproto3.AuthenticationOk{})
	be.Send(&pgproto3.BackendKeyData{ProcessID: s.nextPID.Add(1), SecretKey: []byte{0, 0, 0, 1}})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if be.Flush() != nil {
		return
	}
	for {
		msg, err := be.Receive()
		if err != nil {
			return
		}
		switch msg.(type) {
		case *pgproto3.Query:
			be.Send(&pgproto3.EmptyQueryResponse{})
			be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
			if be.Flush() != nil {
				return
			}
		case *pgproto3.Terminate:
			return
		}
	}
}

// TestAConnectionCutOffAfterItsLastStatementIsDestroyed is the race the
// abandoned mark exists for: the request's context ends just as the last
// statement completes, so the watcher has cut the connection off and sent a
// cancel request, yet the connection comes back idle (TxStatus 'I') and
// healthy. Reused, it would hand the next caller a connection a cancel
// request may still reach. The pool must destroy it: the next read gets a
// connection with another process id.
func TestAConnectionCutOffAfterItsLastStatementIsDestroyed(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	srv := newFakeServer(t)
	cfg, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_URL=postgres://reader@" + srv.ln.Addr().String() + "/viafrei?sslmode=disable",
		"VFAPI_DB_QUERY_EXEC_MODE=simple_protocol",
	}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	pc, err := PoolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// One connection: the next read gets the cut-off one unless it was
	// destroyed.
	pc.MaxConns, pc.MinConns = 1, 0
	var (
		mu       sync.Mutex
		handlers = map[*pgconn.PgConn]ctxwatch.Handler{}
	)
	build := pc.ConnConfig.BuildContextWatcherHandler
	pc.ConnConfig.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		h := build(c)
		mu.Lock()
		handlers[c] = h
		mu.Unlock()
		return h
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	c, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Conn().PgConn().PID()
	if _, err := c.Exec(ctx, ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	h := handlers[c.Conn().PgConn()]
	mu.Unlock()
	h.HandleCancel(ctx)
	h.HandleUnwatchAfterCancel()
	if s := c.Conn().PgConn().TxStatus(); s != 'I' || c.Conn().IsClosed() {
		t.Fatalf("the connection is not idle and open (%q): the case is not the race", s)
	}
	c.Release()

	next, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	if got := next.Conn().PgConn().PID(); got == pid {
		t.Fatalf("the cut-off connection (process %d) was handed out again", pid)
	}
	if _, err := next.Exec(ctx, ""); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
