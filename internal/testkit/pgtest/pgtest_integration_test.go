//go:build integration

package pgtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

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

// TestHarnessIsUsable: the owner connects, the reader URL names the reader
// role, and building the stub a second time is refused rather than doubled.
func TestHarnessIsUsable(t *testing.T) {
	ctx := bounded(t)
	conn, err := harness.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if !strings.Contains(harness.ReaderURL, "api_reader:") {
		t.Error("the reader URL does not name the reader role")
	}
	if err := prepare(ctx, harness.OwnerURL, "AAAA", "BBBB"); err == nil || !strings.Contains(err.Error(), "build contract stub") {
		t.Fatalf("a second build of the stub = %v, want a refusal", err)
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
