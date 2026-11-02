package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestReadyzWithoutChecksIsNotReady(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	for name, ready := range map[string]httpapi.Readiness{"no checker": nil, "no checks": &fakeReadiness{}} {
		w := do(t, newHandler(t, config.Defaults(), ready), http.MethodGet, "/readyz", nil)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"storage":"fail"`) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

// probeConfig points the healthcheck at srv.
func probeConfig(srv *httptest.Server) *config.Config {
	cfg := config.Defaults()
	cfg.Server.ListenAddr = strings.TrimPrefix(srv.URL, "http://")
	return cfg
}

func TestProbeVerdicts(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	ok := httptest.NewServer(newHandler(t, config.Defaults(), nil))
	defer ok.Close()
	if err := httpapi.Probe(context.Background(), probeConfig(ok)); err != nil {
		t.Fatalf("a healthy service failed the probe: %v", err)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer failing.Close()
	if err := httpapi.Probe(context.Background(), probeConfig(failing)); err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Errorf("a 500 passed the probe: %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	cfg := probeConfig(closed)
	closed.Close()
	if err := httpapi.Probe(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("a closed port passed the probe: %v", err)
	}
	for _, addr := range []string{"localhost", "localhost:%zz"} {
		cfg := config.Defaults()
		cfg.Server.ListenAddr = addr
		if err := httpapi.Probe(context.Background(), cfg); err == nil {
			t.Errorf("listen_addr %q passed the probe", addr)
		}
	}
}
