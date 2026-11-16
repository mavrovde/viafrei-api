//go:build integration

package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/dbcontract"
)

// closedPortAddr is an address nothing listens on.
func closedPortAddr() string { return "127.0.0.1:1" }

var harness *DB

func TestMain(m *testing.M) {
	// Starting the container is setup, outside every test body's bound.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	var err error
	harness, err = Start(ctx)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", err)
		os.Exit(1)
	}
	code := m.Run()
	harness.Stop()
	os.Exit(code)
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestHarnessIsUsable: the owner connects, each role's URL names its role and
// connects, and the reader reads the contract version this build expects.
func TestHarnessIsUsable(t *testing.T) {
	ctx := bounded(t)
	conn, err := harness.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if !strings.Contains(harness.ReaderURL, "api_reader:") || !strings.Contains(harness.AccessURL, "api_access:") {
		t.Error("a role URL does not name its role")
	}
	reader, err := pgx.Connect(ctx, harness.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(context.WithoutCancel(ctx)) }()
	var v string
	if err := reader.QueryRow(ctx, "select contract_version from contract.contract_info").Scan(&v); err != nil || v != dbcontract.ContractVersion {
		t.Fatalf("contract_info = %q %v, want %s", v, err, dbcontract.ContractVersion)
	}
	access, err := pgx.Connect(ctx, harness.AccessURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = access.Close(ctx)
	if err := passwords(ctx, "postgres://owner:x@"+closedPortAddr()+"/contract?sslmode=disable", "A", "B"); err == nil || !strings.Contains(err.Error(), "connect as owner") {
		t.Fatalf("passwords on no database = %v", err)
	}
	// api_access may not set api_reader's password: the step reports which
	// role failed. Its own password is set to what it already is.
	au, _ := url.Parse(harness.AccessURL)
	own, _ := au.User.Password()
	if err := passwords(ctx, harness.AccessURL, "A", own); err == nil || !strings.Contains(err.Error(), "set the password of api_reader") {
		t.Fatalf("passwords as api_access = %v", err)
	}
}

func TestStartWithAnEndedContextFails(t *testing.T) {
	ctx, cancel := context.WithCancel(bounded(t))
	cancel()
	if db, err := Start(ctx); err == nil {
		db.Stop()
		t.Fatal("a container started on an ended context")
	}
}

// TestThePinnedReleaseBuiltTheSchema: the platform's schema is the pinned
// image's (its schemas and roles exist), and the settings it gives each role
// in this database are held, so a role limit is tested as prod runs it.
func TestThePinnedReleaseBuiltTheSchema(t *testing.T) {
	ctx := bounded(t)
	conn, err := harness.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var schemas, roles, settings int
	err = conn.QueryRow(ctx, `select
		(select count(*) from pg_namespace where nspname in ('public', 'control', 'domains', 'contract', 'access')),
		(select count(*) from pg_roles where rolname in ('viafrei_mcp', 'viafrei_digest', 'api_reader', 'api_access')),
		(select count(*) from pg_db_role_setting s join pg_database d on d.oid = s.setdatabase where d.datname = current_database())`).
		Scan(&schemas, &roles, &settings)
	if err != nil {
		t.Fatal(err)
	}
	if schemas != 5 || roles != 4 || settings == 0 {
		t.Fatalf("schemas %d of 5, roles %d of 4, role settings in this database %d", schemas, roles, settings)
	}
	if harness.Pin.Head < 1 {
		t.Fatalf("the harness names no pin: %+v", harness.Pin)
	}
}

// TestContractColumnsMatchThePinnedImage: test/contract/columns.tsv, the
// column list the scope lint reads, is the catalogue of the schemas the
// service reads as the pinned image built them, column for column in order.
// A new pin that changes a column fails here until the list is regenerated.
func TestContractColumnsMatchThePinnedImage(t *testing.T) {
	ctx := bounded(t)
	conn, err := harness.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	rows, _ := conn.Query(ctx, `select n.nspname || '.' || c.relname || chr(9) || a.attname || chr(9) || format_type(a.atttypid, a.atttypmod)
from pg_attribute a join pg_class c on c.oid = a.attrelid join pg_namespace n on n.oid = c.relnamespace
where n.nspname in ('contract', 'access') and c.relkind in ('r', 'v', 'p') and a.attnum > 0 and not a.attisdropped
order by n.nspname || '.' || c.relname collate "C", a.attnum`)
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	src, err := os.ReadFile(filepath.Join(wd, "..", "..", "..", "test", "contract", "columns.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, l := range strings.Split(strings.TrimSpace(string(src)), "\n") {
		if !strings.HasPrefix(l, "#") {
			want = append(want, l)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("test/contract/columns.tsv differs from the pinned image's catalogue; it is, as the file's format:\n%s", strings.Join(got, "\n"))
	}
}

// TestStartWithoutAPinFails: outside a module there is no database.pin, and
// no container is started.
func TestStartWithoutAPinFails(t *testing.T) {
	t.Chdir(t.TempDir())
	if db, err := Start(bounded(t)); err == nil || !strings.Contains(err.Error(), "database.pin") {
		if db != nil {
			db.Stop()
		}
		t.Fatalf("Start without a pin = %v", err)
	}
}

// TestRunImageReportsAnImageThatCannotRun: a reference no runtime can pull
// is an error of the runtime, never an exit status.
func TestRunImageReportsAnImageThatCannotRun(t *testing.T) {
	if _, _, err := RunImage(bounded(t), "invalid reference::", nil, "version"); err == nil {
		t.Fatal("an invalid reference ran")
	}
}
