package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) FuelRefresh(_ context.Context, key access.Record, a energy.RefreshAsk) (energy.RefreshAnswer, error) {
	f.gotKey, f.gotRefresh = key, a
	return f.refresh, f.refreshErr
}

// TestAFuelRefreshIsRelayed (G8): the ask is handed over as the site sends
// it, and every answer relayed: the settling instant, the queue place in
// whole seconds rounded up, the reason; its faults are the problems the
// description names.
func TestAFuelRefreshIsRelayed(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001", "SEC-020")
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	settled := time.Date(2026, 3, 28, 11, 59, 30, 0, time.FixedZone("CET", 3600))
	f.refresh = energy.RefreshAnswer{State: energy.RefreshRefreshed, SettledAt: &settled}
	w := post(t, h, "/v1/fuel-refresh", `{"at":"pt:48.137,11.575","snap":true}`, bearer)
	data, _ := decode(t, w)["data"].(map[string]any)
	if w.Code != http.StatusOK || f.gotRefresh != (energy.RefreshAsk{At: "pt:48.137,11.575", Snap: true}) ||
		data["state"] != "refreshed" || data["settledAt"] != "2026-03-28T10:59:30Z" || data["queue"] != nil || data["reason"] != nil {
		t.Fatalf("status %d, asked %+v: %s", w.Code, f.gotRefresh, w.Body)
	}
	f.refresh = energy.RefreshAnswer{State: energy.RefreshPending, Queue: &energy.RefreshQueue{Position: 2, ETA: 4100 * time.Millisecond}}
	w = post(t, h, "/v1/fuel-refresh", `{"at":"ags:09162000","anchor":false}`, bearer)
	data, _ = decode(t, w)["data"].(map[string]any)
	if q, _ := data["queue"].(map[string]any); w.Code != http.StatusOK || q["position"] != 2.0 || q["etaSeconds"] != 5.0 || data["settledAt"] != nil {
		t.Errorf("pending: %s", w.Body)
	}
	f.refresh = energy.RefreshAnswer{State: energy.RefreshRefused, Reason: energy.ReasonNotConfigured}
	w = post(t, h, "/v1/fuel-refresh", `{"at":"ags:09162000"}`, bearer)
	if data, _ = decode(t, w)["data"].(map[string]any); data["reason"] != energy.ReasonNotConfigured || data["state"] != "refused" {
		t.Errorf("refused: %s", w.Body)
	}
	// The schema and Resolve refuse what the platform would not read.
	for body, field := range map[string]string{
		`{"at":"ags:123"}`: "body.at", `{"at":"pt:40.0,11.0"}`: "body.at", `{"at":"ags:09162000","snap":true,"anchor":true}`: "body.anchor",
	} {
		w := post(t, h, "/v1/fuel-refresh", body, bearer)
		if assertProblem(t, w, 400, "invalid-parameter"); !bytes.Contains(w.Body.Bytes(), []byte(field)) {
			t.Errorf("%s: field not named: %s", body, w.Body)
		}
	}
	f.refreshErr = energy.ErrRefreshArea
	w = post(t, h, "/v1/fuel-refresh", `{"at":"ags:09162000"}`, bearer)
	if assertProblem(t, w, 400, "invalid-parameter"); !bytes.Contains(w.Body.Bytes(), []byte("body.at")) {
		t.Errorf("unknown area: %s", w.Body)
	}
	f.refreshErr = features.ErrOwnKeyOnly
	assertProblem(t, post(t, h, "/v1/fuel-refresh", `{"at":"ags:09162000"}`, bearer), 403, "scope-denied")
	f.refreshErr = errors.New("refresher down")
	assertProblem(t, post(t, h, "/v1/fuel-refresh", `{"at":"ags:09162000"}`, bearer), 503, "upstream-unavailable")
	assertProblem(t, post(t, featureHandler(t, config.Defaults(), nil, nil), "/v1/fuel-refresh", `{"at":"ags:09162000"}`, bearer), 404, "feature-disabled")
}
