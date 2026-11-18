package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// fakeRegister is a register read that counts its calls and answers fam,
// or fails while down.
type fakeRegister struct {
	mu    sync.Mutex
	fam   string
	down  bool
	reads atomic.Int32
}

func (f *fakeRegister) read(context.Context) ([]access.Terms, error) {
	f.reads.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errors.New("storage down")
	}
	return []access.Terms{{Family: f.fam}}, nil
}

func (f *fakeRegister) set(fam string, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fam, f.down = fam, down
}

func TestRegisterSnapshot(t *testing.T) {
	var clockMu sync.Mutex
	clock := time.Unix(0, 0)
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	f := &fakeRegister{fam: "a"}
	r := &registerSnapshot{ttl: time.Second, staleFor: 3 * time.Second}
	ctx := context.Background()
	family := func() string {
		t.Helper()
		got, err := r.get(ctx, now, f.read)
		if err != nil || len(got) != 1 {
			t.Fatalf("get %v %v", got, err)
		}
		return got[0].Family
	}
	for range 3 {
		if family() != "a" {
			t.Fatal("first read")
		}
	}
	if n := f.reads.Load(); n != 1 {
		t.Fatalf("%d reads within the lifetime, want 1", n)
	}

	// Past ttl the old read still answers; a refresh replaces it.
	f.set("b", false)
	advance(time.Second)
	if family() != "a" {
		t.Fatal("the snapshot within staleFor was not answered")
	}
	r.refresh(ctx, now, f.read)
	if family() != "b" || f.reads.Load() != 2 {
		t.Fatalf("the refresh did not replace the snapshot: %d reads", f.reads.Load())
	}

	// A failed refresh keeps the old snapshot only until staleFor.
	f.set("c", true)
	advance(2 * time.Second)
	r.refresh(ctx, now, f.read)
	if family() != "b" {
		t.Fatal("a failed refresh dropped the snapshot within staleFor")
	}
	advance(time.Second)
	if _, err := r.get(ctx, now, f.read); err == nil {
		t.Fatal("a snapshot past staleFor was answered while storage is down")
	}
	f.set("c", false)
	if family() != "c" {
		t.Fatal("a failed read was kept")
	}

	ended, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.get(ended, now, f.read); err == nil {
		t.Fatal("an ended context was answered")
	}
}

func TestRegisterSnapshotWaiterHonoursItsContext(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	read := func(context.Context) ([]access.Terms, error) {
		close(started)
		<-release
		return []access.Terms{{Family: "x"}}, nil
	}
	r := &registerSnapshot{ttl: time.Second, staleFor: time.Second}
	done := make(chan struct{})
	go func() { _, _ = r.get(context.Background(), time.Now, read); close(done) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.get(ctx, time.Now, read); !errors.Is(err, ErrDeadlineExhausted) {
		t.Fatalf("a waiter past its deadline got %v", err)
	}
	close(release)
	<-done
}
