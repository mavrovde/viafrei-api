package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
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
	notOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"starting"}`)) }))
	defer notOK.Close()
	if err := httpapi.Probe(context.Background(), probeConfig(notOK)); err == nil || !strings.Contains(err.Error(), "status 200") {
		t.Errorf("a 200 that is not ok passed the probe: %v", err)
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

// The deployment check's half in the container: the running service's
// /v1/meta compared with the platform release and schema it must match.
func TestCheckVersions(t *testing.T) {
	req.Covers(t, "FR-SVC-003", "REL-001")
	synced, err := httpapi.NewHandler(httpapi.Deps{
		Config:          config.Defaults(),
		Build:           buildinfo.Info{Version: "2.5.21", Commit: "abc1234", Platform: "2.5.21"},
		ContractVersion: "0.1",
		Now:             func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	syncedSrv := httptest.NewServer(synced)
	defer syncedSrv.Close()
	unsyncedSrv := httptest.NewServer(newHandler(t, config.Defaults(), nil))
	defer unsyncedSrv.Close()
	ctx := context.Background()
	for _, want := range []httpapi.Versions{{Platform: "2.5.21", Contract: "0.1"}, {Contract: "0.1"}, {}} {
		if err := httpapi.CheckVersions(ctx, probeConfig(syncedSrv), want); err != nil {
			t.Errorf("%+v: %v", want, err)
		}
	}
	mismatches := map[string]struct {
		srv  *httptest.Server
		want httpapi.Versions
		says string
	}{
		"other platform": {syncedSrv, httpapi.Versions{Platform: "2.5.22"}, "platformVersion 2.5.21, want 2.5.22"},
		"other contract": {syncedSrv, httpapi.Versions{Contract: "0.2"}, "contractVersion 0.1, want 0.2"},
		"unsynced":       {unsyncedSrv, httpapi.Versions{Platform: "2.5.21", Contract: "1.0"}, "platformVersion null, want 2.5.21; contractVersion 0.1, want 1.0"},
	}
	for name, c := range mismatches {
		err := httpapi.CheckVersions(ctx, probeConfig(c.srv), c.want)
		if !errors.Is(err, httpapi.ErrVersionMismatch) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer failing.Close()
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<html>`)) }))
	defer garbled.Close()
	noPort := config.Defaults()
	noPort.Server.ListenAddr = "localhost"
	badPort := config.Defaults()
	badPort.Server.ListenAddr = "localhost:%zz"
	for name, cfg := range map[string]*config.Config{"503": probeConfig(failing), "not JSON": probeConfig(garbled), "no port": noPort, "bad port": badPort} {
		err := httpapi.CheckVersions(ctx, cfg, httpapi.Versions{Platform: "2.5.21"})
		if err == nil || errors.Is(err, httpapi.ErrVersionMismatch) {
			t.Errorf("%s: %v; an unreadable answer must fail and must not read as a mismatch", name, err)
		}
	}
}
