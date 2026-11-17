//go:build integration

package accessstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/fakelive"
)

// refreshAt is the asker's clock: a day of the fixture world, whose
// partitions hold the ticks the fake writes.
var refreshAt = fixtures.ReferenceInstant.AddDate(0, 0, 2)

// fake runs the fake live refresher on the test database with the
// repository's registry and script.
func fake(t *testing.T) *fakelive.Fake {
	t.Helper()
	sc, err := fakelive.Load(filepath.Join("..", "..", "..", "test", "live", "sources.json"), filepath.Join("..", "..", "..", "test", "live", "fakelive.json"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := fakelive.Start(bounded(t), db.OwnerURL, sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Stop(); err != nil {
			t.Error(err)
		}
	})
	return f
}

// askers issues n keys of ViaFrei's own audience.
func askers(t *testing.T, s *accessstore.Store, n int) []string {
	t.Helper()
	k := auth.NewKeys(wire.AuthSettings(cfg(t)), s, nil, nil)
	plan := newPlan(t, k, "consumer", "fuel_alpha")
	ids := make([]string, n)
	for i := range ids {
		key, err := k.IssueAs(bounded(t), access.AudienceOwn, plan, "site", "ops@example.test", 0, "tester")
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = key.ID
	}
	return ids
}

func ask(source, key, keyID string) refresh.New {
	return refresh.New{ID: uuid.Must(uuid.NewV7()).String(), Source: source, Subject: map[string]any{"k": key},
		SubjectKey: key, KeyID: keyID, At: refreshAt}
}

func TestARefreshSettlesByTheFakeAndAnswersItsAskers(t *testing.T) {
	s := open(t)
	f := fake(t)
	ids := askers(t, s, 2)
	ctx := bounded(t)
	const wait = 1200 * time.Millisecond

	for _, c := range []struct {
		key    string
		state  refresh.State
		reason string
	}{
		{"ags:09162000", refresh.Refreshed, ""},
		{"ags:11000000", refresh.NoneDue, ""},
		{"ags:02000000", refresh.Refused, "the hourly refresh cap of the area is reached"},
		{"ags:14612000", refresh.Failed, "the provider did not answer"},
	} {
		n := ask("fuel", c.key, ids[0])
		r, err := s.Request(ctx, n, time.Time{}, wait)
		if err != nil || r.ID != n.ID || r.Source != "fuel" || r.State != c.state || r.Reason != c.reason ||
			r.SettledAt == nil || !r.SettledAt.Equal(refreshAt) || !r.CreatedAt.Equal(refreshAt) || r.Position != nil {
			t.Fatalf("%s: %+v %v", c.key, r, err)
		}
		got, err := s.Refresh(ctx, n.ID, ids[0])
		if err != nil || got.State != c.state {
			t.Fatalf("%s read back: %+v %v", c.key, got, err)
		}
	}
	// The fake wrote the source's ticks: the fixture's newest prices again,
	// dated at the settle.
	var ticks int
	if err := owner(t).QueryRow(ctx, `select count(*) from public.fuel_prices where observed_at = $1`, refreshAt).Scan(&ticks); err != nil || ticks == 0 {
		t.Fatalf("no ticks written: %d %v", ticks, err)
	}

	// A refresh the key did not ask for, an id of none and an id that is
	// no UUID are not found.
	first := ask("fuel", "ags:09162000", ids[0])
	if _, err := s.Refresh(ctx, uuid.Must(uuid.NewV7()).String(), ids[0]); !errors.Is(err, refresh.ErrNotFound) {
		t.Fatalf("none: %v", err)
	}
	if _, err := s.Refresh(ctx, "not-a-uuid", ids[0]); !errors.Is(err, refresh.ErrNotFound) {
		t.Fatalf("no uuid: %v", err)
	}

	// Inside the freshness window a settled refresh answers a new ask, and
	// the new asker may read it; outside it a new refresh is made.
	again, err := s.Request(ctx, first, refreshAt.Add(-time.Minute), wait)
	if err != nil || again.State != refresh.Refreshed || again.ID == first.ID {
		t.Fatalf("fresh: %+v %v", again, err)
	}
	other := ask("fuel", "ags:09162000", ids[1])
	shared, err := s.Request(ctx, other, refreshAt.Add(-time.Minute), wait)
	if err != nil || shared.ID != again.ID || f.Claims() != 4 {
		t.Fatalf("shared fresh: %+v %v, %d claims", shared, err, f.Claims())
	}
	if _, err := s.Refresh(ctx, shared.ID, ids[1]); err != nil {
		t.Fatalf("the second asker reads it: %v", err)
	}
	newer, err := s.Request(ctx, ask("fuel", "ags:09162000", ids[1]), refreshAt.Add(time.Minute), wait)
	if err != nil || newer.ID == shared.ID || newer.State != refresh.Refreshed || f.Claims() != 5 {
		t.Fatalf("stale: %+v %v, %d claims", newer, err, f.Claims())
	}
}

func TestAnOpenRefreshAnswersPendingAtTheDeadline(t *testing.T) {
	s := open(t)
	f := fake(t)
	ids := askers(t, s, 1)
	ctx := bounded(t)
	// never: the fake leaves it pending, first in the queue of pending ones.
	n := ask("fuel", "ags:05315000", ids[0])
	r, err := s.Request(ctx, n, time.Time{}, 150*time.Millisecond)
	if err != nil || r.State != refresh.Pending || r.Position == nil || *r.Position < 1 || r.SettledAt != nil {
		t.Fatalf("never: %+v %v", r, err)
	}
	// slow: claimed, running, at position 0; it settles after 3 s.
	slow := ask("fuel", "ags:06412000", ids[0])
	r, err = s.Request(ctx, slow, time.Time{}, 300*time.Millisecond)
	if err != nil || r.State != refresh.Claimed || r.Position == nil || *r.Position != 0 {
		t.Fatalf("slow: %+v %v", r, err)
	}
	// No wait at all reads it as it stands.
	r, err = s.Request(ctx, ask("fuel", "ags:06412000", ids[0]), time.Time{}, 0)
	if err != nil || r.ID != slow.ID || r.State != refresh.Claimed || f.Claims() != 1 {
		t.Fatalf("no wait: %+v %v", r, err)
	}
	// A cancelled ask is an error, not an answer, whether it was cancelled
	// before it was recorded or while it waits.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Request(cctx, ask("fuel", "ags:05315000", ids[0]), time.Time{}, time.Second); err == nil {
		t.Fatal("a cancelled ask answered")
	}
	dctx, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stop()
	if _, err := s.Request(dctx, ask("fuel", "ags:05315000", ids[0]), time.Time{}, 5*time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an ask cancelled in its wait: %v", err)
	}
	// An asker of no key is refused by the store; an ask with no subject is
	// stored with an empty one.
	if _, err := s.Request(ctx, ask("fuel", "ags:05315000", "no-such-key"), time.Time{}, 0); err == nil {
		t.Fatal("an asker of no key was recorded")
	}
	bare := ask("train_board", "eva:8011160", ids[0])
	bare.Subject = nil
	if r, err := s.Request(ctx, bare, time.Time{}, 0); err != nil || r.State != refresh.Pending {
		t.Fatalf("no subject: %+v %v", r, err)
	}
}

// TestALostListenerIsOpenedAgain: the store's one settle listener, lost,
// leaves the waiting asks to their deadlines and is opened again, after
// which a settle answers its asks at once again.
func TestALostListenerIsOpenedAgain(t *testing.T) {
	s, err := accessstore.Open(context.Background(), cfg(t, "VFAPI_DB_CONNECT_TIMEOUT=200ms"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	f := fake(t)
	ids := askers(t, s, 1)
	ctx := bounded(t)
	r, err := s.Request(ctx, ask("fuel", "ags:05315000", ids[0]), time.Time{}, 200*time.Millisecond)
	if err != nil || r.State != refresh.Pending {
		t.Fatalf("never: %+v %v", r, err)
	}
	var lost int
	if err := owner(t).QueryRow(ctx, `select count(pg_terminate_backend(pid)) from pg_stat_activity
		where usename = 'api_access' and query like '%listen "refresh_%'`).Scan(&lost); err != nil || lost != 1 {
		t.Fatalf("%d listeners ended: %v", lost, err)
	}
	start := time.Now()
	r, err = s.Request(ctx, ask("train_board", "eva:8000209", ids[0]), time.Time{}, 5*time.Second)
	if err != nil || r.State != refresh.Refreshed || time.Since(start) > 3*time.Second || f.Claims() != 1 {
		t.Fatalf("after the loss: %+v %v after %s, %d claims", r, err, time.Since(start), f.Claims())
	}
}

// TestConcurrentAsksShareOneRefresh: N people asking about one subject at
// once, while its refresh runs (the script settles it 500 ms after the
// claim), get one request, one claim and N identical answers; another
// subject gets its own.
func TestConcurrentAsksShareOneRefresh(t *testing.T) {
	s := open(t)
	f := fake(t)
	const n = 12
	ids := askers(t, s, n)
	ctx := bounded(t)
	answers := make([]refresh.Request, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			answers[i], errs[i] = s.Request(ctx, ask("train_board", "eva:8000207", ids[i]), time.Time{}, 1200*time.Millisecond)
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || answers[i].ID != answers[0].ID || answers[i].State != refresh.Refreshed ||
			!answers[i].SettledAt.Equal(*answers[0].SettledAt) {
			t.Fatalf("asker %d: %+v %v; first %+v", i, answers[i], errs[i], answers[0])
		}
	}
	if f.Claims() != 1 {
		t.Fatalf("%d claims for one subject", f.Claims())
	}
	var rows, linked int
	o := owner(t)
	if err := o.QueryRow(ctx, `select count(*) from live.refresh_request where subject_key = 'eva:8000207'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("%d rows %v", rows, err)
	}
	if err := o.QueryRow(ctx, `select count(*) from live.refresh_asker where request_id = $1`, answers[0].ID).Scan(&linked); err != nil || linked != n {
		t.Fatalf("%d askers %v", linked, err)
	}
	for i := range n {
		if _, err := s.Refresh(ctx, answers[0].ID, ids[i]); err != nil {
			t.Fatalf("asker %d reads it: %v", i, err)
		}
	}
	other, err := s.Request(ctx, ask("train_board", "eva:8000096", ids[0]), time.Time{}, 1200*time.Millisecond)
	if err != nil || other.ID == answers[0].ID || f.Claims() != 2 {
		t.Fatalf("another subject: %+v %v, %d claims", other, err, f.Claims())
	}
}

func TestPruneRefreshesKeepsThirtyDays(t *testing.T) {
	s := open(t)
	ctx := bounded(t)
	if _, err := s.PruneRefreshes(ctx, time.Now().Add(-time.Hour)); err == nil {
		t.Fatal("a prune of younger refreshes ran")
	}
	if _, err := s.PruneRefreshes(ctx, time.Now().Add(-accessstore.RefreshRetention-time.Hour)); err != nil {
		t.Fatal(err)
	}
}
