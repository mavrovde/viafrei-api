package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) HydrogenStations(_ context.Context, key access.Record, q energy.HydrogenQuery) (features.HydrogenStations, error) {
	f.gotKey, f.gotHydro = key, q
	return f.hydrogen, f.err
}

func TestHydrogenAnswer(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001", "ENV-040")
	f := everyAnswer()
	f.hydrogen = features.HydrogenStations{
		Answer: features.Answer[energy.HydrogenStation]{Items: []energy.HydrogenStation{
			{ID: "h:1", Name: sp("H2 Köln"), Location: geo.Point{Lat: 50.9, Lon: 6.9}, DistanceM: 1200, Refill: json.RawMessage(`[{"solution":"700bar"}]`), Family: "src-alpha-energy"},
		}, HasMore: true, Families: []provenance.Family{transitFamily}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/hydrogen-options?at=50.94,6.96&vehicle=car&limit=1", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	if f.gotHydro.RadiusM != 50000 || f.gotHydro.Vehicle != "car" || f.gotHydro.Limit != 1 {
		t.Errorf("asked %+v", f.gotHydro)
	}
	for _, want := range []string{`"id":"h:1"`, `"refill":[{"solution":"700bar"}]`, `"nearest":null`, `"hasMore":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	f.hydrogen = features.HydrogenStations{Nearest: &energy.HydrogenStation{ID: "h:9", DistanceM: 90000, Family: "src-alpha-energy"},
		Answer: features.Answer[energy.HydrogenStation]{At: fixedNow}}
	w = do(t, h, http.MethodGet, "/v1/hydrogen-options?at=50.94,6.96", bearer)
	if body = w.Body.String(); w.Code != http.StatusOK || !strings.Contains(body, `"nearest":{"id":"h:9"`) || f.gotHydro.Limit != 5 {
		t.Errorf("nearest beyond: %d %s %+v", w.Code, body, f.gotHydro)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/hydrogen-options?at=50.94,6.96&vehicle=bus", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/hydrogen-options?at=50.94,6.96", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/hydrogen-options?at=50.94,6.96", bearer), 403, "source-not-permitted")
}
