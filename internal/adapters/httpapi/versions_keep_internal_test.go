package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// A request is answered from the read kept in the background while it is
// younger than the TTL, and reads no versions itself; once the kept read is
// as old as the TTL (the keeper behind or failing) it reads them itself, as
// it did with no keeper.
func TestKeptVersionsAnswerWithoutARequestRead(t *testing.T) {
	now := time.Unix(100, 0)
	fake := &versionsFake{vs: map[string]string{"f": "1"}}
	var logged bytes.Buffer
	kept := NewKeptVersions(fake, time.Second, slog.New(slog.NewTextHandler(&logged, nil)))
	kept.now = func() time.Time { return now }
	v := &versionCache{reader: kept, ttl: time.Second, now: func() time.Time { return now }}
	if _, _, ok := kept.kept(); ok {
		t.Fatal("a keeper that never read holds a read")
	}
	kept.step(context.Background())
	if fake.calls != 1 {
		t.Fatalf("the keeper read %d times", fake.calls)
	}
	now = now.Add(900 * time.Millisecond)
	for range 3 {
		if vs, ok := v.get(context.Background()); !ok || vs.Families["f"] != "1" {
			t.Fatal("the kept read did not answer")
		}
	}
	if fake.calls != 1 {
		t.Fatalf("a request read the versions while the kept read was young: %d reads", fake.calls)
	}
	// A newer kept read replaces the one held; an older one never does.
	fake.vs = map[string]string{"f": "2"}
	kept.step(context.Background())
	if vs, ok := v.get(context.Background()); !ok || vs.Families["f"] != "2" {
		t.Fatal("the newer kept read did not answer")
	}
	kept.mu.Lock()
	kept.at, kept.vs.Families = now.Add(-500*time.Millisecond), map[string]string{"f": "old"}
	kept.mu.Unlock()
	if vs, _ := v.get(context.Background()); vs.Families["f"] != "2" {
		t.Fatal("an older kept read replaced a newer one")
	}
	// The keeper fails: the failure is logged, and once the last read is
	// as old as the TTL a request reads the versions itself.
	fake.err = errors.New("down")
	kept.step(context.Background())
	if !strings.Contains(logged.String(), "background versions read failed") {
		t.Fatalf("a failed background read was not logged: %q", logged.String())
	}
	now = now.Add(time.Second)
	calls := fake.calls
	if _, ok := v.get(context.Background()); ok || fake.calls != calls+1 {
		t.Fatal("a request with no young kept read did not read the versions itself")
	}
	if _, err := kept.FreshnessVersions(context.Background()); err == nil {
		t.Fatal("the request's own read hid the reader's failure")
	}
	// A failure after the keeper's context ended is no failure to report.
	logged.Reset()
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	kept.step(gone)
	if logged.Len() != 0 {
		t.Fatalf("a read cut short by shutdown was logged: %q", logged.String())
	}
}

// Keep reads at once and again, paced by the TTL and the last read's
// duration (next), until its context ends; with no
// TTL it keeps nothing.
func TestKeepReadsUntilItsContextEnds(t *testing.T) {
	fake := &versionsFake{vs: map[string]string{"f": "1"}}
	NewKeptVersions(fake, 0, slog.Default()).Keep(context.Background())
	if fake.calls != 0 {
		t.Fatal("a keeper with no TTL read")
	}
	reads := make(chan struct{}, 8)
	counting := &countingVersions{versionsFake: fake, reads: reads}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { NewKeptVersions(counting, 20*time.Millisecond, slog.Default()).Keep(ctx); close(done) }()
	for range 3 {
		select {
		case <-reads:
		case <-time.After(5 * time.Second):
			t.Fatal("the keeper did not read again")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the keeper did not end with its context")
	}
}

// countingVersions signals each read of the fake it wraps.
type countingVersions struct {
	*versionsFake
	reads chan struct{}
}

func (c *countingVersions) FreshnessVersions(ctx context.Context) (p provenance.Versions, err error) {
	p, err = c.versionsFake.FreshnessVersions(ctx)
	select {
	case c.reads <- struct{}{}:
	default:
	}
	return p, err
}

// The next read starts the TTL less the last read's duration and a tenth of
// the TTL after the last start, so it lands before the last read is as old
// as the TTL; and never sooner than a quarter of the TTL after the read ended.
func TestTheKeeperPacesItsReadsByTheTTL(t *testing.T) {
	k := NewKeptVersions(&versionsFake{}, time.Second, slog.Default())
	for took, want := range map[time.Duration]time.Duration{
		0:                       900 * time.Millisecond,
		100 * time.Millisecond:  800 * time.Millisecond,
		700 * time.Millisecond:  950 * time.Millisecond,
		1500 * time.Millisecond: 1750 * time.Millisecond,
	} {
		if got := k.next(took); got != want {
			t.Errorf("a read of %v: next after %v, want %v", took, got, want)
		}
	}
}
