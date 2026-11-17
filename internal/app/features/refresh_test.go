package features_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

type sources struct {
	checkErr, keyErr, storedErr error
	fresh, deadline             time.Duration
	gotLookup                   refresh.Lookup
	gotSubject                  map[string]any
}

func (s *sources) Check(refresh.Ask) error { return s.checkErr }

func (s *sources) Key(_ context.Context, source string, subject map[string]any, lookup refresh.Lookup) (string, error) {
	s.gotLookup, s.gotSubject = lookup, subject
	at, _ := subject["at"].(string)
	return at, s.keyErr
}

func (s *sources) Stored(_, key string) (map[string]any, error) {
	return map[string]any{"at": key}, s.storedErr
}

func (s *sources) FreshFor(string) time.Duration { return s.fresh }

func (s *sources) Deadline(string) time.Duration { return s.deadline }

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

// gemeinden answers the AGS of one position, fails on another and knows no
// other.
func gemeinden(_ context.Context, name, value string) (string, error) {
	switch {
	case name == features.LookupAnchor && value == "49.2648,12.9487":
		return "12345", nil
	case name == features.LookupAnchor && value == "49.2600,12.9400":
		return "a7-12.n", nil
	case name == features.LookupAnchor && value == "49.2700,12.9400":
		return "1234567", nil
	case name == features.LookupAnchor && value == "2.0,2.0":
		return "", errors.New("store down")
	case name == features.LookupAnchor:
		return "", nil
	case name != features.LookupAGS:
		return "", errors.New("unknown lookup " + name)
	case value == "48.137,11.575":
		return "09162000", nil
	case value == "1,1":
		return "", errors.New("store down")
	}
	return "", nil
}

func TestRequestRefreshIsForOwnKeysOnAConfiguredInstance(t *testing.T) {
	ctx := context.Background()
	s := features.New(features.Repositories{}, features.Settings{}, func() time.Time { return now })
	own := transitKey
	own.Audience = access.AudienceOwn
	own.Plan.Name = "mcp"
	for plan, want := range map[string]string{"site": "site", "mcp": "mcp", "dev": "dev", "parity": "dev", "": "dev"} {
		if got := features.Door(plan); got != want {
			t.Errorf("plan %q asks as %q, want %q", plan, got, want)
		}
	}
	ask := refresh.Ask{Source: "fuel-prices-de", Subject: map[string]any{"at": "ags:09162000"}}
	if _, err := s.RequestRefresh(ctx, own, ask); !errors.Is(err, features.ErrNoRefreshes) {
		t.Fatalf("not configured: %v", err)
	}
	if _, err := s.Refresh(ctx, own, "x"); !errors.Is(err, features.ErrNoRefreshes) {
		t.Fatalf("not configured, read: %v", err)
	}
	src, st := &sources{}, &refreshStore{}
	s.WithRefreshes(features.Refreshes{Sources: src, Store: st, Lookup: gemeinden, Wait: 1200 * time.Millisecond})
	if _, err := s.RequestRefresh(ctx, transitKey, ask); !errors.Is(err, features.ErrOwnKeyOnly) || st.requested != 0 {
		t.Fatalf("public key: %v", err)
	}
	if _, err := s.Refresh(ctx, transitKey, "x"); !errors.Is(err, features.ErrOwnKeyOnly) {
		t.Fatalf("public key, read: %v", err)
	}

	// No freshness window: since is zero; no deadline: the configured wait.
	st.answer = refresh.Request{ID: "1", State: refresh.Pending}
	got, err := s.RequestRefresh(ctx, own, ask)
	if err != nil || got.ID != "1" || st.got.Source != "fuel-prices-de" || st.got.SubjectKey != "ags:09162000" || st.got.KeyID != own.ID ||
		st.got.RequestedBy != "mcp" || !st.got.At.Equal(now) || !st.gotSince.IsZero() || st.gotWait != 1200*time.Millisecond || src.gotLookup == nil {
		t.Fatalf("ask: %+v %v %+v", got, err, st)
	}
	src.fresh, src.deadline = 5*time.Minute, 300*time.Millisecond
	if _, err := s.RequestRefresh(ctx, own, ask); err != nil || !st.gotSince.Equal(now.Add(-5*time.Minute)) || st.gotWait != 300*time.Millisecond {
		t.Fatalf("fresh window and deadline: %v %v %v", err, st.gotSince, st.gotWait)
	}

	src.keyErr = &refresh.SubjectError{Location: "subject.name", Detail: "nothing answers"}
	if _, err := s.RequestRefresh(ctx, own, ask); !errors.Is(err, src.keyErr) {
		t.Fatalf("key: %v", err)
	}
	src.keyErr, src.storedErr = nil, &refresh.SubjectError{Location: "subject", Detail: "no public id"}
	if _, err := s.RequestRefresh(ctx, own, ask); !errors.Is(err, src.storedErr) {
		t.Fatalf("stored: %v", err)
	}
	src.checkErr = refresh.ErrSource
	if _, err := s.RequestRefresh(ctx, own, ask); !errors.Is(err, refresh.ErrSource) || st.requested != 2 {
		t.Fatalf("check: %v", err)
	}
	src.checkErr, src.storedErr = nil, nil

	st.err = refresh.ErrNotFound
	if _, err := s.Refresh(ctx, own, "1"); !errors.Is(err, refresh.ErrNotFound) || st.gotID != "1" || st.gotKey != own.ID {
		t.Fatalf("read: %v %+v", err, st)
	}
}

// TestRequestRefreshStoresAPositionAsItsGemeinde: the privacy ruling. A
// position is keyed and stored as the AGS of its Gemeinde, never as itself;
// a position no Gemeinde answers is refused with nothing recorded.
func TestRequestRefreshStoresAPositionAsItsGemeinde(t *testing.T) {
	ctx := context.Background()
	own := transitKey
	own.Audience = access.AudienceOwn
	src, st := &sources{}, &refreshStore{}
	s := features.New(features.Repositories{}, features.Settings{}, func() time.Time { return now }).
		WithRefreshes(features.Refreshes{Sources: src, Store: st, Lookup: gemeinden})
	if _, err := s.RequestRefresh(ctx, own, refresh.Ask{Source: "fuel-prices-de", Subject: map[string]any{"at": "pt:48.137,11.575", "snap": true}}); err != nil {
		t.Fatal(err)
	}
	if src.gotSubject["at"] != "ags:09162000" || src.gotSubject["snap"] != true || st.got.SubjectKey != "ags:09162000" ||
		len(st.got.Subject) != 1 || st.got.Subject["at"] != "ags:09162000" {
		t.Fatalf("keyed %v, stored %+v", src.gotSubject, st.got)
	}
	var se *refresh.SubjectError
	for _, at := range []string{"pt:0,0", "pt:1,1"} {
		st.requested = 0
		_, err := s.RequestRefresh(ctx, own, refresh.Ask{Source: "fuel-prices-de", Subject: map[string]any{"at": at}})
		if st.requested != 0 || (at == "pt:0,0") != errors.As(err, &se) || err == nil || strings.Contains(err.Error(), at[3:]) {
			t.Fatalf("%s: %v, %d recorded", at, err, st.requested)
		}
	}
	s.WithRefreshes(features.Refreshes{Sources: src, Store: st})
	if _, err := s.RequestRefresh(ctx, own, refresh.Ask{Source: "fuel-prices-de", Subject: map[string]any{"at": "pt:48.137,11.575"}}); !errors.As(err, &se) || se.Location != "subject.at" {
		t.Fatalf("no lookup: %v", err)
	}
}

// TestRequestRefreshStoresAMotorwayAnchorAsItsLCLCode: lead ruling 1 of
// 2026-11-17. A motorway page's anchor (anchor true, four decimals at most)
// on a motorway point of the BASt LCL is keyed and stored as that point's
// code, anchor:<code>; an anchor on none, an anchor of more decimals and a
// position not marked anchor are stored as the AGS of their Gemeinde.
func TestRequestRefreshStoresAMotorwayAnchorAsItsLCLCode(t *testing.T) {
	ctx := context.Background()
	own := transitKey
	own.Audience = access.AudienceOwn
	src, st := &sources{}, &refreshStore{}
	s := features.New(features.Repositories{}, features.Settings{}, func() time.Time { return now }).
		WithRefreshes(features.Refreshes{Sources: src, Store: st, Lookup: gemeinden})
	for _, c := range []struct {
		subject map[string]any
		want    string
	}{
		{map[string]any{"at": "pt:49.2648,12.9487", "anchor": true}, "anchor:12345"},
		{map[string]any{"at": "pt:48.137,11.575", "anchor": true}, "ags:09162000"},
		{map[string]any{"at": "pt:49.2648,12.9487"}, ""},
		{map[string]any{"at": "pt:49.26477,12.94870", "anchor": true}, ""},
		// A code that is no numeric LCL code of six digits at most is never
		// stored: the position goes to its Gemeinde, of which none answers.
		{map[string]any{"at": "pt:49.2600,12.9400", "anchor": true}, ""},
		{map[string]any{"at": "pt:49.2700,12.9400", "anchor": true}, ""},
	} {
		st.requested = 0
		_, err := s.RequestRefresh(ctx, own, refresh.Ask{Source: "fuel-prices-de", Subject: c.subject})
		if c.want == "" {
			var se *refresh.SubjectError
			if !errors.As(err, &se) || st.requested != 0 {
				t.Errorf("%v: %v, %d recorded; want it resolved to a Gemeinde, of which none answers", c.subject, err, st.requested)
			}
			continue
		}
		if err != nil || src.gotSubject["at"] != c.want || st.got.SubjectKey != c.want || len(st.got.Subject) != 1 || st.got.Subject["at"] != c.want {
			t.Errorf("%v: keyed %v, stored %+v, %v; want %s", c.subject, src.gotSubject, st.got, err, c.want)
		}
	}
	if _, err := s.RequestRefresh(ctx, own, refresh.Ask{Source: "fuel-prices-de", Subject: map[string]any{"at": "pt:2.0,2.0", "anchor": true}}); err == nil || errors.As(err, new(*refresh.SubjectError)) {
		t.Error("a failed anchor lookup answered")
	}
}
