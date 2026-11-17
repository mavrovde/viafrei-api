package features_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// refresher records the asks it is given and answers one outcome.
type refresher struct {
	asked []energy.RefreshAsk
	ans   energy.RefreshAnswer
	err   error
}

func (r *refresher) Refresh(_ context.Context, a energy.RefreshAsk) (energy.RefreshAnswer, error) {
	r.asked = append(r.asked, a)
	return r.ans, r.err
}

// TestAFuelRefreshIsForwardedForOwnKeysOnly (G8): only a key of audience own
// may ask; an ask the platform would not read is refused before any
// forwarding; an instance with no refresher refuses with its reason; any
// other ask is forwarded and its answer, or its fault, relayed.
func TestAFuelRefreshIsForwardedForOwnKeysOnly(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001", "SEC-020")
	ctx := context.Background()
	s := features.New(features.Repositories{}, features.Settings{Language: "en"}, func() time.Time { return now })
	public := key("commercial", []access.Scope{"energy"})
	own := public
	own.Audience = access.AudienceOwn
	ask := energy.RefreshAsk{At: "ags:09162000"}
	if _, err := s.FuelRefresh(ctx, public, ask); !errors.Is(err, features.ErrOwnKeyOnly) {
		t.Fatalf("public key: %v", err)
	}
	a, err := s.FuelRefresh(ctx, own, ask)
	if err != nil || a.State != energy.RefreshRefused || a.Reason != energy.ReasonNotConfigured {
		t.Fatalf("no refresher: %+v, %v", a, err)
	}
	r := &refresher{ans: energy.RefreshAnswer{State: energy.RefreshPending, Queue: &energy.RefreshQueue{Position: 2, ETA: time.Second}}}
	s.WithFuelRefresher(r)
	if _, err := s.FuelRefresh(ctx, own, energy.RefreshAsk{At: "pt:1,1"}); !errors.Is(err, energy.ErrRefreshAt) || len(r.asked) != 0 {
		t.Fatalf("bad area: %v, asked %v", err, r.asked)
	}
	if a, err = s.FuelRefresh(ctx, own, ask); err != nil || a.State != energy.RefreshPending || a.Queue.Position != 2 || len(r.asked) != 1 || r.asked[0] != ask {
		t.Fatalf("forwarded: %+v, %v, asked %v", a, err, r.asked)
	}
	r.err = energy.ErrRefreshArea
	if _, err := s.FuelRefresh(ctx, own, ask); !errors.Is(err, energy.ErrRefreshArea) {
		t.Fatalf("refresher fault: %v", err)
	}
}
