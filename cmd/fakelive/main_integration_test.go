//go:build integration

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
)

// On a migrated database the fake runs until it is interrupted, says it
// settles, and stops cleanly: 0.
func TestRunSettlesUntilInterrupted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	defer cancel()
	db, err := pgtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Stop)
	rctx, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	var stderr bytes.Buffer
	args := []string{"-db", db.OwnerURL,
		"-registry", filepath.Join("..", "..", "test", "live", "registry.json"),
		"-script", filepath.Join("..", "..", "test", "live", "fakelive.json")}
	if got := run(rctx, args, &stderr); got != 0 || !strings.Contains(stderr.String(), "settling refreshes") {
		t.Fatalf("%d %q", got, stderr.String())
	}

	// A fake whose listener is lost while it runs reports it at its stop: 1.
	lctx, lstop := context.WithCancel(ctx)
	defer lstop()
	stderr.Reset()
	code := make(chan int, 1)
	go func() { code <- run(lctx, args, &stderr) }()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for ended := 0; ended == 0; time.Sleep(20 * time.Millisecond) {
		if err := conn.QueryRow(ctx, `select count(pg_terminate_backend(pid)) from pg_stat_activity
			where query like 'listen%live_refresh_requested%' and pid <> pg_backend_pid()`).Scan(&ended); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond) // the loop hears the loss before it is interrupted
	lstop()
	if got := <-code; got != 1 {
		t.Fatalf("lost listener: %d %q", got, stderr.String())
	}
}
