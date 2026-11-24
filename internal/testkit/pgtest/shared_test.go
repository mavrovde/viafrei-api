package pgtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A URL that does not parse is kept as it is; a role name and a value are
// quoted.
func TestURLHelpersKeepAnUnparsableURL(t *testing.T) {
	db := &DB{}
	const bad = "postgres://%zz"
	if got := db.urlOf(bad, "x"); got != bad {
		t.Errorf("urlOf: %q", got)
	}
	if got := withUser(bad, "u", "p"); got != bad {
		t.Errorf("withUser: %q", got)
	}
	if got := ident(`a"b`); got != `"a""b"` {
		t.Errorf("ident: %s", got)
	}
	if got := quoteLiteral("it's"); got != `'it''s'` {
		t.Errorf("quoteLiteral: %s", got)
	}
}

// The image's address is refused when the container has none or the
// owner's URL does not parse.
func TestImageAddressRefusesWhatItCannotRead(t *testing.T) {
	db := &DB{}
	ctx := context.Background()
	if err := db.imageAddress(ctx, func(context.Context) (string, error) { return "", errors.New("no ip") }, "postgres://h/d"); err == nil {
		t.Error("no container address was accepted")
	}
	if err := db.imageAddress(ctx, func(context.Context) (string, error) { return "10.0.0.1", nil }, "postgres://%zz"); err == nil {
		t.Error("an unparsable owner URL was accepted")
	}
}

// A copy whose server is gone is dropped silently; statistics of a database
// the run cannot reach are an error.
func TestUnreachableServer(t *testing.T) {
	ctx := context.Background()
	gone := "postgres://u:p@" + closedPort(t) + "/d?sslmode=disable&connect_timeout=1"
	(&DB{shared: true, copyOf: gone, copyName: "c"}).Stop()
	if err := analyseStats(ctx, gone); err == nil {
		t.Error("an unreachable database was analysed")
	}
}

// fatalT stops at its first Fatal, as a test does.
type fatalT struct{ failed bool }

func (*fatalT) Helper()        {}
func (*fatalT) Cleanup(func()) {}
func (f *fatalT) Fatal(...any) { f.failed = true; panic(f) }

// Rollback on a database the run cannot reach fails the test.
func TestRollbackOnAnUnreachableDatabaseFails(t *testing.T) {
	ft := &fatalT{}
	func() {
		defer func() { _ = recover() }()
		(&DB{OwnerURL: "postgres://u:p@" + closedPort(t) + "/d?sslmode=disable&connect_timeout=1"}).Rollback(context.Background(), ft)
	}()
	if !ft.failed {
		t.Fatal("the test did not fail")
	}
}

// Stopping no database does nothing; outside the module there is no pin to
// share a database by.
func TestStopOfNoneAndSharedWithoutAPin(t *testing.T) {
	(*DB)(nil).Stop()
	t.Chdir(t.TempDir())
	if _, err := Shared(context.Background()); err == nil {
		t.Fatal("a database was shared without a pin")
	}
}

// errReaperCreated is the error testcontainers returns for the run's reaper
// while another package process has created but not started it.
var errReaperCreated = errors.New(`run postgres: generic container: create container: reaper: from container "04a06ab2": ` +
	`wait for reaper 04a06ab2: unexpected container status "created"`)

// shortReaperRetry shortens the retry's waits to bound and collects what it
// logs.
func shortReaperRetry(t *testing.T, bound time.Duration) *[]string {
	t.Helper()
	tries, delay, b, l := reaperStartTries, reaperStartDelay, reaperStartBound, logf
	t.Cleanup(func() { reaperStartTries, reaperStartDelay, reaperStartBound, logf = tries, delay, b, l })
	var logged []string
	reaperStartDelay, reaperStartBound = time.Millisecond, bound
	logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	return &logged
}

// failing returns a run that fails with errs in turn, then succeeds, and
// counts its calls.
func failing(calls *int, errs ...error) func() (sharedContainer, error) {
	return func() (sharedContainer, error) {
		*calls++
		if *calls <= len(errs) {
			return nil, errs[*calls-1]
		}
		return fakeShared{}, nil
	}
}

type fakeShared struct{}

func (fakeShared) ConnectionString(context.Context, ...string) (string, error) { return "", nil }
func (fakeShared) ContainerIP(context.Context) (string, error)                 { return "", nil }

// TestReaperStartIsRetriedOnlyWhileNotStarted: a reaper that is not started
// yet is waited for, each wait logged, up to the tries; any other error
// fails at once, unlogged.
func TestReaperStartIsRetriedOnlyWhileNotStarted(t *testing.T) {
	ctx := context.Background()
	logged := shortReaperRetry(t, time.Minute)
	calls := 0
	if c, err := retryReaperStart(ctx, failing(&calls, errReaperCreated, errReaperCreated)); err != nil || c == nil || calls != 3 || len(*logged) != 2 {
		t.Fatalf("not retried until started: %v %d %q", err, calls, *logged)
	}
	if !strings.Contains((*logged)[1], "try 2 of 5") || !strings.Contains((*logged)[1], `"created"`) {
		t.Errorf("the retry's line: %q", (*logged)[1])
	}
	other := errors.New(`create container: unexpected container status "created"`)
	calls, *logged = 0, nil
	if _, err := retryReaperStart(ctx, failing(&calls, other)); !errors.Is(err, other) || calls != 1 || len(*logged) != 0 {
		t.Fatalf("another error retried: %v %d %q", err, calls, *logged)
	}
	calls = 0
	errs := []error{errReaperCreated, errReaperCreated, errReaperCreated, errReaperCreated, errReaperCreated, errReaperCreated}
	if _, err := retryReaperStart(ctx, failing(&calls, errs...)); !errors.Is(err, errReaperCreated) || calls != 5 || len(*logged) != 4 ||
		!strings.Contains(err.Error(), "did not start in 5 tries") {
		t.Fatalf("the tries not bounded: %v %d", err, calls)
	}
}

// TestReaperStartStopsAtItsBoundAndItsContext: no wait begins that would
// pass the bound, and a cancelled context ends the wait.
func TestReaperStartStopsAtItsBoundAndItsContext(t *testing.T) {
	logged := shortReaperRetry(t, 0)
	calls := 0
	if _, err := retryReaperStart(context.Background(), failing(&calls, errReaperCreated, errReaperCreated)); calls != 1 || len(*logged) != 0 ||
		err == nil || !strings.Contains(err.Error(), "did not start in 1 tries") {
		t.Fatalf("the bound not kept: %v %d", err, calls)
	}
	reaperStartBound, reaperStartDelay = time.Minute, time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	if _, err := retryReaperStart(ctx, failing(&calls, errReaperCreated)); !errors.Is(err, context.Canceled) || !errors.Is(err, errReaperCreated) || calls != 1 {
		t.Fatalf("the context not kept: %v %d", err, calls)
	}
}
