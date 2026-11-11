// Package console holds the owner console's use cases: the guard that keeps
// its sessions, single-use challenges and confirmations and limits sign-in
// attempts, and the owners' passkey lifecycle (Spec: Threat Model, THR-018).
// Sessions, challenges and confirmations are held in memory only; a restart
// ends them all.
package console

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"
)

// GuardSettings are the console's session and challenge limits.
type GuardSettings struct {
	SessionTTL   time.Duration
	ChallengeTTL time.Duration
	ConfirmTTL   time.Duration
	SignInLimit  int
	SignInWindow time.Duration
	MaxPending   int
	MaxSessions  int
}

// The guard's refusals.
var (
	ErrNoSession   = errors.New("console: no valid session")
	ErrCSRF        = errors.New("console: missing or wrong CSRF token")
	ErrUnknown     = errors.New("console: unknown or already used challenge or confirmation")
	ErrExpired     = errors.New("console: the challenge or confirmation has expired")
	ErrRateLimited = errors.New("console: too many sign-in attempts")
	ErrBusy        = errors.New("console: too many pending challenges and confirmations")
)

// Session is a signed-in owner. Token is the cookie value; CSRF is the token
// every change form must carry. Passkey is the credential id the owner
// signed in with: the session lasts no longer than that passkey.
type Session struct {
	Token, CSRF, Owner string
	Passkey            []byte
	Expires            time.Time
}

type pending struct {
	kind    string
	session string
	data    any
	expires time.Time
}

// Guard keeps the console's sessions, challenges and confirmations.
type Guard struct {
	s    GuardSettings
	rand io.Reader
	now  func() time.Time

	mu       sync.Mutex
	sessions map[string]Session
	pending  map[string]pending
	window   time.Time
	attempts int
}

// NewGuard returns a guard. random and now default to crypto/rand and
// time.Now.
func NewGuard(s GuardSettings, random io.Reader, now func() time.Time) *Guard {
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Guard{s: s, rand: random, now: now, sessions: map[string]Session{}, pending: map[string]pending{}}
}

// token returns 32 random bytes, base64url without padding.
func (g *Guard) token() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(g.rand, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Attempt counts one sign-in or enrolment attempt, begun or failed, against
// admin.signin_limit; over the limit it returns ErrRateLimited and the time
// until the window ends.
func (g *Guard) Attempt() (time.Duration, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if !now.Before(g.window.Add(g.s.SignInWindow)) {
		g.window, g.attempts = now, 0
	}
	if g.attempts >= g.s.SignInLimit {
		return g.window.Add(g.s.SignInWindow).Sub(now), ErrRateLimited
	}
	g.attempts++
	return 0, nil
}

// put holds data under a new single-use id; the caller holds the lock.
func (g *Guard) put(kind, session string, data any, ttl time.Duration) (string, error) {
	now := g.now()
	if len(g.pending) >= g.s.MaxPending {
		for id, p := range g.pending {
			if !now.Before(p.expires) {
				delete(g.pending, id)
			}
		}
	}
	if len(g.pending) >= g.s.MaxPending {
		return "", ErrBusy
	}
	id, err := g.token()
	if err != nil {
		return "", err
	}
	g.pending[id] = pending{kind: kind, session: session, data: data, expires: now.Add(ttl)}
	return id, nil
}

// take removes the entry under id and returns its data; any entry is gone
// after its first use, right or wrong.
func (g *Guard) take(id, kind, session string) (any, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.pending[id]
	delete(g.pending, id)
	if !ok || p.kind != kind || p.session != session {
		return nil, ErrUnknown
	}
	if !g.now().Before(p.expires) {
		return nil, ErrExpired
	}
	return p.data, nil
}

// PutChallenge holds a sign-in or enrolment ceremony's state for
// admin.challenge_ttl under a new single-use id.
func (g *Guard) PutChallenge(kind string, data any) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.put("challenge:"+kind, "", data, g.s.ChallengeTTL)
}

// TakeChallenge returns the state under id and forgets it: ErrUnknown for an
// id never issued, already used or of another kind, ErrExpired past its
// lifetime.
func (g *Guard) TakeChallenge(id, kind string) (any, error) {
	return g.take(id, "challenge:"+kind, "")
}

// StartSession signs owner in with the passkey of credential id passkey.
// Beyond admin.max_sessions the session that ends first is ended now.
func (g *Guard) StartSession(owner string, passkey []byte) (Session, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.sessions) >= g.s.MaxSessions {
		var oldest string
		for t, s := range g.sessions {
			if oldest == "" || s.Expires.Before(g.sessions[oldest].Expires) {
				oldest = t
			}
		}
		g.end(oldest)
	}
	tok, err := g.token()
	if err != nil {
		return Session{}, err
	}
	csrf, err := g.token()
	if err != nil {
		return Session{}, err
	}
	s := Session{Token: tok, CSRF: csrf, Owner: owner, Passkey: passkey, Expires: g.now().Add(g.s.SessionTTL)}
	g.sessions[tok] = s
	return s, nil
}

// Session returns the live session of token, or ErrNoSession.
func (g *Guard) Session(token string) (Session, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessions[token]
	if !ok {
		return Session{}, ErrNoSession
	}
	if !g.now().Before(s.Expires) {
		g.end(token)
		return Session{}, ErrNoSession
	}
	return s, nil
}

// EndSession signs the session out; its pending confirmations go with it.
func (g *Guard) EndSession(token string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.end(token)
}

func (g *Guard) end(token string) {
	delete(g.sessions, token)
	for id, p := range g.pending {
		if p.session == token {
			delete(g.pending, id)
		}
	}
}

// CheckCSRF compares a form's token with the session's in constant time.
func CheckCSRF(s Session, token string) error {
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRF)) != 1 {
		return ErrCSRF
	}
	return nil
}

// PutConfirm holds an asked-for change until it is confirmed, for
// admin.confirm_ttl, bound to the session that asked.
func (g *Guard) PutConfirm(s Session, change any) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.put("confirm", s.Token, change, g.s.ConfirmTTL)
}

// TakeConfirm returns the change under id if s asked for it, and forgets it.
func (g *Guard) TakeConfirm(s Session, id string) (any, error) {
	return g.take(id, "confirm", s.Token)
}
