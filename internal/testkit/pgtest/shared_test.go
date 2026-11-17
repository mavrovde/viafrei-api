package pgtest

import (
	"context"
	"errors"
	"testing"
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
