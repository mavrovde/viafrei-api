package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestStatementBudget(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	now := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		left    time.Duration
		want    time.Duration
		allowed bool
	}{
		{2 * time.Second, 1950 * time.Millisecond, true},
		{100 * time.Millisecond, 50 * time.Millisecond, true},
		{99 * time.Millisecond, 49 * time.Millisecond, false},
		{-time.Second, -1050 * time.Millisecond, false},
	}
	for _, c := range cases {
		got, ok := store.StatementBudget(now, now.Add(c.left), 50*time.Millisecond, 50*time.Millisecond)
		if got != c.want || ok != c.allowed {
			t.Errorf("left %s: got %s %v, want %s %v", c.left, got, ok, c.want, c.allowed)
		}
	}
}

func TestReadBelowTheFloorNeverTouchesStorage(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	// A nil pool would panic if Read reached it.
	st := store.New(nil, config.Defaults())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	called := false
	err := st.Read(ctx, func(context.Context, pgx.Tx) error { called = true; return nil })
	if !errors.Is(err, store.ErrDeadlineExhausted) || called {
		t.Fatalf("got %v, called %v", err, called)
	}
}

func TestPoolConfigCarriesEveryDBKey(t *testing.T) {
	req.Covers(t, "CFG-006")
	cfg, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_URL=postgres://reader:pw@db.invalid:6000/x",
		"VFAPI_DB_POOL_MAX=7",
		"VFAPI_DB_POOL_MIN=3",
		"VFAPI_DB_CONNECT_TIMEOUT=1500ms",
		"VFAPI_DB_MAX_CONN_LIFETIME=7m",
		"VFAPI_DB_MAX_CONN_IDLE=90s",
		"VFAPI_DB_CANCEL_GRACE=250ms",
		"VFAPI_DB_APPLICATION_NAME=probe-name",
	}})
	if err != nil {
		t.Fatal(err)
	}
	pc, err := store.PoolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if pc.ConnConfig.Host != "db.invalid" || pc.ConnConfig.Port != 6000 {
		t.Errorf("db.url not used: %s:%d", pc.ConnConfig.Host, pc.ConnConfig.Port)
	}
	if pc.MaxConns != 7 || pc.MinConns != 3 {
		t.Errorf("pool bounds %d/%d", pc.MinConns, pc.MaxConns)
	}
	if pc.ConnConfig.ConnectTimeout != 1500*time.Millisecond || pc.MaxConnLifetime != 7*time.Minute || pc.MaxConnIdleTime != 90*time.Second {
		t.Errorf("timeouts %s %s %s", pc.ConnConfig.ConnectTimeout, pc.MaxConnLifetime, pc.MaxConnIdleTime)
	}
	if pc.ConnConfig.RuntimeParams["application_name"] != "probe-name" {
		t.Errorf("application_name %q", pc.ConnConfig.RuntimeParams["application_name"])
	}
	h, ok := pc.ConnConfig.BuildContextWatcherHandler(&pgconn.PgConn{}).(*pgconn.CancelRequestContextWatcherHandler)
	if !ok || h.DeadlineDelay != 250*time.Millisecond || h.CancelRequestDelay != 0 {
		t.Errorf("the context watcher must send a cancel request at once and wait db.cancel_grace: %#v", h)
	}
}

func TestBadURLNeverQuoted(t *testing.T) {
	req.Covers(t, "CFG-007")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=postgres://reader:planted-pw-55@host:notaport/x"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PoolConfig(cfg)
	if err == nil || strings.Contains(err.Error(), "planted-pw-55") {
		t.Fatalf("got %v", err)
	}
}

func TestBudgetUsesConfiguredDeadlineAndReserve(t *testing.T) {
	req.Covers(t, "CFG-006")
	cfg, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DEADLINE_REQUEST=1s",
		"VFAPI_DEADLINE_RESERVE=200ms",
		"VFAPI_DB_STATEMENT_TIMEOUT_FLOOR=300ms",
	}})
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(nil, cfg)
	budget, ok := st.Budget(context.Background())
	if !ok || budget < 790*time.Millisecond || budget > 800*time.Millisecond {
		t.Fatalf("no deadline: budget %s %v, want request 1s minus reserve 200ms", budget, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()
	if budget, ok := st.Budget(ctx); ok {
		t.Fatalf("a %s budget is below the 300ms floor and must be refused", budget)
	}
}
