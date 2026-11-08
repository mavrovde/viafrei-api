package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var (
	pepper   = bytes.Repeat([]byte("p"), 32)
	prefixes = access.Prefixes{Live: "vf_live_", Test: "vf_test_"}
	t0       = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeStore is an access store in memory.
type fakeStore struct {
	mu       sync.Mutex
	keys     map[string]access.Record
	lookups  atomic.Int64
	gate     chan struct{} // when set, Lookup waits on it
	fail     error
	remacErr error
	remacs   []bool
	changed  []string
	since    []time.Time
}

func (f *fakeStore) Lookup(_ context.Context, id string) (access.Record, error) {
	f.lookups.Add(1)
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return access.Record{}, f.fail
	}
	r, ok := f.keys[id]
	if !ok {
		return access.Record{}, auth.ErrUnknown
	}
	return r, nil
}

func (f *fakeStore) Remac(_ context.Context, id string, previous bool, mac []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remacs = append(f.remacs, previous)
	if f.remacErr != nil {
		return f.remacErr
	}
	r := f.keys[id]
	if previous {
		r.PreviousMAC = mac
	} else {
		r.MAC = mac
	}
	f.keys[id] = r
	return nil
}

func (f *fakeStore) ChangedSince(_ context.Context, since time.Time) ([]string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since = append(f.since, since)
	if f.fail != nil {
		return nil, time.Time{}, f.fail
	}
	return f.changed, since.Add(time.Second), nil
}

func settings() auth.Settings {
	return auth.Settings{
		Environment: access.Live, Prefixes: prefixes, Peppers: access.Peppers{Current: pepper},
		KeyCacheTTL: time.Minute, NegativeCacheTTL: 10 * time.Second, KeyCacheSize: 100,
		ExpiryWarning: 14 * 24 * time.Hour, FailureLimit: 3, FailureWindow: time.Minute, FailureTrackSize: 100,
		RotationOverlap: 7 * 24 * time.Hour, DefaultExpiry: 365 * 24 * time.Hour,
		MinExpiry: 24 * time.Hour, MaxExpiry: 1095 * 24 * time.Hour, AuditRetention: 395 * 24 * time.Hour,
	}
}

// issue puts a fresh key into the store and returns it.
func issue(t *testing.T, f *fakeStore, expires time.Time) access.Key {
	t.Helper()
	k, err := access.Generate(rand.Reader, access.Live, prefixes)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keys == nil {
		f.keys = map[string]access.Record{}
	}
	f.keys[k.ID] = access.Record{ID: k.ID, Environment: access.Live, MAC: access.MAC(pepper, k.ID, k.Secret), ExpiresAt: expires, Plan: access.Plan{Name: "p"}}
	return k
}

func setup(t *testing.T) (*auth.Authenticator, *fakeStore, *clock, access.Key) {
	t.Helper()
	c := &clock{t: t0}
	f := &fakeStore{}
	k := issue(t, f, t0.Add(30*24*time.Hour))
	return auth.New(settings(), f, c.now), f, c, k
}

func TestAuthenticateRefusals(t *testing.T) {
	req.Covers(t, "SEC-001", "SEC-003", "SEC-004")
	a, f, _, k := setup(t)
	ctx := context.Background()
	if p, err := a.Authenticate(ctx, k.Full, "c"); err != nil || p.Key.ID != k.ID || p.Expiring {
		t.Fatalf("valid key: %v %+v", err, p)
	}
	test, _ := access.NewSecret(rand.Reader, access.Test, prefixes, k.ID)
	other, _ := access.Generate(rand.Reader, access.Live, prefixes)
	wrong, _ := access.NewSecret(rand.Reader, access.Live, prefixes, k.ID)
	// The last checksum character, always changed: appending a fixed one
	// left the key valid whenever the checksum already ended in it (1 in 62).
	altered := k.Full[:len(k.Full)-1] + "x"
	if strings.HasSuffix(k.Full, "x") {
		altered = k.Full[:len(k.Full)-1] + "y"
	}
	for name, c := range map[string]struct {
		key  string
		want error
	}{
		"missing":          {"", access.ErrMissing},
		"malformed":        {"vf_live_nonsense", access.ErrInvalid},
		"other env":        {test.Full, access.ErrInvalid},
		"unknown id":       {other.Full, access.ErrInvalid},
		"wrong secret":     {wrong.Full, access.ErrInvalid},
		"checksum altered": {altered, access.ErrInvalid},
	} {
		if _, err := a.Authenticate(ctx, c.key, name); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	f.fail = errors.New("down")
	stale, _ := access.Generate(rand.Reader, access.Live, prefixes)
	if _, err := a.Authenticate(ctx, stale.Full, "c"); !errors.Is(err, auth.ErrUnavailable) {
		t.Errorf("store down: %v", err)
	}
}

func TestExpiredAndRevoked(t *testing.T) {
	req.Covers(t, "SEC-042")
	a, f, c, k := setup(t)
	ctx := context.Background()
	c.add(31 * 24 * time.Hour)
	if _, err := a.Authenticate(ctx, k.Full, "c"); !errors.Is(err, access.ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	k2 := issue(t, f, c.now().Add(time.Hour))
	r := f.keys[k2.ID]
	at := c.now()
	r.RevokedAt = &at
	f.keys[k2.ID] = r
	if _, err := a.Authenticate(ctx, k2.Full, "d"); !errors.Is(err, access.ErrRevoked) {
		t.Errorf("revoked: %v", err)
	}
}

func TestExpiryWarning(t *testing.T) {
	req.Covers(t, "SEC-043")
	a, f, _, _ := setup(t)
	soon := issue(t, f, t0.Add(13*24*time.Hour))
	if p, err := a.Authenticate(context.Background(), soon.Full, "c"); err != nil || !p.Expiring {
		t.Fatalf("no warning 13 days before expiry: %v %+v", err, p)
	}
	// The warning starts exactly expiry_warning_days before expiry.
	edge := issue(t, f, t0.Add(14*24*time.Hour))
	if p, err := a.Authenticate(context.Background(), edge.Full, "c"); err != nil || !p.Expiring {
		t.Fatalf("no warning exactly 14 days before expiry: %v %+v", err, p)
	}
	later := issue(t, f, t0.Add(14*24*time.Hour+time.Second))
	if p, err := a.Authenticate(context.Background(), later.Full, "c"); err != nil || p.Expiring {
		t.Fatalf("a warning before the window: %v %+v", err, p)
	}
	s := settings()
	s.ExpiryWarning = 0
	off := auth.New(s, f, func() time.Time { return t0 })
	if p, _ := off.Authenticate(context.Background(), soon.Full, "c"); p.Expiring {
		t.Fatal("a warning with expiry_warning_days 0")
	}
}

func TestKeyCache(t *testing.T) {
	req.Covers(t, "SEC-022", "SEC-050")
	a, f, c, k := setup(t)
	ctx := context.Background()
	for range 3 {
		if _, err := a.Authenticate(ctx, k.Full, "c"); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.lookups.Load(); n != 1 {
		t.Fatalf("%d store reads for one cached key", n)
	}
	c.add(time.Minute)
	_, _ = a.Authenticate(ctx, k.Full, "c")
	if n := f.lookups.Load(); n != 2 {
		t.Fatalf("the cache outlived auth.key_cache_ttl: %d reads", n)
	}
	// Negative cache: an unknown id is read once per negative_cache_ttl.
	unknown, _ := access.Generate(rand.Reader, access.Live, prefixes)
	_, _ = a.Authenticate(ctx, unknown.Full, "x")
	_, _ = a.Authenticate(ctx, unknown.Full, "y")
	if n := f.lookups.Load(); n != 3 {
		t.Fatalf("unknown id read %d times", n-2)
	}
	c.add(10 * time.Second)
	_, _ = a.Authenticate(ctx, unknown.Full, "z")
	if n := f.lookups.Load(); n != 4 {
		t.Fatalf("the negative cache outlived its ttl: %d", n)
	}
	s := settings()
	s.NegativeCacheTTL = 0
	noNeg := auth.New(s, f, c.now)
	_, _ = noNeg.Authenticate(ctx, unknown.Full, "q")
	_, _ = noNeg.Authenticate(ctx, unknown.Full, "q")
	if n := f.lookups.Load(); n != 6 {
		t.Fatalf("negative cache 0s still caches: %d", n)
	}
}

func TestConcurrentFirstRequestsShareOneRead(t *testing.T) {
	req.Covers(t, "SEC-050")
	a, f, _, k := setup(t)
	f.gate = make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := a.Authenticate(context.Background(), k.Full, "c")
			errs <- err
		})
	}
	for f.lookups.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(f.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.lookups.Load(); n != 1 {
		t.Fatalf("%d concurrent first requests caused %d reads", 8, n)
	}
}

func TestWaiterGivesUpWithItsContext(t *testing.T) {
	a, f, _, k := setup(t)
	f.gate = make(chan struct{})
	done := make(chan struct{})
	go func() {
		_, _ = a.Authenticate(context.Background(), k.Full, "c")
		close(done)
	}()
	for f.lookups.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Authenticate(ctx, k.Full, "c"); !errors.Is(err, auth.ErrUnavailable) {
		t.Errorf("a waiter whose context ended: %v", err)
	}
	close(f.gate)
	<-done
}

func TestCacheSizeIsBounded(t *testing.T) {
	c := &clock{t: t0}
	f := &fakeStore{}
	s := settings()
	s.KeyCacheSize = 10
	a := auth.New(s, f, c.now)
	ctx := context.Background()
	for range 12 {
		k := issue(t, f, t0.Add(time.Hour))
		_, _ = a.Authenticate(ctx, k.Full, "c")
	}
	if n := a.Cached(); n != 10 {
		t.Fatalf("%d entries in a cache of 10", n)
	}
	c.add(2 * time.Minute) // every entry expired: the next one clears them
	k := issue(t, f, t0.Add(time.Hour))
	_, _ = a.Authenticate(ctx, k.Full, "c")
	if n := a.Cached(); n != 1 {
		t.Fatalf("expired entries kept: %d", n)
	}
}

// TestRefreshingAnEntryInAFullCacheEvictsNothing: reading an id the cache
// already holds again replaces that entry and evicts no other.
func TestRefreshingAnEntryInAFullCacheEvictsNothing(t *testing.T) {
	c := &clock{t: t0}
	f := &fakeStore{}
	s := settings()
	s.KeyCacheSize = 10
	a := auth.New(s, f, c.now)
	ctx := context.Background()
	var first access.Key
	for i := range 10 {
		if i == 5 {
			c.add(40 * time.Second)
		}
		k := issue(t, f, t0.Add(time.Hour))
		if i == 0 {
			first = k
		}
		_, _ = a.Authenticate(ctx, k.Full, "c")
	}
	c.add(30 * time.Second) // the first five expired, the last five live
	if _, err := a.Authenticate(ctx, first.Full, "c"); err != nil {
		t.Fatal(err)
	}
	if n := a.Cached(); n != 10 {
		t.Fatalf("re-reading a held id left %d entries, want 10", n)
	}
}

func TestFailureLimit(t *testing.T) {
	req.Covers(t, "SEC-052")
	a, _, c, k := setup(t)
	ctx := context.Background()
	for range 3 {
		_, _ = a.Authenticate(ctx, "vf_live_bad", "203.0.113.4")
	}
	if _, err := a.Authenticate(ctx, k.Full, "203.0.113.4"); !errors.Is(err, auth.ErrLimited) {
		t.Fatalf("after three failures: %v", err)
	}
	if _, err := a.Authenticate(ctx, k.Full, "203.0.113.8"); err != nil {
		t.Fatalf("another address is limited too: %v", err)
	}
	c.add(time.Minute)
	if _, err := a.Authenticate(ctx, k.Full, "203.0.113.4"); err != nil {
		t.Fatalf("the window did not end: %v", err)
	}
	_, _ = a.Authenticate(ctx, "vf_live_bad", "203.0.113.4")
	if _, err := a.Authenticate(ctx, k.Full, "203.0.113.4"); err != nil {
		t.Fatalf("one failure in a new window limits: %v", err)
	}
}

func TestFailureTrackingIsBounded(t *testing.T) {
	c := &clock{t: t0}
	s := settings()
	s.FailureTrackSize = 10
	s.FailureLimit = 1
	f := &fakeStore{}
	k := issue(t, f, t0.Add(time.Hour))
	a := auth.New(s, f, c.now)
	ctx := context.Background()
	for i := range 11 {
		_, _ = a.Authenticate(ctx, "bad", string(rune('a'+i)))
	}
	if _, err := a.Authenticate(ctx, k.Full, "k"); !errors.Is(err, auth.ErrLimited) {
		t.Fatalf("the newest address is not tracked: %v", err)
	}
	limited := 0
	for i := range 10 {
		if _, err := a.Authenticate(ctx, k.Full, string(rune('a'+i))); errors.Is(err, auth.ErrLimited) {
			limited++
		}
	}
	if limited > 9 {
		t.Fatalf("eleven addresses tracked in a table of ten")
	}
}

func TestPepperRotationRemacs(t *testing.T) {
	req.Covers(t, "SEC-011")
	c := &clock{t: t0}
	f := &fakeStore{}
	k := issue(t, f, t0.Add(30*24*time.Hour))
	s := settings()
	newPepper := bytes.Repeat([]byte("n"), 32)
	s.Peppers = access.Peppers{Current: newPepper, Previous: pepper}
	a := auth.New(s, f, c.now)
	ctx := context.Background()
	if _, err := a.Authenticate(ctx, k.Full, "c"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.keys[k.ID].MAC, access.MAC(newPepper, k.ID, k.Secret)) {
		t.Fatal("not re-MACed under the current pepper")
	}
	if _, err := a.Authenticate(ctx, k.Full, "c"); err != nil || len(f.remacs) != 1 {
		t.Fatalf("re-MACed twice: %v %v", err, f.remacs)
	}
	// The previous secret of a rotated key is re-MACed in its own column.
	k2 := issue(t, f, t0.Add(time.Hour))
	r := f.keys[k2.ID]
	r.PreviousMAC, r.PreviousUntil = r.MAC, t0.Add(time.Hour)
	r.MAC = access.MAC(newPepper, k2.ID, "other")
	f.keys[k2.ID] = r
	if _, err := a.Authenticate(ctx, k2.Full, "c"); err != nil || len(f.remacs) != 2 || !f.remacs[1] {
		t.Fatalf("previous secret: %v %v", err, f.remacs)
	}
	// A failed re-MAC does not fail the request; it is tried again.
	k3 := issue(t, f, t0.Add(time.Hour))
	f.remacErr = errors.New("read only")
	for range 2 {
		if _, err := a.Authenticate(ctx, k3.Full, "c"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.remacs) != 4 {
		t.Fatalf("re-MAC attempts %d", len(f.remacs))
	}
}

func TestRefreshDropsChangedKeys(t *testing.T) {
	req.Covers(t, "SEC-042")
	a, f, _, k := setup(t)
	ctx := context.Background()
	_, _ = a.Authenticate(ctx, k.Full, "c")
	r := f.keys[k.ID]
	at := t0
	r.RevokedAt = &at
	f.keys[k.ID] = r
	if _, err := a.Authenticate(ctx, k.Full, "c"); err != nil {
		t.Fatal("served from the cache before a refresh is expected")
	}
	f.changed = []string{k.ID}
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(ctx, k.Full, "c"); !errors.Is(err, access.ErrRevoked) {
		t.Fatalf("revocation not seen after a refresh: %v", err)
	}
	if err := a.Refresh(ctx); err != nil || len(f.since) != 2 || !f.since[1].Equal(t0.Add(time.Second)) {
		t.Fatalf("the mark did not advance to the store's instant: %v", f.since)
	}
	f.fail = errors.New("down")
	if err := a.Refresh(ctx); err == nil {
		t.Fatal("a failed refresh reported success")
	}
}

func TestRunRefreshesUntilCancelled(t *testing.T) {
	c := &clock{t: t0}
	f := &fakeStore{}
	s := settings()
	s.RevocationPropagation = time.Millisecond
	a := auth.New(s, f, c.now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	for {
		f.mu.Lock()
		n := len(f.since)
		f.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func TestNewDefaultsToTheSystemClock(t *testing.T) {
	f := &fakeStore{}
	k := issue(t, f, time.Now().Add(time.Hour))
	if _, err := auth.New(settings(), f, nil).Authenticate(context.Background(), k.Full, "c"); err != nil {
		t.Fatal(err)
	}
}

func TestANewSecretWorksAtOnceAfterARotation(t *testing.T) {
	req.Covers(t, "SEC-041")
	a, f, _, k := setup(t)
	ctx := context.Background()
	if _, err := a.Authenticate(ctx, k.Full, "c"); err != nil {
		t.Fatal(err)
	}
	// The store rotates the key behind the cached copy.
	rotated, _ := access.NewSecret(rand.Reader, access.Live, prefixes, k.ID)
	r := f.keys[k.ID]
	r.PreviousMAC, r.PreviousUntil, r.MAC = r.MAC, t0.Add(time.Hour), access.MAC(pepper, k.ID, rotated.Secret)
	f.keys[k.ID] = r
	if _, err := a.Authenticate(ctx, rotated.Full, "c"); err != nil {
		t.Fatalf("the new secret is refused until the cache expires: %v", err)
	}
	if _, err := a.Authenticate(ctx, k.Full, "c"); err != nil {
		t.Fatalf("the old secret in the overlap: %v", err)
	}
	// A wrong secret costs one more read, then is refused.
	before := f.lookups.Load()
	wrong, _ := access.NewSecret(rand.Reader, access.Live, prefixes, k.ID)
	if _, err := a.Authenticate(ctx, wrong.Full, "c"); !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("wrong secret: %v", err)
	}
	if n := f.lookups.Load() - before; n != 1 {
		t.Fatalf("a wrong secret caused %d reads", n)
	}
	// The second read fails: the store is down.
	f.fail = errors.New("down")
	if _, err := a.Authenticate(ctx, wrong.Full, "c"); !errors.Is(err, auth.ErrUnavailable) {
		t.Fatalf("re-read with the store down: %v", err)
	}
}
