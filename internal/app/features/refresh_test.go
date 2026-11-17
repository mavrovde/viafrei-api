package features_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

type sources struct {
	checkErr, keyErr error
	fresh            time.Duration
	gotLookup        refresh.Lookup
}

func (s *sources) Check(refresh.Ask) error { return s.checkErr }

func (s *sources) Key(_ context.Context, a refresh.Ask, lookup refresh.Lookup) (string, error) {
	s.gotLookup = lookup
	return "k:" + a.Source, s.keyErr
}

func (s *sources) FreshFor(string) time.Duration { return s.fresh }

type refreshStore struct {
	got       refresh.New
	gotSince  time.Time
	gotWait   time.Duration
	gotID     string
	gotKey    string
	answer    refresh.Request
	err       error
	requested int
}

func (r *refreshStore) Request(_ context.Context, n refresh.New, since time.Time, wait time.Duration) (refresh.Request, error) {
	r.requested++
	r.got, r.gotSince, r.gotWait = n, since, wait
	return r.answer, r.err
}

func (r *refreshStore) Refresh(_ context.Context, id, keyID string) (refresh.Request, error) {
	r.gotID, r.gotKey = id, keyID
	return r.answer, r.err
}

func noLookup(context.Context, string, string) (string, error) { return "", nil }

func TestRequestRefreshIsForOwnKeysOnAConfiguredInstance(t *testing.T) {
	ctx := context.Background()
	s := features.New(features.Repositories{}, features.Settings{}, func() time.Time { return now })
	own := transitKey
	own.Audience = access.AudienceOwn
	ask := refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "ags:09162000"}}
	if _, err := s.RequestRefresh(ctx, own, ask); !errors.Is(err, features.ErrNoRefreshes) {
		t.Fatalf("not configured: %v", err)
	}
	if _, err := s.Refresh(ctx, own, "x"); !errors.Is(err, features.ErrNoRefreshes) {
		t.Fatalf("not configured, read: %v", err)
	}
	src, st := &sources{}, &refreshStore{}
	s.WithRefreshes(features.Refreshes{Sources: src, Store: st, Lookup: noLookup, Wait: 1200 * time.Millisecond})
	if _, err := s.RequestRefresh(ctx, transitKey, ask); !errors.Is(err, features.ErrOwnKeyOnly) || st.requested != 0 {
		t.Fatalf("public key: %v", err)
	}
	if _, err := s.Refresh(ctx, transitKey, "x"); !errors.Is(err, features.ErrOwnKeyOnly) {
		t.Fatalf("public key, read: %v", err)
	}

	// No freshness window: since is zero.
	st.answer = refresh.Request{ID: "r1", State: refresh.Pending}
	got, err := s.RequestRefresh(ctx, own, ask)
	if err != nil || got.ID != "r1" || st.got.Source != "fuel" || st.got.SubjectKey != "k:fuel" || st.got.KeyID != own.ID ||
		!st.got.At.Equal(now) || !st.gotSince.IsZero() || st.gotWait != 1200*time.Millisecond || src.gotLookup == nil {
		t.Fatalf("ask: %+v %v %+v", got, err, st)
	}
	id, err := uuid.Parse(st.got.ID)
	if err != nil || id.Version() != 7 {
		t.Fatalf("the id is a uuid v7: %q %v", st.got.ID, err)
	}
	src.fresh = 5 * time.Minute
	if _, err := s.RequestRefresh(ctx, own, ask); err != nil || !st.gotSince.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("fresh window: %v %v", err, st.gotSince)
	}

	src.keyErr = &refresh.SubjectError{Location: "subject.name", Detail: "nothing answers"}
	if _, err := s.RequestRefresh(ctx, own, ask); !errors.Is(err, src.keyErr) {
		t.Fatalf("key: %v", err)
	}
	src.checkErr = refresh.ErrSource
	if _, err := s.RequestRefresh(ctx, own, ask); !errors.Is(err, refresh.ErrSource) || st.requested != 2 {
		t.Fatalf("check: %v", err)
	}
	src.checkErr, src.keyErr = nil, nil
	s.WithRefreshes(features.Refreshes{Sources: src, Store: st, NewID: func(source, key string) string { return source + "/" + key }})
	if _, err := s.RequestRefresh(ctx, own, ask); err != nil || st.got.ID != "fuel/k:fuel" || st.gotWait != 0 {
		t.Fatalf("own ids: %v %+v", err, st.got)
	}

	st.err = refresh.ErrNotFound
	if _, err := s.Refresh(ctx, own, "r1"); !errors.Is(err, refresh.ErrNotFound) || st.gotID != "r1" || st.gotKey != own.ID {
		t.Fatalf("read: %v %+v", err, st)
	}
}
