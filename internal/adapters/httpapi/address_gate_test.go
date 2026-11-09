package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
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
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	r.RemoteAddr = "192.0.2.9:4000"
	for k, vs := range header {
		r.Header[k] = vs
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// A key header on an operation that checks no key buys no exemption from
// gate 1: every service operation and an unknown path is charged by
// address, whatever the request carries (RATE-001, SEC-051).
func TestAKeyHeaderOnAnOperationThatChecksNoKeyIsChargedByAddress(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	cfg := config.Defaults()
	burst := cfg.RateLimit.AnonBurst
	for _, header := range []http.Header{
		{"Authorization": {"x"}},
		{httpapi.APIKeyHeader: {"x"}},
	} {
		for _, target := range []string{"/healthz", "/readyz", "/v1/meta", "/no/such/path"} {
			h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, Auth: &fakeAuth{p: principal("consumer")},
				Addresses: ratelimit.NewIPLimiter(wire.AddressSettings(cfg), nil)})
			if err != nil {
				t.Fatal(err)
			}
			for i := range burst {
				if code := send(h, target, header); code == http.StatusTooManyRequests {
					t.Fatalf("%s %v: request %d inside the burst refused", target, header, i)
				}
			}
			if code := send(h, target, header); code != http.StatusTooManyRequests {
				t.Errorf("%s %v: past the burst answered %d, want 429", target, header, code)
			}
		}
	}
}

// A key presented to an operation that checks it is governed by the key
// gates, not by address; without an access store no operation checks a
// key, so the address is charged there too.
func TestAKeyPresentedToAKeyedOperationIsNotChargedByAddress(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	for _, tc := range []struct {
		name    string
		auth    httpapi.Authenticator
		target  string
		header  http.Header
		charged int
	}{
		{"keyed, with a key", &fakeAuth{p: principal("consumer")}, "/v1/key", bearer, 0},
		{"keyed, escaped path, with a key", &fakeAuth{p: principal("consumer")}, "/v1/places/a%2Fb/overview", bearer, 0},
		{"keyed, without a key", &fakeAuth{p: principal("consumer")}, "/v1/key", nil, 1},
		{"no access store, with a key", nil, "/v1/key", bearer, 1},
		{"service, with a key", &fakeAuth{p: principal("consumer")}, "/healthz", bearer, 1},
		{"unknown, with a key", &fakeAuth{p: principal("consumer")}, "/v1/nothing", bearer, 1},
	} {
		gate := &addressCounter{}
		d := httpapi.Deps{Config: config.Defaults(), Addresses: gate}
		if tc.auth != nil {
			d.Auth = tc.auth
		}
		h, err := httpapi.NewHandler(d)
		if err != nil {
			t.Fatal(err)
		}
		send(h, tc.target, tc.header)
		if gate.charged != tc.charged {
			t.Errorf("%s: charged %d, want %d", tc.name, gate.charged, tc.charged)
		}
	}
}

// A keyed operation that is switched off checks no key: it is charged by
// address.
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
	if gate.charged != 1 {
		t.Fatalf("charged %d, want 1", gate.charged)
	}
}
