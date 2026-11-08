// Package auth authenticates presented API keys against the access store,
// with the key cache, the negative cache, the authentication-failure limit
// and the revocation refresh in front of it, and runs the key lifecycle
// (issue, rotate, revoke, terms acceptance) for the operator tool.
package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// cryptoRand is the source of key ids and secrets.
var cryptoRand io.Reader = rand.Reader

// ErrUnknown is what a Store answers for a key id it does not hold.
var ErrUnknown = errors.New("auth: unknown key id")

// The refusals the authenticator adds to the domain's.
var (
	// ErrLimited: the client address had too many failures in the window.
	ErrLimited = errors.New("auth: too many authentication failures from this address")
	// ErrUnavailable: the access store could not answer.
	ErrUnavailable = errors.New("auth: access store unavailable")
)

// Store is the read side of the access store the authenticator needs.
type Store interface {
	// Lookup returns the key with its plan and acceptances, or ErrUnknown.
	Lookup(ctx context.Context, id string) (access.Record, error)
	// Remac replaces the stored MAC (the previous one when previous is set)
	// by mac, the value under the current pepper (SEC-011).
	Remac(ctx context.Context, id string, previous bool, mac []byte) error
	// ChangedSince returns the ids of keys or plans changed after since,
	// and the store's own instant of the read, the mark of the next call.
	ChangedSince(ctx context.Context, since time.Time) (ids []string, mark time.Time, err error)
}

// Settings are the auth.* values the authenticator works with.
type Settings struct {
	Environment      access.Environment
	Prefixes         access.Prefixes
	Peppers          access.Peppers
	KeyCacheTTL      time.Duration
	NegativeCacheTTL time.Duration
	KeyCacheSize     int
	ExpiryWarning    time.Duration
	FailureLimit     int
	FailureWindow    time.Duration
	FailureTrackSize int
	RotationOverlap  time.Duration
	DefaultExpiry    time.Duration
	// RevocationPropagation is the interval of Run (SEC-042).
	RevocationPropagation time.Duration
	// MinExpiry and MaxExpiry bound an explicit key lifetime (SEC-040).
	MinExpiry, MaxExpiry time.Duration
	// AuditRetention is how long an audit record is kept (SEC-045).
	AuditRetention time.Duration
}

// Principal is an authenticated key.
type Principal struct {
	Key access.Record
	// Expiring is set inside the expiry-warning window (SEC-043).
	Expiring bool
}

type entry struct {
	rec   access.Record
	err   error
	until time.Time
}

type call struct {
	done chan struct{}
	rec  access.Record
	err  error
}

type window struct {
	start time.Time
	n     int
}

// Authenticator checks presented keys.
type Authenticator struct {
	s     Settings
	store Store
	now   func() time.Time
	dummy access.Record

	mu    sync.Mutex
	cache map[string]entry
	calls map[string]*call
	fails map[string]*window
	mark  time.Time
}

// New returns an authenticator over store. now is the clock (time.Now when
// nil).
func New(s Settings, store Store, now func() time.Time) *Authenticator {
	if now == nil {
		now = time.Now
	}
	a := &Authenticator{
		s: s, store: store, now: now,
		cache: map[string]entry{}, calls: map[string]*call{}, fails: map[string]*window{},
		mark: now(),
	}
	// The record an unknown id is checked against, so that an unknown id
	// costs the same MAC work as a known one (SEC-003).
	mac := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, mac) // crypto/rand.Reader never fails
	a.dummy = access.Record{ID: "0000000000", MAC: mac, PreviousMAC: mac, PreviousUntil: now().Add(s.MaxExpiry), ExpiresAt: now().Add(s.MaxExpiry)}
	return a
}

// Authenticate checks the presented key for a request from client (an
// address). It answers access.ErrMissing, access.ErrInvalid,
// access.ErrExpired, access.ErrRevoked, ErrLimited or ErrUnavailable.
func (a *Authenticator) Authenticate(ctx context.Context, presented, client string) (Principal, error) {
	if a.limited(client) {
		return Principal{}, ErrLimited
	}
	if presented == "" {
		return Principal{}, access.ErrMissing
	}
	now := a.now()
	id, secret, err := access.Parse(presented, a.s.Environment, a.s.Prefixes)
	if err != nil {
		_, _ = access.Verify(a.dummy, secret, a.s.Peppers, now)
		a.fail(client)
		return Principal{}, access.ErrInvalid
	}
	rec, cached, err := a.lookup(ctx, id)
	switch {
	case errors.Is(err, ErrUnknown):
		_, _ = access.Verify(a.dummy, secret, a.s.Peppers, now)
		fallthrough
	case err != nil:
		return Principal{}, a.refuse(client, err)
	}
	m, err := access.Verify(rec, secret, a.s.Peppers, now)
	if errors.Is(err, access.ErrInvalid) && cached {
		// The cached copy may predate a rotation: read the key once more, so
		// a new secret works at once (SEC-041).
		a.drop(id)
		if rec, _, err = a.lookup(ctx, id); err != nil {
			return Principal{}, a.refuse(client, err)
		}
		m, err = access.Verify(rec, secret, a.s.Peppers, now)
	}
	if err != nil {
		a.fail(client)
		return Principal{}, err
	}
	if m.Remac != nil && a.store.Remac(ctx, id, m.Previous, m.Remac) == nil {
		// Stored under the current pepper now; the cache follows, so the
		// next request does not write again.
		if m.Previous {
			rec.PreviousMAC = m.Remac
		} else {
			rec.MAC = m.Remac
		}
		a.put(id, entry{rec: rec, until: now.Add(a.s.KeyCacheTTL)})
	}
	return Principal{Key: rec, Expiring: a.s.ExpiryWarning > 0 && rec.ExpiresAt.Sub(now) <= a.s.ExpiryWarning}, nil
}

// lookup reads a key through the cache; concurrent first requests for one
// id share one store read (SEC-050).
func (a *Authenticator) lookup(ctx context.Context, id string) (rec access.Record, cached bool, err error) {
	a.mu.Lock()
	if e, ok := a.cache[id]; ok && a.now().Before(e.until) {
		a.mu.Unlock()
		return e.rec, e.err == nil, e.err
	}
	if c, ok := a.calls[id]; ok {
		a.mu.Unlock()
		select {
		case <-c.done:
			return c.rec, false, c.err
		case <-ctx.Done():
			return access.Record{}, false, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	a.calls[id] = c
	a.mu.Unlock()

	c.rec, c.err = a.store.Lookup(ctx, id)

	now := a.now()
	switch {
	case c.err == nil:
		a.put(id, entry{rec: c.rec, until: now.Add(a.s.KeyCacheTTL)})
	case errors.Is(c.err, ErrUnknown) && a.s.NegativeCacheTTL > 0:
		a.put(id, entry{err: c.err, until: now.Add(a.s.NegativeCacheTTL)})
	}
	a.mu.Lock()
	delete(a.calls, id)
	a.mu.Unlock()
	close(c.done)
	return c.rec, false, c.err
}

// put stores an entry; a full cache first drops its expired entries, then
// an arbitrary one.
func (a *Authenticator) put(id string, e entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.cache[id]; !ok && len(a.cache) >= a.s.KeyCacheSize {
		now := a.now()
		for k, v := range a.cache {
			if !now.Before(v.until) {
				delete(a.cache, k)
			}
		}
		for k := range a.cache {
			if len(a.cache) < a.s.KeyCacheSize {
				break
			}
			delete(a.cache, k)
		}
	}
	a.cache[id] = e
}

// Cached returns the number of key ids held in the cache.
func (a *Authenticator) Cached() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.cache)
}

func (a *Authenticator) limited(client string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.fails[client]
	return w != nil && a.now().Before(w.start.Add(a.s.FailureWindow)) && w.n >= a.s.FailureLimit
}

func (a *Authenticator) fail(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	w := a.fails[client]
	if w == nil || !now.Before(w.start.Add(a.s.FailureWindow)) {
		if w == nil && len(a.fails) >= a.s.FailureTrackSize {
			for k, v := range a.fails {
				if !now.Before(v.start.Add(a.s.FailureWindow)) || len(a.fails) >= a.s.FailureTrackSize {
					delete(a.fails, k)
				}
			}
		}
		w = &window{start: now}
		a.fails[client] = w
	}
	w.n++
}

// Refresh drops every key changed in the access store since the last
// refresh from the cache, so a revocation or a plan change is seen within
// auth.revocation_propagation (SEC-042).
func (a *Authenticator) Refresh(ctx context.Context) error {
	a.mu.Lock()
	since := a.mark
	a.mu.Unlock()
	ids, mark, err := a.store.ChangedSince(ctx, since)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range ids {
		delete(a.cache, id)
	}
	a.mark = mark
	return nil
}

// Run refreshes every auth.revocation_propagation until ctx ends. A failed refresh is retried
// at the next interval; the cache lifetime bounds what it can miss.
func (a *Authenticator) Run(ctx context.Context) {
	t := time.NewTicker(a.s.RevocationPropagation)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = a.Refresh(ctx)
		}
	}
}

// refuse turns a failed read into the answer: an unknown id is invalid and
// counts as a failure; anything else means the store could not answer.
func (a *Authenticator) refuse(client string, err error) error {
	if errors.Is(err, ErrUnknown) {
		a.fail(client)
		return access.ErrInvalid
	}
	return errors.Join(ErrUnavailable, err)
}

func (a *Authenticator) drop(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.cache, id)
}
