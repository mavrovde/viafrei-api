//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// testBound is the ceiling on every test body (TST-017).
const testBound = 2 * time.Second

var db *pgtest.DB

func TestMain(m *testing.M) {
	// Starting the container is setup, outside every test body's bound.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	var err error
	db, err = pgtest.Start(ctx)
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", err)
		os.Exit(1)
	}
	err = seed(ctx)
	cancel()
	if err != nil {
		db.Stop()
		fmt.Fprintln(os.Stderr, "loading the fixtures:", err)
		os.Exit(1)
	}
	code := m.Run()
	db.Stop()
	os.Exit(code)
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	t.Cleanup(cancel)
	return ctx
}

func readerConfig(t *testing.T, extra ...string) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.Options{Environ: append([]string{"VFAPI_DB_URL=" + db.ReaderURL}, extra...), RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func openStore(t *testing.T, ctx context.Context, extra ...string) *store.Store {
	t.Helper()
	st, err := store.Open(ctx, readerConfig(t, extra...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestReaderRoleCannotWrite(t *testing.T) {
	req.Covers(t, "NFR-SEC-004")
	ctx := bounded(t)
	conn, err := pgx.Connect(ctx, db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	if _, err := conn.Exec(ctx, "insert into contract.publisher (publisher_id, display_name) values ('x', 'x')"); sqlState(err) != "25006" {
		t.Errorf("a default session must be read-only (25006), got %v", err)
	}
	// Even a session that switches itself to read-write holds no write grant.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "insert into contract.publisher (publisher_id, display_name) values ('x', 'x')"); sqlState(err) != "42501" {
		t.Errorf("an insert must be refused by privilege (42501), got %v", err)
	}
	_ = tx.Rollback(ctx)
	tx, err = conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "create table public.planted (x int)"); sqlState(err) != "42501" {
		t.Errorf("creating a table must be refused (42501), got %v", err)
	}
	_ = tx.Rollback(ctx)
}

func TestWriteThroughStoreRefused(t *testing.T) {
	req.Covers(t, "NFR-SEC-004")
	ctx := bounded(t)
	st := openStore(t, ctx)
	err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "delete from contract.contract_info")
		return err
	})
	if !errors.Is(err, store.ErrUnavailable) || sqlState(err) != "25006" {
		t.Fatalf("a write inside the store's read must be refused as read-only, got %v", err)
	}
	var v string
	if err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select contract_version from contract.contract_info").Scan(&v)
	}); err != nil || v != contract.Version() {
		t.Fatalf("the row must still be there: %q %v", v, err)
	}
}

func TestReaderRoleLimits(t *testing.T) {
	req.Covers(t, "NFR-SEC-004")
	ctx := bounded(t)
	owner, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(context.WithoutCancel(ctx)) }()
	var super, inherit bool
	var limit int
	if err := owner.QueryRow(ctx, "select rolsuper, rolinherit, rolconnlimit from pg_roles where rolname = $1", contract.ReaderRole).Scan(&super, &inherit, &limit); err != nil {
		t.Fatal(err)
	}
	// 16 is the largest db.pool_max (db/contract TestRoleLimitCoversPoolMax).
	if super || inherit || limit != 16 {
		t.Fatalf("role attributes: super=%v inherit=%v connlimit=%d", super, inherit, limit)
	}
	conn, err := pgx.Connect(ctx, db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for setting, want := range map[string]string{
		"statement_timeout":                   "2s",
		"default_transaction_read_only":       "on",
		"idle_in_transaction_session_timeout": "5s",
	} {
		var got string
		if err := conn.QueryRow(ctx, "select current_setting($1)", setting).Scan(&got); err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", setting, got, err, want)
		}
	}
}

func TestStatementTimeoutIsTheRemainingBudget(t *testing.T) {
	req.Covers(t, "NFR-PERF-004", "CFG-006")
	ctx, cancel := context.WithTimeout(bounded(t), 800*time.Millisecond)
	defer cancel()
	st := openStore(t, ctx)
	var raw string
	if err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select current_setting('statement_timeout')").Scan(&raw)
	}); err != nil {
		t.Fatal(err)
	}
	ms, err := strconv.Atoi(raw[:len(raw)-2])
	if err != nil || raw[len(raw)-2:] != "ms" {
		t.Fatalf("statement_timeout %q is not the per-read budget in ms", raw)
	}
	// 800ms deadline minus the 50ms reserve, less the time already spent.
	if ms > 750 || ms < 100 {
		t.Fatalf("statement_timeout %dms is not the remaining budget", ms)
	}
}

func TestServerStopsTheStatementOnItsOwn(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	ctx := bounded(t)
	st := openStore(t, ctx)
	short, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()
	var limit, took time.Duration
	err := st.Read(short, func(ctx context.Context, tx pgx.Tx) error {
		var raw string
		if err := tx.QueryRow(ctx, "select current_setting('statement_timeout')").Scan(&raw); err != nil {
			return err
		}
		if len(raw) < 3 || raw[len(raw)-2:] != "ms" {
			return fmt.Errorf("statement_timeout %q is not the per-read budget in ms", raw)
		}
		ms, err := strconv.Atoi(raw[:len(raw)-2])
		if err != nil {
			return fmt.Errorf("statement_timeout %q is not the per-read budget in ms", raw)
		}
		limit = time.Duration(ms) * time.Millisecond
		// Detach the client-side cancel: only the statement timeout can stop it. Only the
		// statement is timed: the begin, this read and the rollback are outside its timeout,
		// and timing them too made the bound fail on a loaded machine.
		start := time.Now()
		_, err = tx.Exec(context.WithoutCancel(ctx), "select pg_sleep(5)")
		took = time.Since(start)
		return err
	})
	if !errors.Is(err, store.ErrDeadlineExhausted) || sqlState(err) != "57014" {
		t.Fatalf("want the server-side statement timeout (57014), got %v", err)
	}
	// The server stops it at its own timeout: not before, and not long after.
	if took < limit || took > limit+250*time.Millisecond {
		t.Fatalf("the statement ran %s against a %s statement timeout", took, limit)
	}
}

func TestDeadlineCancelsTheStatementOnTheServer(t *testing.T) {
	req.Covers(t, "NFR-PERF-004", "CFG-006")
	ctx := bounded(t)
	const app = "deadline-cancel-probe"
	st := openStore(t, ctx, "VFAPI_DB_APPLICATION_NAME="+app, "VFAPI_DB_CANCEL_GRACE=100ms")
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := st.Read(short, func(ctx context.Context, tx pgx.Tx) error {
		// Lift the statement timeout so only the client-side cancel can stop it.
		if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "select pg_sleep(5)")
		return err
	})
	took := time.Since(start)
	if !errors.Is(err, store.ErrDeadlineExhausted) {
		t.Fatalf("want deadline exhausted, got %v", err)
	}
	if took > 300*time.Millisecond+100*time.Millisecond+200*time.Millisecond {
		t.Fatalf("the read returned %s after start; the deadline is 300ms and the grace 100ms", took)
	}
	owner, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(context.WithoutCancel(ctx)) }()
	// The cancel request must have stopped the backend, not merely abandoned it.
	for {
		var active int
		if err := owner.QueryRow(ctx, "select count(*) from pg_stat_activity where application_name = $1 and state = 'active' and query like '%pg_sleep%'", app).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active == 0 {
			return
		}
		if time.Since(start) > time.Second {
			t.Fatalf("%d backend(s) still running the statement after the deadline", active)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestApplicationNameReachesTheServer(t *testing.T) {
	req.Covers(t, "CFG-006")
	ctx := bounded(t)
	st := openStore(t, ctx, "VFAPI_DB_APPLICATION_NAME=configured-name")
	var got string
	if err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select current_setting('application_name')").Scan(&got)
	}); err != nil || got != "configured-name" {
		t.Fatalf("application_name = %q (%v)", got, err)
	}
}

func TestReadinessAgainstARealDatabase(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	ctx := bounded(t)
	st := openStore(t, ctx)
	expected, err := health.ExpectedVersion("", contract.Version())
	if err != nil {
		t.Fatal(err)
	}
	if r := health.NewChecker(st, expected, 500*time.Millisecond).Check(ctx); !r.Ready {
		t.Fatalf("not ready against the stub: %v", r.Checks)
	}
	newer, _ := health.ExpectedVersion("0.9", "")
	r := health.NewChecker(st, newer, 500*time.Millisecond).Check(ctx)
	if r.Ready || r.Checks[health.CheckStorage] != health.Pass || r.Checks[health.CheckContract] != health.Fail {
		t.Fatalf("a newer required contract must fail the contract check only: %v", r.Checks)
	}
}

func TestUnreachableStorageIsNotReady(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	ctx := bounded(t)
	cfg, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_URL=postgres://nobody:x@localhost:1/none?sslmode=disable",
		"VFAPI_DB_POOL_MIN=0",
	}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	expected, _ := health.ExpectedVersion("", contract.Version())
	r := health.NewChecker(st, expected, 300*time.Millisecond).Check(ctx)
	if r.Ready || r.Checks[health.CheckStorage] != health.Fail {
		t.Fatalf("unreachable storage reported %v", r.Checks)
	}
}
