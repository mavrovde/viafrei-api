package httpapi_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// publicHandler is the public listener's handler with callers as its
// allow-list, logging into logs.
func publicHandler(t *testing.T, callers config.Callers, logs *bytes.Buffer) http.Handler {
	t.Helper()
	cfg := config.Defaults()
	cfg.Callers = callers
	h, err := httpapi.NewPublicHandler(httpapi.Deps{
		Config:          cfg,
		ContractVersion: "0.1",
		Logger:          slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// ask GETs target on h from peer, with the given header values.
func ask(h http.Handler, peer, target string, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	r.RemoteAddr = peer
	for k, vs := range header {
		r.Header[k] = vs
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// refused holds a refusal to its shape (#92): 403, an empty body, and no
// header of the service, so it says nothing about which paths exist.
func refused(t *testing.T, name string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusForbidden || w.Body.Len() != 0 {
		t.Errorf("%s: %d %q, want 403 and no body", name, w.Code, w.Body.String())
	}
	for _, h := range []string{"Content-Type", httpapi.RequestIDHeader, "Strict-Transport-Security", "Content-Security-Policy", "Cache-Control", "RateLimit-Policy"} {
		if v := w.Header().Get(h); v != "" {
			t.Errorf("%s: the refusal carries %s: %s", name, h, v)
		}
	}
}

// TestAnEmptyAllowListRefusesEveryCaller (#92, criterion 1): with no
// callers.allow_cidr every route answers 403 with an empty body, the
// OpenAPI document, an unknown path and the health paths included, and the
// refusal is logged without the caller's address.
func TestAnEmptyAllowListRefusesEveryCaller(t *testing.T) {
	req.Covers(t, "SEC-001")
	var logs bytes.Buffer
	h := publicHandler(t, config.Callers{}, &logs)
	for _, target := range []string{"/v1/meta", "/v1/openapi.json", "/openapi.json", "/v1/charging-options?lat=52&lon=13", "/healthz", "/readyz", "/no/such/path"} {
		for _, peer := range []string{"127.0.0.1:4000", "[::1]:4000", "192.0.2.7:4000"} {
			refused(t, peer+" "+target, ask(h, peer, target, nil))
		}
	}
	if !strings.Contains(logs.String(), "caller refused") || strings.Contains(logs.String(), "192.0.2.7") {
		t.Fatalf("the refusal log line: %s", logs.String())
	}
}

// TestTheAllowListJudgesTheCaller (#92, criterion 2): an allowed address
// gets the normal answer and any other one 403, for a direct peer, for an
// address named by the trusted edge, and for a header forged by a peer
// outside the edge's ranges, which is judged by its socket address.
func TestTheAllowListJudgesTheCaller(t *testing.T) {
	req.Covers(t, "SEC-001")
	direct := config.Callers{AllowCIDR: []string{"198.51.100.0/24", "2001:db8::/32"}}
	edge := config.Callers{AllowCIDR: []string{"198.51.100.7/32"}, TrustedProxyHeader: "X-Real-IP", TrustedProxyCIDR: []string{"10.0.0.0/8"}}
	realIP := func(v ...string) http.Header { return http.Header{"X-Real-Ip": v} }
	for _, c := range []struct {
		name    string
		callers config.Callers
		peer    string
		header  http.Header
		allowed bool
	}{
		{"a direct peer inside the list", direct, "198.51.100.9:5000", nil, true},
		{"a direct IPv6 peer inside the list", direct, "[2001:db8::1]:5000", nil, true},
		{"a direct IPv4-mapped peer inside the list", direct, "[::ffff:198.51.100.9]:5000", nil, true},
		{"a direct peer outside the list", direct, "203.0.113.1:5000", nil, false},
		{"a peer that is no address", direct, "pipe", nil, false},
		{"a header with no proxy configured is not read", direct, "203.0.113.1:5000", realIP("198.51.100.9"), false},
		{"the edge names an allowed caller", edge, "10.1.2.3:5000", realIP("198.51.100.7"), true},
		{"the edge names a caller outside the list", edge, "10.1.2.3:5000", realIP("203.0.113.1"), false},
		{"the rightmost address is the one the edge wrote", edge, "10.1.2.3:5000", realIP("203.0.113.1, 198.51.100.7"), true},
		{"a client-written address left of the edge's is ignored", edge, "10.1.2.3:5000", realIP("198.51.100.7, 203.0.113.1"), false},
		{"the last header line is the edge's", edge, "10.1.2.3:5000", realIP("198.51.100.7", "203.0.113.1"), false},
		{"the edge sends a value that is no address", edge, "10.1.2.3:5000", realIP("unknown"), false},
		{"the edge sends no header: the edge itself is judged", edge, "10.1.2.3:5000", nil, false},
		{"a forged header from a peer outside the edge's ranges", edge, "203.0.113.1:5000", realIP("198.51.100.7"), false},
		{"the allowed address itself, direct, without the edge", edge, "198.51.100.7:5000", nil, true},
	} {
		w := ask(publicHandler(t, c.callers, &bytes.Buffer{}), c.peer, "/v1/meta", c.header)
		if !c.allowed {
			refused(t, c.name, w)
			continue
		}
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"contractVersion":"0.1"`) {
			t.Errorf("%s: %d %.120s, want the normal answer", c.name, w.Code, w.Body.String())
		}
	}
}

// TestTheHealthListenerHasNoAllowList (#92, criterion 3): liveness,
// readiness and the metadata answer every caller on the health listener,
// which serves nothing else; the public listener serves neither probe.
func TestTheHealthListenerHasNoAllowList(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	cfg := config.Defaults()
	h, err := httpapi.NewHealthHandler(httpapi.Deps{Config: cfg, ContractVersion: "0.1"})
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]int{
		"/healthz": http.StatusOK, "/readyz": http.StatusServiceUnavailable, "/v1/meta": http.StatusOK,
		"/v1/openapi.json": http.StatusNotFound, "/openapi.json": http.StatusNotFound, "/v1/key": http.StatusNotFound,
	} {
		if w := ask(h, "203.0.113.1:5000", target, nil); w.Code != want {
			t.Errorf("health %s: %d, want %d", target, w.Code, want)
		}
	}
	open := publicHandler(t, config.Callers{AllowCIDR: []string{"0.0.0.0/0", "::/0"}}, &bytes.Buffer{})
	// The public listener keeps them as deprecated aliases: the same answer,
	// with a Deprecation header.
	for target, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		if w := ask(open, "203.0.113.1:5000", target, nil); w.Code != want || w.Header().Get("Deprecation") != "true" {
			t.Errorf("public %s: %d, Deprecation %q; want %d, true", target, w.Code, w.Header().Get("Deprecation"), want)
		}
		if w := ask(h, "203.0.113.1:5000", target, nil); w.Header().Get("Deprecation") != "" {
			t.Errorf("health %s carries a Deprecation header", target)
		}
	}
}

// TestTheHealthServerListensOnItsOwnAddress: the health server is bound to
// health.listen_addr with the server.* limits.
func TestTheHealthServerListensOnItsOwnAddress(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	cfg := config.Defaults()
	cfg.Health.ListenAddr = "localhost:9097"
	s, err := httpapi.NewHealthServer(httpapi.Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != "localhost:9097" || s.ReadHeaderTimeout != cfg.Server.ReadHeaderTimeout || s.MaxHeaderBytes != cfg.Server.MaxHeaderBytes {
		t.Fatalf("health server %s %s %d", s.Addr, s.ReadHeaderTimeout, s.MaxHeaderBytes)
	}
	if _, err := httpapi.NewHealthServer(httpapi.Deps{}); err == nil {
		t.Fatal("a health server without configuration was built")
	}
	if _, err := httpapi.NewPublicHandler(httpapi.Deps{}); err == nil {
		t.Fatal("a public handler without configuration was built")
	}
}
