package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type fakeReadiness struct {
	result health.Result
	calls  atomic.Int32
}

func (f *fakeReadiness) Check(context.Context) health.Result {
	f.calls.Add(1)
	return f.result
}

var fixedNow = time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)

func newHandler(t *testing.T, cfg *config.Config, ready httpapi.Readiness) http.Handler {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config:          cfg,
		Readiness:       ready,
		Build:           buildinfo.Info{Version: "0.2.0-test", Commit: "abc1234"},
		ContractVersion: "0.1",
		Now:             func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(t *testing.T, h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	for k, vs := range header {
		r.Header[k] = vs
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, w.Body.String())
	}
	return m
}

func TestHealthzNeverTouchesStorage(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	ready := &fakeReadiness{}
	w := do(t, newHandler(t, config.Defaults(), ready), http.MethodGet, "/healthz", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if ready.calls.Load() != 0 {
		t.Fatal("/healthz consulted storage")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
}

func TestReadyzReportsChecks(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	ok := &fakeReadiness{result: health.Result{Ready: true}}
	w := do(t, newHandler(t, config.Defaults(), ok), http.MethodGet, "/readyz", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"ready"}` {
		t.Fatalf("ready: got %d %s", w.Code, w.Body.String())
	}
	bad := &fakeReadiness{result: health.Result{Checks: map[string]string{"storage": "pass", "contract": "fail"}}}
	w = do(t, newHandler(t, config.Defaults(), bad), http.MethodGet, "/readyz", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("not ready: got %d", w.Code)
	}
	body := decode(t, w)
	if body["status"] != "not_ready" || body["checks"].(map[string]any)["contract"] != "fail" {
		t.Fatalf("not ready body: %s", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("readiness answer is cacheable")
	}
}

func TestMetaEnvelopeAndVersions(t *testing.T) {
	req.Covers(t, "FR-SVC-003", "ENV-001", "ENV-002")
	h := newHandler(t, config.Defaults(), nil)
	w := do(t, h, http.MethodGet, "/v1/meta", http.Header{"X-Request-Id": {"client-req-0001"}})
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	body := decode(t, w)
	if len(body) != 3 || body["data"] == nil || body["meta"] == nil || body["links"] == nil {
		t.Fatalf("top level must be exactly data, meta, links: %s", w.Body.String())
	}
	data := body["data"].(map[string]any)
	if data["apiVersion"] != "0.2.0-test" || data["buildSha"] != "abc1234" || data["contractVersion"] != "0.1" {
		t.Fatalf("versions: %v", data)
	}
	if _, present := data["platformVersion"]; !present {
		t.Fatal("platformVersion must be present (null until version sync)")
	}
	meta := body["meta"].(map[string]any)
	if meta["requestId"] != "client-req-0001" || w.Header().Get("X-Request-Id") != "client-req-0001" {
		t.Fatalf("request id not carried: %v / %q", meta["requestId"], w.Header().Get("X-Request-Id"))
	}
	if meta["generatedAt"] != "2026-03-29T01:30:00Z" || meta["language"] != "en" {
		t.Fatalf("meta: %v", meta)
	}
	if body["links"].(map[string]any)["self"] != "https://api.viafrei.de/v1/meta" {
		t.Fatalf("links: %v", body["links"])
	}
}

func TestMetaCarriesNoSecretOrAddress(t *testing.T) {
	req.Covers(t, "FR-SVC-003", "CFG-007")
	const planted = "planted-meta-secret-91c2"
	cfg, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_URL=postgres://api_reader:" + planted + "@storage-host.invalid:6543/x",
		"VFAPI_SERVER_LISTEN_ADDR=listener.invalid:8080",
	}})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, newHandler(t, cfg, nil), http.MethodGet, "/v1/meta", nil)
	for _, leak := range []string{planted, "storage-host", "6543", "listener.invalid", "api_reader"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("/v1/meta carries %q", leak)
		}
	}
}

func TestGeneratedRequestID(t *testing.T) {
	req.Covers(t, "ENV-002")
	w := do(t, newHandler(t, config.Defaults(), nil), http.MethodGet, "/v1/meta", http.Header{"X-Request-Id": {"bad id with spaces"}})
	id := w.Header().Get("X-Request-Id")
	if len(id) != 36 || strings.Contains(id, " ") {
		t.Fatalf("a malformed client id must be replaced by a generated one, got %q", id)
	}
}

func assertProblem(t *testing.T, w *httptest.ResponseRecorder, status int, slug string) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != httpapi.ProblemContentType {
		t.Fatalf("Content-Type %q", ct)
	}
	p := decode(t, w)
	for _, member := range []string{"type", "title", "status", "detail", "instance", "requestId", "retryable"} {
		if _, ok := p[member]; !ok {
			t.Errorf("problem lacks %q: %s", member, w.Body.String())
		}
	}
	if p["type"] != "https://api.viafrei.de/problems/"+slug {
		t.Errorf("type = %v, want slug %s", p["type"], slug)
	}
	if int(p["status"].(float64)) != status {
		t.Errorf("status member %v", p["status"])
	}
	if p["requestId"] != w.Header().Get("X-Request-Id") {
		t.Errorf("requestId member differs from the header")
	}
	return p
}

func TestUnknownPathIsProblem(t *testing.T) {
	req.Covers(t, "ERR-001")
	w := do(t, newHandler(t, config.Defaults(), nil), http.MethodGet, "/v1/nothing-here?x=1", nil)
	p := assertProblem(t, w, http.StatusNotFound, "not-found")
	if p["instance"] != "/v1/nothing-here" {
		t.Errorf("instance must be the path without the query: %v", p["instance"])
	}
}

func TestWrongMethodIsProblem(t *testing.T) {
	req.Covers(t, "ERR-001")
	w := do(t, newHandler(t, config.Defaults(), nil), http.MethodDelete, "/v1/meta", nil)
	assertProblem(t, w, http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestURITooLong(t *testing.T) {
	req.Covers(t, "ERR-001")
	cfg := config.Defaults()
	w := do(t, newHandler(t, cfg, nil), http.MethodGet, "/v1/meta?pad="+strings.Repeat("a", cfg.Server.MaxURIBytes), nil)
	assertProblem(t, w, http.StatusRequestURITooLong, "uri-too-long")
}

func TestDisabledOperation(t *testing.T) {
	req.Covers(t, "REL-012")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_ENDPOINTS_DISABLED=getMeta"}})
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(t, cfg, nil)
	assertProblem(t, do(t, h, http.MethodGet, "/v1/meta", nil), http.StatusNotFound, "feature-disabled")
	doc := do(t, h, http.MethodGet, "/v1/openapi.json", nil).Body.String()
	if strings.Contains(doc, "getMeta") || strings.Contains(doc, "/v1/meta") {
		t.Fatal("a disabled operation is still in the OpenAPI document")
	}
	if !strings.Contains(doc, "getHealth") {
		t.Fatal("the OpenAPI document lost an enabled operation")
	}
}

func TestUnknownDisabledOperationRefused(t *testing.T) {
	req.Covers(t, "CFG-003")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_ENDPOINTS_DISABLED=getNothing"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := httpapi.NewHandler(httpapi.Deps{Config: cfg}); err == nil || !strings.Contains(err.Error(), "getNothing") {
		t.Fatalf("an unknown operation id must refuse the boot, got %v", err)
	}
}

func TestOpenAPIServedIsTheCommittedDocument(t *testing.T) {
	req.Covers(t, "FR-SVC-005")
	h := newHandler(t, config.Defaults(), nil)
	w := do(t, h, http.MethodGet, "/v1/openapi.json", nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != httpapi.OpenAPIMediaType {
		t.Fatalf("got %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	path := filepath.Join("..", "..", "..", "api", "openapi.json")
	if os.Getenv("UPDATE_GENERATED") == "1" {
		if err := os.WriteFile(path, w.Body.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(path) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatalf("api/openapi.json unreadable: %v (run: make generate)", err)
	}
	if !bytes.Equal(committed, w.Body.Bytes()) {
		t.Fatal("the served document differs from api/openapi.json; run: make generate")
	}
	var doc map[string]any
	if err := json.Unmarshal(committed, &doc); err != nil || doc["openapi"] != "3.1.0" {
		t.Fatalf("not an OpenAPI 3.1 document: %v", err)
	}
	if n := len(doc["paths"].(map[string]any)); n != len(httpapi.OperationIDs()) {
		t.Fatalf("document has %d paths, the service %d operations", n, len(httpapi.OperationIDs()))
	}
	red := do(t, h, http.MethodGet, "/openapi.json", nil)
	if red.Code != http.StatusFound || red.Header().Get("Location") != "/v1/openapi.json" {
		t.Fatalf("/openapi.json: %d %q", red.Code, red.Header().Get("Location"))
	}
}

func TestDocsSwitch(t *testing.T) {
	req.Covers(t, "CFG-006")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DOCS_ENABLED=false"}})
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, newHandler(t, cfg, nil), http.MethodGet, "/v1/openapi.json", nil)
	assertProblem(t, w, http.StatusNotFound, "not-found")
}

// TestEveryAnswerCarriesSecurityHeaders holds NFR-SEC-001 on a success, on a
// problem written by the router, on one written by a middleware before the
// router and on the OpenAPI document.
func TestEveryAnswerCarriesSecurityHeaders(t *testing.T) {
	req.Covers(t, "NFR-SEC-001")
	cfg := config.Defaults()
	h := newHandler(t, cfg, &fakeReadiness{})
	want := map[string]string{
		"Strict-Transport-Security":    "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":       "nosniff",
		"Content-Security-Policy":      "default-src 'none'; frame-ancestors 'none'",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Resource-Policy": "same-site",
	}
	for _, target := range []string{"/v1/meta", "/healthz", "/v1/openapi.json", "/nowhere", "/v1/meta?pad=" + strings.Repeat("a", cfg.Server.MaxURIBytes)} {
		w := do(t, h, http.MethodGet, target, nil)
		for name, value := range want {
			if got := w.Header().Get(name); got != value {
				t.Errorf("GET %.40s (%d): %s = %q, want %q", target, w.Code, name, got, value)
			}
		}
	}
}
