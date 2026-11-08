package httpapi_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/domain/limits"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// FuzzRequest (TEST-006, TST-016): the request parsers of the service — the
// strict query allow-list, the request-id header and the URI limit — run on
// every request. A fuzzed query string and X-Request-Id header on each
// operation are answered with a success or a 4xx problem, never a 5xx (the
// panic recovery turns a crash into a 500, so a crash fails here too), and
// within the request deadline (no hang).
// Seed corpus: testdata/fuzz/FuzzRequest (test/fuzz/targets.tsv).
func FuzzRequest(f *testing.F) {
	cfg := config.Defaults()
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config:          cfg,
		Readiness:       &fakeReadiness{result: health.Result{Ready: true}},
		ContractVersion: "0.1",
		Now:             func() time.Time { return fixedNow },
	})
	if err != nil {
		f.Fatal(err)
	}
	paths := []string{"/healthz", "/readyz", "/v1/meta", "/v1/openapi.json", "/v1/nope"}
	f.Add(uint8(0), "", "")
	f.Add(uint8(2), "lang=de", "client-req-0001")
	f.Add(uint8(2), "a=1&a=2&b", "\x00\xff")
	f.Add(uint8(1), "%zz;&=", strings.Repeat("r", 300))
	f.Add(uint8(3), strings.Repeat("q=1&", 2000), "")
	f.Add(uint8(4), "fields=a,b", "id with spaces")
	f.Fuzz(func(t *testing.T, which uint8, rawQuery, requestID string) {
		path := paths[int(which)%len(paths)]
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		r.URL.RawQuery = rawQuery
		r.RequestURI = path + "?" + rawQuery
		r.Header["X-Request-Id"] = []string{requestID}
		w := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(w, r)
		took := time.Since(start)
		if w.Code >= 500 || w.Code < 200 {
			t.Fatalf("GET %s?%q (request id %q): status %d %s", path, rawQuery, requestID, w.Code, w.Body.String())
		}
		if took > cfg.Deadline.Request+cfg.Deadline.Reserve {
			t.Fatalf("GET %s?%q took %s, over the deadline", path, rawQuery, took)
		}
		if w.Code >= 400 && !strings.HasPrefix(w.Header().Get("Content-Type"), httpapi.ProblemContentType) {
			t.Fatalf("GET %s?%q: %d without a problem body (%q)", path, rawQuery, w.Code, w.Header().Get("Content-Type"))
		}
	})
}

// clientRecorder is a per-address gate that admits every request and
// records the address it was asked about.
type clientRecorder struct{ last string }

func (c *clientRecorder) Take(client string) limits.Decision {
	c.last = client
	return limits.Decision{Allowed: true}
}

// FuzzForwarded (TEST-006, TST-016): the client address of a request from a
// trusted proxy is read from X-Forwarded-For. Whatever the header holds, the
// request is answered without a 5xx, and the address the per-address gate
// is charged with is either the proxy itself or a well-formed address.
// Seed corpus: testdata/fuzz/FuzzForwarded (test/fuzz/targets.tsv).
func FuzzForwarded(f *testing.F) {
	cfg := config.Defaults()
	cfg.Server.TrustedProxies = []string{"10.0.0.0/8"}
	gate := &clientRecorder{}
	h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, Addresses: gate})
	if err != nil {
		f.Fatal(err)
	}
	f.Add("10.0.0.1:5000", "192.0.2.7")
	f.Add("10.0.0.1:5000", "spoofed, 192.0.2.7 , 2001:db8::1")
	f.Add("198.51.100.1:5000", "192.0.2.7")
	f.Add("10.0.0.1", ",,,")
	f.Fuzz(func(t *testing.T, remote, forwarded string) {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
		r.RemoteAddr = remote
		r.Header["X-Forwarded-For"] = []string{forwarded}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code >= 500 {
			t.Fatalf("remote %q forwarded %q: status %d", remote, forwarded, w.Code)
		}
		host, _, splitErr := net.SplitHostPort(remote)
		if splitErr != nil {
			host = remote
		}
		if _, err := netip.ParseAddr(gate.last); gate.last != host && err != nil {
			t.Fatalf("remote %q forwarded %q: charged %q, neither the peer nor an address", remote, forwarded, gate.last)
		}
	})
}
