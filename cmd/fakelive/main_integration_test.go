//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
)

// TestMain joins the run's shared database before any test: its migration
// under the pinned image takes tens of seconds, and a container of the
// test's own took as long to start and migrate (TST-012); the test's
// database is then a template copy.
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

// On a migrated database the fake runs until it is interrupted, says it
// settles, and stops cleanly: 0.
func TestRunSettlesUntilInterrupted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	defer cancel()
	db, err := pgtest.Shared(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Stop)
	args := []string{"-db", db.OwnerURL,
		"-registry", filepath.Join("..", "..", "test", "live", "registry.json"),
		"-script", filepath.Join("..", "..", "test", "live", "fakelive.json")}
	t.Run("settles until interrupted", func(t *testing.T) {
		rctx, stop := context.WithTimeout(ctx, 500*time.Millisecond)
		defer stop()
		var stderr bytes.Buffer
		if got := run(rctx, args, &stderr); got != 0 || !strings.Contains(stderr.String(), "settling refreshes") {
			t.Fatalf("%d %q", got, stderr.String())
		}
	})

	// A fake whose listener is lost while it runs reports it at its stop: 1.
	t.Run("a lost listener", func(t *testing.T) {
		lctx, lstop := context.WithCancel(ctx)
		defer lstop()
		var stderr bytes.Buffer
		code := make(chan int, 1)
		go func() { code <- run(lctx, args, &stderr) }()
		conn, err := db.Owner(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(ctx) }()
		// The shared container holds other packages' databases, whose
		// fakes listen too: only this copy's listener is ended.
		for ended := 0; ended == 0; time.Sleep(20 * time.Millisecond) {
			if err := conn.QueryRow(ctx, `select count(pg_terminate_backend(pid)) from pg_stat_activity
				where query like 'listen%live_refresh_requested%' and datname = current_database() and pid <> pg_backend_pid()`).Scan(&ended); err != nil {
				t.Fatal(err)
			}
		}
		// Interrupted at once: the fake reads the loss when its wait is cut.
		lstop()
		if got := <-code; got != 1 {
			t.Fatalf("lost listener: %d %q", got, stderr.String())
		}
	})
}
