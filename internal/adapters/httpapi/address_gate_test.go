package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/limits"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// addressCounter admits every request and counts the ones charged.
type addressCounter struct{ charged int }

func (c *addressCounter) Take(string) limits.Decision {
	c.charged++
	return limits.Decision{Allowed: true}
}

func send(h http.Handler, target string, header http.Header) int {
	return sendFrom(h, "192.0.2.9:4000", target, header)
}

// sendFrom is send from the client address addr.
func sendFrom(h http.Handler, addr, target string, header http.Header) int {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	r.RemoteAddr = addr
	for k, vs := range header {
		r.Header[k] = vs
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// The service operations (liveness, readiness, the metadata, the pressure
// word) are never refused by gate 1, whatever the request carries: 100
// sequential calls from one address all answer (#155). A key header that
// is not valid buys no exemption anywhere else: an unknown path is charged
// by address (RATE-001, SEC-051).
//
// One handler serves every case, and each case comes from its own client
// address, so each starts with a full bucket: building a handler marshals
// the whole OpenAPI document, which made a handler per case the cost of
// this test (TST-012).
func TestServiceOperationsAreNeverRefusedByAddress(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	cfg := config.Defaults()
	burst := cfg.RateLimit.AnonBurst
	h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, Auth: &fakeAuth{err: access.ErrInvalid},
		Addresses: ratelimit.NewIPLimiter(wire.AddressSettings(cfg), nil)})
	if err != nil {
		t.Fatal(err)
	}
	client := 0
	for _, header := range []http.Header{nil, {"Authorization": {"x"}}, {httpapi.APIKeyHeader: {"x"}}} {
		for _, target := range []string{"/healthz", "/readyz", "/v1/meta", "/v1/pressure"} {
			client++
			addr := fmt.Sprintf("192.0.2.%d:4000", client)
			for i := range 100 {
				if code := sendFrom(h, addr, target, header); code == http.StatusTooManyRequests {
					t.Fatalf("%s %v: call %d refused by address", target, header, i)
				}
			}
		}
		client++
		addr := fmt.Sprintf("192.0.2.%d:4000", client)
		for i := range burst {
			if code := sendFrom(h, addr, "/no/such/path", header); code == http.StatusTooManyRequests {
				t.Fatalf("unknown path %v: request %d inside the burst refused", header, i)
			}
		}
		if code := sendFrom(h, addr, "/no/such/path", header); code != http.StatusTooManyRequests {
			t.Errorf("unknown path %v: past the burst answered %d, want 429", header, code)
		}
	}
}

// A key presented to an operation that checks it is governed by the key
// gates, not by address; so is a valid key presented where no key is
// checked (#151). Without an access store no operation checks a key, and
// a key that is not valid buys nothing: the address is charged there.
//
// One handler per access store, not per case: building a handler marshals
// the whole OpenAPI document (TST-012).
func TestAKeyPresentedToAKeyedOperationIsNotChargedByAddress(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	gate := &addressCounter{}
	handler := func(auth httpapi.Authenticator) http.Handler {
		d := httpapi.Deps{Config: config.Defaults(), Addresses: gate}
		if auth != nil {
			d.Auth = auth
		}
		h, err := httpapi.NewHandler(d)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	withStore, withoutStore := handler(&fakeAuth{p: principal("consumer")}), handler(nil)
	refusing := handler(&fakeAuth{err: access.ErrInvalid})
	for _, tc := range []struct {
		name    string
		h       http.Handler
		target  string
		header  http.Header
		charged int
	}{
		{"keyed, with a key", withStore, "/v1/key", bearer, 0},
		{"keyed, escaped path, with a key", withStore, "/v1/places/a%2Fb/overview", bearer, 0},
		{"keyed, without a key", withStore, "/v1/key", nil, 1},
		{"no access store, with a key", withoutStore, "/v1/key", bearer, 1},
		{"service, with a key", withStore, "/healthz", bearer, 0},
		{"service, without a key", withStore, "/readyz", nil, 0},
		{"unknown, with a key", withStore, "/v1/nothing", bearer, 0},
		{"unknown, without a key", withStore, "/v1/nothing", nil, 1},
		{"unknown, no access store, with a key", withoutStore, "/v1/nothing", bearer, 1},
		{"unknown, with a key that is not valid", refusing, "/v1/nothing", bearer, 1},
	} {
		gate.charged = 0
		send(tc.h, tc.target, tc.header)
		if gate.charged != tc.charged {
			t.Errorf("%s: charged %d, want %d", tc.name, gate.charged, tc.charged)
		}
	}
}

// A keyed operation that is switched off checks no key: a valid key's
// request counts against the key, one without a key against the address.
func TestADisabledKeyedOperationIsChargedByAddress(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	cfg := config.Defaults()
	cfg.Endpoints.Disabled = []string{"getKey"}
	gate := &addressCounter{}
	h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, Auth: &fakeAuth{p: principal("consumer")}, Addresses: gate})
	if err != nil {
		t.Fatal(err)
	}
	send(h, "/v1/key", bearer)
	send(h, "/v1/key", nil)
	if gate.charged != 1 {
		t.Fatalf("charged %d, want 1", gate.charged)
	}
}
