//go:build integration

package fakelive

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
)

// bound is the ceiling on each wait of these tests.
const bound = 10 * time.Second

// TestMain joins the run's shared database before any test: its migration
// under the pinned image takes tens of seconds, which the first test to ask
// for a copy carried (TST-012); a test's copy is then a template copy.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	db, err := pgtest.Shared(ctx)
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "the shared database:", err)
		os.Exit(1)
	}
	db.StopWith(ctx)
	cancel()
	os.Exit(m.Run())
}

// fresh is a migrated, empty database of the test's own, and its owner.
func fresh(t *testing.T) (*pgtest.DB, *pgx.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	defer cancel()
	db, err := pgtest.Shared(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Stop)
	owner, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	return db, owner
}

func exec(t *testing.T, c *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := c.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// ask records a fuel refresh of the Gemeinde ags, as the API records one: with
// its caller, which the database requires (requested_by: site, mcp or dev).
func ask(t *testing.T, owner *pgx.Conn, ags string) {
	t.Helper()
	exec(t, owner, `insert into live.refresh_requests (source, subject, subject_key, requested_by) values ('fuel-prices-de', $1::jsonb, $2, 'site')`,
		`{"at":"ags:`+ags+`"}`, "ags:"+ags)
}

// role is a login role of the test's own, dropped at its end, and the URL
// of db as that role.
func role(t *testing.T, db *pgtest.DB, owner *pgx.Conn, member bool) (string, string) {
	t.Helper()
	name := "fakelive_" + strings.ToLower(rand.Text())[:10]
	pw := rand.Text()
	exec(t, owner, "create role "+name+" login password '"+pw+"'")
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = owner.Exec(ctx, "select pg_terminate_backend(pid) from pg_stat_activity where usename = $1", name)
		_, _ = owner.Exec(ctx, "drop owned by "+name)
		_, _ = owner.Exec(ctx, "drop role "+name)
	})
	if member {
		exec(t, owner, "grant viafrei_live_ingestor to "+name)
	}
	u, err := url.Parse(db.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(name, pw)
	return name, u.String()
}

// until waits for cond, at most bound.
func until(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(bound); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the condition never held")
		}
	}
}

// stopWith waits for the fake's first fault, then stops it: the fault is
// the stop's error.
func stopWith(t *testing.T, f *Fake, says string) {
	t.Helper()
	until(t, func() bool {
		f.errMu.Lock()
		defer f.errMu.Unlock()
		return f.err != nil
	})
	if err := f.Stop(); err == nil || !strings.Contains(err.Error(), says) {
		t.Fatalf("stop: %v, want %q", err, says)
	}
}

func start(t *testing.T, u string, script string) *Fake {
	t.Helper()
	sc, err := ParseScript([]byte(script), registry(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	f, err := Start(ctx, u, sc)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A partition named without its schema, and ticks that fail, are the
// fake's error at its stop.
func TestATickFaultIsTheStopsError(t *testing.T) {
	for says, script := range map[string]string{
		"partition of nodot": `{"sources":{"fuel-prices-de":{"ticks":"select 1","partitions":["nodot"],"default":{"state":"refreshed"}}}}`,
		"ticks of":           `{"sources":{"fuel-prices-de":{"ticks":"select no_such_function()","default":{"state":"refreshed"}}}}`,
	} {
		t.Run(says, func(t *testing.T) {
			db, owner := fresh(t)
			f := start(t, db.OwnerURL, script)
			ask(t, owner, "09162000")
			until(t, func() bool { return f.Claims() == 1 })
			stopWith(t, f, says)
		})
	}
}

// A role that may not act as the live refresher claims nothing: the claim's
// fault is the stop's error.
func TestARoleThatIsNoRefresherClaimsNothing(t *testing.T) {
	db, owner := fresh(t)
	ask(t, owner, "09162000")
	_, u := role(t, db, owner, false)
	f := start(t, u, `{"sources":{}}`)
	stopWith(t, f, "viafrei_live_ingestor")
}

// A refresher that can no longer log in fails its next drain; one that loses
// the role while it waits fails the settle.
func TestARefresherThatLosesItsLoginOrRoleFails(t *testing.T) {
	t.Run("the drain", func(t *testing.T) {
		db, owner := fresh(t)
		name, u := role(t, db, owner, true)
		f := start(t, u, `{"sources":{}}`)
		exec(t, owner, "alter role "+name+" nologin")
		ask(t, owner, "09162000")
		stopWith(t, f, name)
	})
	for change, says := range map[string]string{"nologin": "", "noinherit": "viafrei_live_ingestor"} {
		t.Run("the settle, "+change, func(t *testing.T) {
			db, owner := fresh(t)
			name, u := role(t, db, owner, true)
			// Asked before the start, the request is claimed by the first
			// drain, and no notification starts another.
			ask(t, owner, "09162000")
			f := start(t, u, `{"sources":{"fuel-prices-de":{"default":{"state":"refreshed","after_ms":500}}}}`)
			until(t, func() bool { return f.Claims() == 1 })
			if change == "nologin" {
				exec(t, owner, "alter role "+name+" nologin")
			} else {
				exec(t, owner, "revoke viafrei_live_ingestor from "+name)
			}
			stopWith(t, f, says)
		})
	}
}

// A request an asker is being linked to is claimed all the same: the link's
// key check (live.refresh_askers) holds a key-share lock on the request, and
// a claim that skipped the row it locks waited on with nothing left to wake
// it (CI: twelve askers of one subject, no claim in 5 s).
func TestARequestBeingLinkedIsClaimed(t *testing.T) {
	db, owner := fresh(t)
	ask(t, owner, "09162000")
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	linking, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = linking.Close(context.Background()) }()
	tx, err := linking.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `select 1 from live.refresh_requests where subject_key = 'ags:09162000' for key share`); err != nil {
		t.Fatal(err)
	}
	f := start(t, db.OwnerURL, `{"sources":{"fuel-prices-de":{"default":{"state":"refreshed"}}}}`)
	until(t, func() bool { return f.Claims() == 1 })
	if err := f.Stop(); err != nil {
		t.Fatal(err)
	}
}
