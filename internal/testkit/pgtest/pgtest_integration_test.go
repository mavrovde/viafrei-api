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
