//go:build integration

package fixtures_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// testBound is the ceiling on every test body (TST-017).
const testBound = 2 * time.Second

var (
	db        *pgtest.DB
	ciSeed    uint64 = 7
	ciLoadErr error
	ciCounts  map[string]int
)

// TestMain starts the database and loads the ci profile once: both are setup,
// outside every test body's bound. The tiny profile is loaded inside a test.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	var err error
	db, err = pgtest.Start(ctx)
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", err)
		os.Exit(1)
	}
	ciCounts, ciLoadErr = loadCommitted(ctx, "ci", ciSeed)
	cancel()
	code := m.Run()
	db.Stop()
	os.Exit(code)
}

func loadCommitted(ctx context.Context, profile string, seed uint64) (map[string]int, error) {
	var buf bytes.Buffer
	m, err := fixtures.Generate(&buf, profile, seed, contract.Version())
	if err != nil {
		return nil, err
	}
	conn, err := db.Owner(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, buf.String())
		return err
	})
	return m.Rows, err
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	t.Cleanup(cancel)
	return ctx
}

func countRows(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, table string) (int, error) {
	var n int
	// table comes from fixtures.Tables, a fixed list in this repository.
	err := q.QueryRow(ctx, "select count(*) from contract."+pgx.Identifier{table}.Sanitize()).Scan(&n) //nolint:misspell // the pgx method's name
	return n, err
}

// TestTinyLoadsIntoTheContract loads the tiny profile into the contract stub
// in a transaction that is rolled back, and compares every table's count with
// the manifest.
func TestTinyLoadsIntoTheContract(t *testing.T) {
	req.Covers(t, "TST-014")
	ctx := bounded(t)
	var buf bytes.Buffer
	m, err := fixtures.Generate(&buf, "tiny", 1, contract.Version())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// The ci set committed in TestMain uses the same identifiers, so the
	// tables are emptied inside this transaction first; the rollback brings
	// the ci set back for the other tests. Cascade: a contract table the
	// generator does not fill (contract.message_coverage) references one it
	// does, and is emptied with it.
	names := make([]string, len(fixtures.Tables))
	for i, table := range fixtures.Tables {
		names[i] = "contract." + pgx.Identifier{table}.Sanitize() //nolint:misspell // the pgx method's name
	}
	if _, err := tx.Exec(ctx, "truncate table "+strings.Join(names, ", ")+" cascade"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, buf.String()); err != nil {
		t.Fatalf("tiny seed %d does not load: %v", m.Seed, err)
	}
	for _, table := range fixtures.Tables {
		n, err := countRows(ctx, tx, table)
		if err != nil {
			t.Fatal(err)
		}
		if got := n; got != m.Rows[table] {
			t.Errorf("%s: %d rows loaded, the manifest says %d", table, got, m.Rows[table])
		}
	}
}

// TestCIProfileLoadsAndTheReaderSeesIt: the ci set loaded in TestMain, every
// table holds the manifest's count, and the read-only role reads it.
func TestCIProfileLoadsAndTheReaderSeesIt(t *testing.T) {
	req.Covers(t, "TST-014")
	if ciLoadErr != nil {
		t.Fatalf("ci seed %d does not load: %v", ciSeed, ciLoadErr)
	}
	ctx := bounded(t)
	conn, err := pgx.Connect(ctx, db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	read := 0
	for _, table := range fixtures.Tables {
		n, err := countRows(ctx, conn, table)
		if err != nil {
			t.Fatalf("the reader cannot read %s: %v", table, err)
		}
		if n != ciCounts[table] {
			t.Errorf("%s: %d rows, the manifest says %d", table, n, ciCounts[table])
		}
		read += n
	}
	if read == 0 {
		t.Fatal("read zero rows")
	}
	t.Logf("read %d rows in %d tables as %s", read, len(fixtures.Tables), contract.ReaderRole)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("the reads ran past the test bound")
	}
}
