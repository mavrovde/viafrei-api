package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

func (f *fakeFeatures) ResolvePlace(_ context.Context, key access.Record, text string) (features.ResolveAnswer, error) {
	f.gotKey, f.gotResolve = key, text
	return f.resolved, f.err
}

func TestResolvePlaceAnswer(t *testing.T) {
	f := &fakeFeatures{resolved: features.ResolveAnswer{Lang: "de", Meta: features.Answer[struct{}]{At: time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)}, Resolution: places.Resolution{
		Kind: places.ResolvedPoint, Lat: 54.9, Lon: 8.3, Label: "Sylt (Ort), auch: Sylt (Insel)", PlaceKind: "city", Via: "gazetteer", AGS: "01054168",
		Alternatives: []places.Option{{Label: "Sylt (Insel)", Kind: "island"}},
	}}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/places/resolve?q=Sylt", bearer)
	if w.Code != http.StatusOK || f.gotResolve != "Sylt" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d (%s): %s", w.Code, f.gotResolve, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	alts, _ := data["alternatives"].([]any)
	if data["name"] != "Sylt (Ort)" || data["districtCode"] != "01054168" || data["kind"] == "" || len(alts) != 1 {
		t.Errorf("data %v", data)
	}
	f.resolved.Resolution = places.Resolution{Kind: places.ResolvedPoint, Lat: 1, Lon: 1, Label: "Fulda-Nord (Anschlussstelle)", PlaceKind: "junction", Via: "poi"}
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/places/resolve?q=Fulda-Nord", bearer))["data"].(map[string]any)
	if data["districtCode"] != nil {
		t.Errorf("no key: %v", data)
	}
	f.resolved.Resolution = places.Resolution{Kind: places.ResolvedCandidates}
	if body := decode(t, do(t, h, http.MethodGet, "/v1/places/resolve?q=Neustadt", bearer)); body["data"] != nil {
		t.Errorf("no one place: %v", body["data"])
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: access.ErrNonePermitted}, nil), http.MethodGet, "/v1/places/resolve?q=Sylt", bearer), 403, "source-not-permitted")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/places/resolve?q=Sylt", bearer), 404, "feature-disabled")
}
