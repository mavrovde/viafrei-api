package features

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// LiveSources is the port to the configured registry of live sources.
type LiveSources interface {
	// Check refuses an ask whose source is not registered
	// (refresh.ErrSource), is switched off (refresh.ErrPaused) or whose
	// subject its schema refuses (*refresh.SubjectError).
	Check(a refresh.Ask) error
	// Key builds the key of a checked subject of the source, resolving a
	// value through lookup where a case says so.
	Key(ctx context.Context, source string, subject map[string]any, lookup refresh.Lookup) (string, error)
	// Stored is the subject stored for a key: public ids only; a key that
	// is no public id is refused (*refresh.SubjectError).
	Stored(source, key string) (map[string]any, error)
	// FreshFor is how long a settled refresh of the source answers a new
	// ask with no new refresh; zero for never.
	FreshFor(source string) time.Duration
	// Deadline is how long an ask of the source waits for its refresh to
	// settle; zero when the registry sets none.
	Deadline(source string) time.Duration
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

// LookupAGS is the lookup that resolves a position, "lat,lon", to the AGS of
// its Gemeinde (refresh.Lookup).
const LookupAGS = "ags"

// LookupAnchor is the lookup that resolves a motorway page's anchor, "lat,lon",
// to the BASt LCL location code of the motorway point it lies on, "" when
// none lies near enough to be one (15 m, as the platform tests it: a page
// rounds its anchor to four decimals, under 7 m, far below any Gemeinde).
const LookupAnchor = "anchor"

// point is a subject value naming a position.
var point = regexp.MustCompile(`^pt:(-?[0-9]{1,3}(?:\.[0-9]+)?),(-?[0-9]{1,3}(?:\.[0-9]+)?)$`)

// anchorPoint is a motorway page's anchor by its own shape, as the platform
// reads it: a position of at most four decimals.
var anchorPoint = regexp.MustCompile(`^pt:-?[0-9]{1,2}\.[0-9]{1,4},-?[0-9]{1,3}\.[0-9]{1,4}$`)

// Refreshes is what the on-demand refreshes (G8) run on.
type Refreshes struct {
	// Sources is the registry asks are checked and keyed against.
	Sources LiveSources
	// Store records the requests and waits on them.
	Store RefreshStore
	// Lookup resolves a subject value where a key case says so, and a
	// position to its Gemeinde (LookupAGS).
	Lookup refresh.Lookup
	// Wait is how long an ask waits for its refresh to settle when the
	// registry sets no deadline for its source.
	Wait time.Duration
}

// WithRefreshes enables the on-demand refreshes.
func (s *Service) WithRefreshes(r Refreshes) *Service {
	s.refreshes = &r
	return s
}

// RequestRefresh asks for a refresh of a live source's subject on a
// person's view, for ViaFrei's own keys only: one refresh is shared by
// everyone asking about the same subject while it is open, and a refresh
// settled inside the source's freshness window answers without a new one.
//
// A position in the subject is resolved in memory before the subject is
// keyed, and only the public id the key names is stored (the data-steward's
// privacy ruling, lead ruling 1 of 2026-11-17): a motorway page's anchor
// (anchor true) to the BASt LCL code of the motorway point it lies on
// (anchor:<code>), any other position, and an anchor on no motorway point, to
// the AGS of its Gemeinde. A position no Gemeinde answers is refused, and
// nothing is recorded.
func (s *Service) RequestRefresh(ctx context.Context, key access.Record, a refresh.Ask) (refresh.Request, error) {
	r, err := s.refreshesFor(key)
	if err != nil {
		return refresh.Request{}, err
	}
	if err := r.Sources.Check(a); err != nil {
		return refresh.Request{}, err
	}
	subject, err := r.resolvePoints(ctx, a.Subject)
	if err != nil {
		return refresh.Request{}, err
	}
	subjectKey, err := r.Sources.Key(ctx, a.Source, subject, r.Lookup)
	if err != nil {
		return refresh.Request{}, err
	}
	stored, err := r.Sources.Stored(a.Source, subjectKey)
	if err != nil {
		return refresh.Request{}, err
	}
	now := s.now()
	var since time.Time
	if d := r.Sources.FreshFor(a.Source); d > 0 {
		since = now.Add(-d)
	}
	wait := r.Sources.Deadline(a.Source)
	if wait == 0 {
		wait = r.Wait
	}
	return r.Store.Request(ctx, refresh.New{Source: a.Source, Subject: stored, SubjectKey: subjectKey, KeyID: key.ID, RequestedBy: key.Plan.Name, At: now}, since, wait)
}

// resolvePoints is the subject with each position (pt:lat,lon) replaced by
// its public id: the BASt LCL code of the motorway point an anchor lies on
// (anchor:<code>), else the AGS of its Gemeinde (ags:<8 digits>); the
// position is never kept.
func (r *Refreshes) resolvePoints(ctx context.Context, subject map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(subject))
	anchor := subject["anchor"] == true
	for field, v := range subject {
		out[field] = v
		str, _ := v.(string)
		m := point.FindStringSubmatch(str)
		if m == nil {
			continue
		}
		if anchor && r.Lookup != nil && anchorPoint.MatchString(str) {
			code, err := r.Lookup(ctx, LookupAnchor, m[1]+","+m[2])
			if err != nil {
				return nil, err
			}
			if code != "" {
				out[field] = "anchor:" + code
				continue
			}
		}
		ags := ""
		if r.Lookup != nil {
			var err error
			if ags, err = r.Lookup(ctx, LookupAGS, m[1]+","+m[2]); err != nil {
				return nil, err
			}
		}
		if ags == "" {
			return nil, &refresh.SubjectError{Location: "subject." + field, Detail: "no Gemeinde answers this position"}
		}
		out[field] = "ags:" + ags
	}
	return out, nil
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
