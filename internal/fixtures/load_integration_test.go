//go:build integration

package fixtures_test

import (
	"bytes"
	"context"
	"errors"
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
	// base is each table's count in the migrated database before any seed:
	// the pinned image's migrations ship the register's own rows (its
	// publishers, licences, places and rules) beside the ones a seed adds.
	base    map[string]int
	baseErr error
)

// TestMain takes a copy of the ci profile and one of the tiny profile from
// the run's shared container (pgtest.Seeded): each set is seeded once per
// run, through the pinned image's test seed, and both are setup, outside
// every test body's bound.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	db, ciCounts, ciLoadErr = loadCommitted(ctx, "ci", ciSeed)
	tinyDB, tinyRows, tinyErr = loadCommitted(ctx, "tiny", 1)
	base, baseErr = migratedCounts(ctx)
	cancel()
	code := m.Run()
	db.Stop()
	tinyDB.Stop()
	os.Exit(code)
}

// migratedCounts counts each table of a copy of the migrated, unseeded
// database, as the reader.
func migratedCounts(ctx context.Context) (map[string]int, error) {
	empty, err := pgtest.Shared(ctx)
	if err != nil {
		return nil, err
	}
	defer empty.StopWith(context.WithoutCancel(ctx))
	conn, err := pgx.Connect(ctx, empty.ReaderURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	out := map[string]int{}
	for _, table := range fixtures.Tables {
		if out[table], err = countRows(ctx, conn, table); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// checkCounts compares each table's count against the migrated database's
// rows and the manifest's. A row the seed gives that the register already
// ships (a licence, an admin region, a fuel product) is the same row, so a
// table holds at least the larger of the two and at most their sum. The
// platform's place view also lists each seeded admin region (and each
// station) as a place, and its alias view each place by its own name.
func checkCounts(t *testing.T, got, manifest map[string]int) {
	t.Helper()
	for _, table := range fixtures.Tables {
		n, b, m := got[table], base[table], manifest[table]
		switch table {
		case "place":
			if want := m + got["admin_region"]; n < want {
				t.Errorf("place: %d rows, fewer than the manifest's %d and the %d admin regions", n, m, got["admin_region"])
			}
		case "place_alias":
			if want := m + got["place"]; n != want {
				t.Errorf("place_alias: %d rows, the manifest's %d and one per place (%d)", n, m, got["place"])
			}
		default:
			if n < max(b, m) || n > b+m {
				t.Errorf("%s: %d rows, the migrated database's %d and the manifest's %d", table, n, b, m)
			}
		}
	}
}

func loadCommitted(ctx context.Context, profile string, seed uint64) (*pgtest.DB, map[string]int, error) {
	var buf bytes.Buffer
	m, err := fixtures.Generate(&buf, profile, seed, dbcontract.ContractVersion)
	if err != nil {
		return nil, nil, err
	}
	into, err := pgtest.Seeded(ctx, buf.String())
	return into, m.Rows, err
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
// table holds the migrated database's rows and the manifest's (checkCounts).
func TestTinyLoadsIntoTheContract(t *testing.T) {
	req.Covers(t, "TST-014")
	if tinyErr != nil {
		t.Fatalf("tiny seed 1 does not load: %v", tinyErr)
	}
	if baseErr != nil {
		t.Fatalf("the migrated database cannot be counted: %v", baseErr)
	}
	ctx := bounded(t)
	conn, err := pgx.Connect(ctx, tinyDB.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	got := map[string]int{}
	for _, table := range fixtures.Tables {
		if got[table], err = countRows(ctx, conn, table); err != nil {
			t.Fatal(err)
		}
	}
	checkCounts(t, got, tinyRows)
}

// TestCIProfileLoadsAndTheReaderSeesIt: the ci set loaded in TestMain, every
// table holds the migrated database's rows and the manifest's (checkCounts),
// and the
// read-only role reads them.
func TestCIProfileLoadsAndTheReaderSeesIt(t *testing.T) {
	req.Covers(t, "TST-014")
	if ciLoadErr != nil {
		t.Fatalf("ci seed %d does not load: %v", ciSeed, ciLoadErr)
	}
	if baseErr != nil {
		t.Fatalf("the migrated database cannot be counted: %v", baseErr)
	}
	ctx := bounded(t)
	conn, err := pgx.Connect(ctx, db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	read := 0
	got := map[string]int{}
	for _, table := range fixtures.Tables {
		n, err := countRows(ctx, conn, table)
		if err != nil {
			t.Fatalf("the reader cannot read %s: %v", table, err)
		}
		got[table] = n
		read += n
	}
	checkCounts(t, got, ciCounts)
	if read == 0 {
		t.Fatal("read zero rows")
	}
	t.Logf("read %d rows in %d tables as %s", read, len(fixtures.Tables), dbcontract.ReaderRole)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("the reads ran past the test bound")
	}
}
