package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// countingGate is gates 2 and 3 counting the requests charged to a key.
type countingGate struct{ charged int }

func (g *countingGate) Admit(context.Context, string, ratelimit.Plan) ratelimit.Verdict {
	g.charged++
	return ratelimit.Verdict{}
}

// TestOnlyAuthenticatedRequestsCostAUnit: OPTIONS, the service endpoints and
// a request refused before authentication are never charged to a key; an
// authenticated request is (RATE-004, RATE-005).
func TestOnlyAuthenticatedRequestsCostAUnit(t *testing.T) {
	req.Covers(t, "RATE-004", "RATE-005")
	cfg := config.Defaults()
	gate := &countingGate{}
	refusing := &fakeAuth{err: auth.ErrUnknown}
	build := func(a httpapi.Authenticator) http.Handler {
		h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, Build: buildinfo.Info{Version: "t", Commit: "c"},
			ContractVersion: "0.1", Now: func() time.Time { return fixedNow }, Auth: a, Sources: &fakeSources{register: fixtureRegister}, Keys: gate})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	unknown := build(refusing)
	for _, r := range []struct{ method, path string }{
		{http.MethodOptions, "/v1/key"}, {http.MethodGet, "/healthz"}, {http.MethodGet, "/v1/meta"}, {http.MethodGet, "/v1/key"},
	} {
		do(t, unknown, r.method, r.path, bearer)
	}
	if gate.charged != 0 {
		t.Fatalf("%d units charged before authentication", gate.charged)
	}
	known := principal("consumer")
	known.Key.Plan.Rate, known.Key.Plan.Burst = 10, 10
	h := build(&fakeAuth{p: known})
	if w := do(t, h, http.MethodGet, "/v1/key", bearer); w.Code != http.StatusOK || gate.charged != 1 {
		t.Fatalf("an authenticated request: %d, %d charged", w.Code, gate.charged)
	}
}
