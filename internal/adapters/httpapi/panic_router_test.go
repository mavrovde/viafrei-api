package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type panickingReadiness struct{}

func (panickingReadiness) Check(context.Context) health.Result { panic("planted fault") }

// TestPanicThroughTheRouterCarriesTheRequest: a panic inside an operation,
// served by the real router, answers a 500 problem that names the request
// (requestId, instance, absolute type), and the log line names it too.
func TestPanicThroughTheRouterCarriesTheRequest(t *testing.T) {
	req.Covers(t, "ERR-001")
	cfg := config.Defaults()
	cfg.Server.PublicBaseURL = "https://api.example.test"
	var logs bytes.Buffer
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config:    cfg,
		Readiness: panickingReadiness{},
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	const id = "panic-test-0001"
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	r.Header.Set(httpapi.RequestIDHeader, id)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var p httpapi.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("status %d, body %s: %v", w.Code, w.Body, err)
	}
	if w.Code != http.StatusInternalServerError || p.RequestID != id || p.Instance != "/readyz" ||
		p.Type != "https://api.example.test/problems/internal" || w.Header().Get(httpapi.RequestIDHeader) != id {
		t.Fatalf("status %d, problem %+v, header %q", w.Code, p, w.Header().Get(httpapi.RequestIDHeader))
	}
	if strings.Contains(w.Body.String(), "planted fault") {
		t.Fatal("the panic value reached the client")
	}
	var sawPanic, sawAccess bool
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		switch entry["msg"] {
		case "panic":
			sawPanic = entry["requestId"] == id && entry["panic"] == "planted fault"
		case "request":
			sawAccess = entry["requestId"] == id && entry["status"] == float64(http.StatusInternalServerError)
		}
	}
	if !sawPanic || !sawAccess {
		t.Fatalf("the panic or access line lacks the request id (panic %v, access %v):\n%s", sawPanic, sawAccess, logs.String())
	}
}
