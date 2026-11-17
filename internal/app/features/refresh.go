package features

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// LiveSources is the port to the configured registry of live sources.
type LiveSources interface {
	// Check refuses an ask whose source is not registered
	// (refresh.ErrSource) or whose subject its schema refuses
	// (*refresh.SubjectError).
	Check(a refresh.Ask) error
	// Key builds the canonical key of a checked ask's subject, resolving
	// a value through lookup where a key rule says so.
	Key(ctx context.Context, a refresh.Ask, lookup refresh.Lookup) (string, error)
	// FreshFor is how long a settled refresh of the source answers a new
	// ask with no new refresh; zero for never.
	FreshFor(source string) time.Duration
}

// RefreshStore is the port to the store of refresh requests.
type RefreshStore interface {
	// Request answers n: the newest refresh of its source and subject key
	// settled refreshed or none_due at or after freshSince (none when it
	// is zero), else the open one, else n recorded as a new pending one;
	// the asking key is linked to the answer. An open answer is waited on
	// for up to wait, and returned as it then stands.
	Request(ctx context.Context, n refresh.New, freshSince time.Time, wait time.Duration) (refresh.Request, error)
	// Refresh returns the refresh id as it now stands, or
	// refresh.ErrNotFound when the key did not ask for it.
	Refresh(ctx context.Context, id, keyID string) (refresh.Request, error)
}

// ErrNoRefreshes is the answer of an instance with no refresh store.
var ErrNoRefreshes = errors.New("features: on-demand refreshes are not configured")

// Refreshes is what the on-demand refreshes (G8) run on.
type Refreshes struct {
	// Sources is the registry asks are checked and keyed against.
	Sources LiveSources
	// Store records the requests and waits on them.
	Store RefreshStore
	// Lookup resolves a subject value where a key rule says so.
	Lookup refresh.Lookup
	// Wait is how long an ask waits for its refresh to settle.
	Wait time.Duration
	// NewID makes a new request's id from its source and subject key; nil
	// for a random UUID v7.
	NewID func(source, subjectKey string) string
}

// WithRefreshes enables the on-demand refreshes.
func (s *Service) WithRefreshes(r Refreshes) *Service {
	if r.NewID == nil {
		// NewV7 fails only when the system's randomness does.
		r.NewID = func(string, string) string { return invariant.Must(uuid.NewV7()).String() }
	}
	s.refreshes = &r
	return s
}

// RequestRefresh asks for a refresh of a live source's subject on a
// person's view, for ViaFrei's own keys only: one refresh is shared by
// everyone asking about the same subject while it is open, and a refresh
// settled inside the source's freshness window answers without a new one.
func (s *Service) RequestRefresh(ctx context.Context, key access.Record, a refresh.Ask) (refresh.Request, error) {
	r, err := s.refreshesFor(key)
	if err != nil {
		return refresh.Request{}, err
	}
	if err := r.Sources.Check(a); err != nil {
		return refresh.Request{}, err
	}
	subjectKey, err := r.Sources.Key(ctx, a, r.Lookup)
	if err != nil {
		return refresh.Request{}, err
	}
	now := s.now()
	var since time.Time
	if d := r.Sources.FreshFor(a.Source); d > 0 {
		since = now.Add(-d)
	}
	return r.Store.Request(ctx, refresh.New{
		ID: r.NewID(a.Source, subjectKey), Source: a.Source, Subject: a.Subject, SubjectKey: subjectKey, KeyID: key.ID, At: now,
	}, since, r.Wait)
}

// Refresh returns a refresh the key asked for, as it now stands.
func (s *Service) Refresh(ctx context.Context, key access.Record, id string) (refresh.Request, error) {
	r, err := s.refreshesFor(key)
	if err != nil {
		return refresh.Request{}, err
	}
	return r.Store.Refresh(ctx, id, key.ID)
}

func (s *Service) refreshesFor(key access.Record) (*Refreshes, error) {
	switch {
	case key.Audience != access.AudienceOwn:
		return nil, ErrOwnKeyOnly
	case s.refreshes == nil:
		return nil, ErrNoRefreshes
	}
	return s.refreshes, nil
}
