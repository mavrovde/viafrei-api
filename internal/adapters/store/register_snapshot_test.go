package store

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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
	if err := r.refresh(ctx, now, f.read); err != nil {
		t.Fatal(err)
	}
	if family() != "b" || f.reads.Load() != 2 {
		t.Fatalf("the refresh did not replace the snapshot: %d reads", f.reads.Load())
	}

	// A failed refresh keeps the old snapshot only until staleFor.
	f.set("c", true)
	advance(2 * time.Second)
	if err := r.refresh(ctx, now, f.read); err == nil {
		t.Fatal("a failed refresh answered no error")
	}
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

// TestRegisterSnapshotStore answers Store.Register from the snapshot when it
// is on, and KeepRegister returns at once when it is off.
func TestRegisterSnapshotStore(t *testing.T) {
	off := &Store{nowFn: time.Now}
	done := make(chan struct{})
	go func() { off.KeepRegister(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("KeepRegister without a snapshot did not return")
	}

	s := (&Store{nowFn: time.Now}).WithRegisterSnapshot(time.Second, 2*time.Second)
	s.reg.terms, s.reg.at = []access.Terms{{Family: "kept"}}, time.Now()
	got, err := s.Register(context.Background())
	if err != nil || len(got) != 1 || got[0].Family != "kept" {
		t.Fatalf("Register with the snapshot on: %v %v", got, err)
	}
	got[0].Family = "changed"
	if s.reg.terms[0].Family != "kept" {
		t.Fatal("the answer shares the snapshot's slice")
	}
}

// TestRegisterSnapshotWaiters: a caller arriving during another's read takes
// that read's answer, as a copy; when that read fails it reads itself; a
// refresh during a read does not read again.
func TestRegisterSnapshotWaiters(t *testing.T) {
	for _, fail := range []bool{false, true} {
		release := make(chan struct{})
		started := make(chan struct{})
		var leads atomic.Int32
		lead := func(context.Context) ([]access.Terms, error) {
			leads.Add(1)
			close(started)
			<-release
			if fail {
				return nil, errors.New("the leader's deadline")
			}
			return []access.Terms{{Family: "shared"}}, nil
		}
		var own atomic.Int32
		self := func(context.Context) ([]access.Terms, error) {
			own.Add(1)
			return []access.Terms{{Family: "own"}}, nil
		}
		r := &registerSnapshot{ttl: time.Second, staleFor: time.Second}
		var leader []access.Terms
		done := make(chan struct{})
		go func() { leader, _ = r.get(context.Background(), time.Now, lead); close(done) }()
		<-started
		if err := r.refresh(context.Background(), time.Now, self); err != nil {
			t.Fatalf("a refresh during a read: %v", err)
		}
		waited := make(chan []access.Terms)
		go func() {
			got, err := r.get(context.Background(), time.Now, self)
			if err != nil {
				t.Errorf("waiter: %v", err)
			}
			waited <- got
		}()
		// The waiter is parked on the flight before the leader settles.
		for {
			r.mu.Lock()
			parked := r.flight != nil
			r.mu.Unlock()
			if parked {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		close(release)
		<-done
		got := <-waited
		want := "shared"
		if fail {
			want = "own"
		}
		if len(got) != 1 || got[0].Family != want {
			t.Fatalf("fail=%v: the waiter got %v, want %s", fail, got, want)
		}
		if n := own.Load(); fail != (n == 1) || n > 1 {
			t.Fatalf("fail=%v: %d own reads (the refresh read during the flight?)", fail, n)
		}
		if leads.Load() != 1 {
			t.Fatalf("fail=%v: %d leader reads", fail, leads.Load())
		}
		if !fail {
			got[0].Family = "changed"
			if leader[0].Family != "shared" || r.terms[0].Family != "shared" {
				t.Fatal("the waiter's answer shares the leader's or the snapshot's slice")
			}
		}
	}
}

// TestRegisterSnapshotKeep reads at once and every ttl, logs a failed read
// at warn, and stops with its context.
func TestRegisterSnapshotKeep(t *testing.T) {
	f := &fakeRegister{fam: "a", down: true}
	var buf syncBuffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	r := &registerSnapshot{ttl: 5 * time.Millisecond, staleFor: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.keep(ctx, time.Now, f.read, log); close(done) }()
	for f.reads.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	f.set("b", false)
	for {
		r.mu.Lock()
		ok := len(r.terms) == 1 && r.terms[0].Family == "b"
		r.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "storage down") {
		t.Fatalf("a failed read was not logged at warn: %q", buf.String())
	}
}

// syncBuffer is a bytes.Buffer safe for the loop's writes and the test's read.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
