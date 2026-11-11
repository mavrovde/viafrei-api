package console_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// clock is a settable test clock.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newClock() *clock { return &clock{t: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)} }

var settings = console.GuardSettings{
	SessionTTL: 15 * time.Minute, ChallengeTTL: time.Minute, ConfirmTTL: 2 * time.Minute,
	SignInLimit: 3, SignInWindow: 5 * time.Minute, MaxPending: 4, MaxSessions: 2,
}

// failingReader fails every read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

// limitedReader serves n good reads, then fails.
type limitedReader struct{ n int }

func (r *limitedReader) Read(b []byte) (int, error) {
	if r.n == 0 {
		return 0, errors.New("no entropy")
	}
	r.n--
	for i := range b {
		b[i] = byte(r.n + i)
	}
	return len(b), nil
}

func TestTheDefaultsAreTheSystemsOwn(t *testing.T) {
	req.Covers(t, "THR-018")
	g := console.NewGuard(settings, nil, nil)
	s, err := g.StartSession("owner")
	if err != nil || len(s.Token) != 43 || len(s.CSRF) != 43 || s.Token == s.CSRF || time.Until(s.Expires) <= 14*time.Minute {
		t.Fatalf("session %+v, %v", s, err)
	}
}

func TestSignInAttemptsAreLimitedPerWindow(t *testing.T) {
	req.Covers(t, "THR-018")
	c := newClock()
	g := console.NewGuard(settings, nil, c.now)
	for i := range 3 {
		if _, err := g.Attempt(); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	c.t = c.t.Add(time.Minute)
	wait, err := g.Attempt()
	if !errors.Is(err, console.ErrRateLimited) || wait != 4*time.Minute {
		t.Fatalf("over the limit: %v %v", wait, err)
	}
	c.t = c.t.Add(4 * time.Minute)
	if _, err := g.Attempt(); err != nil {
		t.Fatalf("a new window: %v", err)
	}
}

func TestAChallengeIsSingleUseAndShortLived(t *testing.T) {
	req.Covers(t, "THR-018")
	c := newClock()
	g := console.NewGuard(settings, nil, c.now)
	id, err := g.PutChallenge("signin", "state")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.TakeChallenge(id, "enrol"); !errors.Is(err, console.ErrUnknown) {
		t.Fatalf("another kind: %v", err)
	}
	// A wrong use spends it too.
	if _, err := g.TakeChallenge(id, "signin"); !errors.Is(err, console.ErrUnknown) {
		t.Fatalf("after a wrong use: %v", err)
	}
	id, _ = g.PutChallenge("signin", "state")
	got, err := g.TakeChallenge(id, "signin")
	if err != nil || got != "state" {
		t.Fatalf("take: %v %v", got, err)
	}
	if _, err := g.TakeChallenge(id, "signin"); !errors.Is(err, console.ErrUnknown) {
		t.Fatalf("reused: %v", err)
	}
	id, _ = g.PutChallenge("signin", "state")
	c.t = c.t.Add(time.Minute)
	if _, err := g.TakeChallenge(id, "signin"); !errors.Is(err, console.ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestPendingEntriesAreBounded(t *testing.T) {
	req.Covers(t, "THR-018")
	c := newClock()
	g := console.NewGuard(settings, nil, c.now)
	for range 4 {
		if _, err := g.PutChallenge("signin", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.PutChallenge("signin", nil); !errors.Is(err, console.ErrBusy) {
		t.Fatalf("full: %v", err)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := g.PutChallenge("signin", nil); err != nil {
		t.Fatalf("expired entries are dropped first: %v", err)
	}
}

func TestSessionsExpireEndAndAreBounded(t *testing.T) {
	req.Covers(t, "THR-018")
	c := newClock()
	g := console.NewGuard(settings, nil, c.now)
	first, _ := g.StartSession("a")
	c.t = c.t.Add(time.Second)
	second, _ := g.StartSession("b")
	c.t = c.t.Add(time.Second)
	third, _ := g.StartSession("c")
	if _, err := g.Session(first.Token); !errors.Is(err, console.ErrNoSession) {
		t.Fatalf("the oldest is ended beyond max_sessions: %v", err)
	}
	if s, err := g.Session(second.Token); err != nil || s.Owner != "b" {
		t.Fatalf("second: %+v %v", s, err)
	}
	id, _ := g.PutConfirm(third, "change")
	g.EndSession(third.Token)
	if _, err := g.Session(third.Token); !errors.Is(err, console.ErrNoSession) {
		t.Fatalf("ended: %v", err)
	}
	if _, err := g.TakeConfirm(third, id); !errors.Is(err, console.ErrUnknown) {
		t.Fatalf("an ended session's confirmations go with it: %v", err)
	}
	c.t = c.t.Add(15 * time.Minute)
	if _, err := g.Session(second.Token); !errors.Is(err, console.ErrNoSession) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := g.Session(""); !errors.Is(err, console.ErrNoSession) {
		t.Fatal("an empty token is no session")
	}
}

func TestTheCSRFTokenMustMatch(t *testing.T) {
	req.Covers(t, "THR-018")
	s := console.Session{CSRF: strings.Repeat("c", 43)}
	for _, tok := range []string{"", "x", strings.Repeat("d", 43)} {
		if err := console.CheckCSRF(s, tok); !errors.Is(err, console.ErrCSRF) {
			t.Fatalf("%q: %v", tok, err)
		}
	}
	if err := console.CheckCSRF(s, s.CSRF); err != nil {
		t.Fatal(err)
	}
	// A session without a token admits nothing.
	if err := console.CheckCSRF(console.Session{}, ""); !errors.Is(err, console.ErrCSRF) {
		t.Fatal(err)
	}
}

func TestAConfirmationIsBoundToItsSession(t *testing.T) {
	req.Covers(t, "THR-018")
	c := newClock()
	g := console.NewGuard(settings, nil, c.now)
	a, _ := g.StartSession("a")
	b, _ := g.StartSession("b")
	id, _ := g.PutConfirm(a, "change")
	if _, err := g.TakeConfirm(b, id); !errors.Is(err, console.ErrUnknown) {
		t.Fatalf("another session: %v", err)
	}
	id, _ = g.PutConfirm(a, "change")
	if _, err := g.TakeChallenge(id, "confirm"); !errors.Is(err, console.ErrUnknown) {
		t.Fatalf("a confirmation is no challenge: %v", err)
	}
	id, _ = g.PutConfirm(a, "change")
	if got, err := g.TakeConfirm(a, id); err != nil || got != "change" {
		t.Fatalf("take: %v %v", got, err)
	}
	id, _ = g.PutConfirm(a, "change")
	c.t = c.t.Add(2 * time.Minute)
	if _, err := g.TakeConfirm(a, id); !errors.Is(err, console.ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestAFailingRandomSourceIsAnError(t *testing.T) {
	req.Covers(t, "THR-018")
	g := console.NewGuard(settings, failingReader{}, nil)
	if _, err := g.StartSession("a"); err == nil {
		t.Fatal("session without entropy")
	}
	if _, err := g.PutChallenge("signin", nil); err == nil {
		t.Fatal("challenge without entropy")
	}
	g = console.NewGuard(settings, &limitedReader{n: 1}, nil)
	if _, err := g.StartSession("a"); err == nil {
		t.Fatal("a session needs its CSRF token too")
	}
}
