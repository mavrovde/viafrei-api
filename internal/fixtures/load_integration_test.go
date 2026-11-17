//go:build integration

package fixtures_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/dbcontract"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// testBound is the ceiling on every test body (TST-017).
const testBound = 2 * time.Second

var (
	db        *pgtest.DB
	tinyDB    *pgtest.DB
	ciSeed    uint64 = 7
	ciLoadErr error
	ciCounts  map[string]int
	tinyErr   error
	tinyRows  map[string]int
)

// TestMain starts two databases and seeds the ci profile into one and the
// tiny profile into the other: a database is seeded once, through the pinned
// image's test seed, and both are setup, outside every test body's bound.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	var err error
	db, err = pgtest.Start(ctx)
	if err == nil {
		tinyDB, err = pgtest.Start(ctx)
	}
	if err != nil {
		cancel()
		if db != nil {
			db.Stop()
		}
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", err)
		os.Exit(1)
	}
	ciCounts, ciLoadErr = loadCommitted(ctx, db, "ci", ciSeed)
	tinyRows, tinyErr = loadCommitted(ctx, tinyDB, "tiny", 1)
	cancel()
	code := m.Run()
	db.Stop()
	tinyDB.Stop()
	os.Exit(code)
}

func loadCommitted(ctx context.Context, into *pgtest.DB, profile string, seed uint64) (map[string]int, error) {
	var buf bytes.Buffer
	m, err := fixtures.Generate(&buf, profile, seed, dbcontract.ContractVersion)
	if err != nil {
		return nil, err
	}
	return m.Rows, into.Seed(ctx, buf.String())
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

// TestTinyLoadsIntoTheContract: the tiny set seeded in TestMain, every
// table holds the manifest's count.
func TestTinyLoadsIntoTheContract(t *testing.T) {
	req.Covers(t, "TST-014")
	if tinyErr != nil {
		t.Fatalf("tiny seed 1 does not load: %v", tinyErr)
	}
	ctx := bounded(t)
	conn, err := pgx.Connect(ctx, tinyDB.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for _, table := range fixtures.Tables {
		n, err := countRows(ctx, conn, table)
		if err != nil {
			t.Fatal(err)
		}
		if n != tinyRows[table] {
			t.Errorf("%s: %d rows loaded, the manifest says %d", table, n, tinyRows[table])
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
	t.Logf("read %d rows in %d tables as %s", read, len(fixtures.Tables), dbcontract.ReaderRole)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("the reads ran past the test bound")
	}
}
