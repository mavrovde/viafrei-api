// Package consolemem is an in-memory console.Store for tests: it keeps the
// rules of the access store's console tables (a code is used once and only
// before it expires, used and expired codes are dropped when a new one is
// offered, a signature counter only moves forward) and records every
// audited change.
package consolemem

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
)

type enrolment struct {
	owner   string
	expires time.Time
	used    bool
}

// Store is the in-memory store. Err, when set, fails every call.
type Store struct {
	mu         sync.Mutex
	Err        error
	Audit      []auth.Change
	enrolments map[string]enrolment
	passkeys   map[string]console.Passkey
}

// New returns an empty store.
func New() *Store {
	return &Store{enrolments: map[string]enrolment{}, passkeys: map[string]console.Passkey{}}
}

// Changes returns a copy of the audited changes.
func (s *Store) Changes() []auth.Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]auth.Change(nil), s.Audit...)
}

// Codes returns how many enrolment codes the store holds, open or not.
func (s *Store) Codes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.enrolments)
}

func (s *Store) OfferEnrolment(_ context.Context, c auth.Change, codeMAC []byte, owner string, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	for mac, e := range s.enrolments {
		if e.used || !c.At.Before(e.expires) {
			delete(s.enrolments, mac)
		}
	}
	s.enrolments[string(codeMAC)] = enrolment{owner: owner, expires: expires}
	s.Audit = append(s.Audit, c)
	return nil
}

func (s *Store) Enrolment(_ context.Context, codeMAC []byte) (console.Enrolment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return console.Enrolment{}, s.Err
	}
	e, ok := s.enrolments[string(codeMAC)]
	if !ok {
		return console.Enrolment{}, console.ErrCode
	}
	out := console.Enrolment{Owner: e.owner, ExpiresAt: e.expires, Used: e.used}
	for _, p := range s.passkeys {
		if p.Owner == e.owner {
			out.UserHandle = p.UserHandle
		}
	}
	return out, nil
}

func (s *Store) AddPasskey(_ context.Context, c auth.Change, codeMAC []byte, p console.Passkey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	e, ok := s.enrolments[string(codeMAC)]
	if !ok || e.used || e.owner != p.Owner || !c.At.Before(e.expires) {
		return console.ErrCode
	}
	e.used = true
	s.enrolments[string(codeMAC)] = e
	s.passkeys[string(p.ID)] = p
	s.Audit = append(s.Audit, c)
	return nil
}

func (s *Store) Passkey(_ context.Context, id []byte) (console.Passkey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return console.Passkey{}, s.Err
	}
	p, ok := s.passkeys[string(id)]
	if !ok {
		return console.Passkey{}, console.ErrUnknownPasskey
	}
	return p, nil
}

func (s *Store) SignedIn(_ context.Context, c auth.Change, id []byte, signCount uint32, flags byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	p, ok := s.passkeys[string(id)]
	if !ok || (signCount <= p.SignCount && (signCount != 0 || p.SignCount != 0)) {
		return console.ErrCounter
	}
	at := c.At
	p.SignCount, p.Flags, p.LastUsedAt = signCount, flags, &at
	s.passkeys[string(id)] = p
	s.Audit = append(s.Audit, c)
	return nil
}

func (s *Store) Passkeys(context.Context) ([]console.Passkey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	out := make([]console.Passkey, 0, len(s.passkeys))
	for _, p := range s.passkeys {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].ID, out[j].ID) < 0 })
	return out, nil
}

func (s *Store) RemovePasskey(_ context.Context, c auth.Change, id []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if _, ok := s.passkeys[string(id)]; !ok {
		return console.ErrUnknownPasskey
	}
	delete(s.passkeys, string(id))
	s.Audit = append(s.Audit, c)
	return nil
}
