package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Pois(_ context.Context, key access.Record, q places.PoiQuery) (features.PoiAnswer, error) {
	f.gotKey, f.gotPoi = key, q
	return f.pois, f.err
}

func TestPoiAnswer(t *testing.T) {
	req.Covers(t, "ENV-040")
	f := everyAnswer()
	f.pois = features.PoiAnswer{Pois: []places.Poi{{ID: "osm:node1", Name: sp("Adler Apotheke"), Category: "amenity", DistanceM: ip(120), Family: "src-alpha-places"}},
		Total: 12, Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/pois?name=apotheke&at=50.94,6.96&category=amenity&limit=1&cursor=10", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	if f.gotPoi.At == nil || f.gotPoi.RadiusM != 10000 || f.gotPoi.Offset != 10 || f.gotPoi.Category != "amenity" {
		t.Errorf("asked %+v", f.gotPoi)
	}
	for _, want := range []string{`"id":"osm:node1"`, `"distanceM":120`, `"nextCursor":"11"`, `"total":12`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	f.pois.Total = 1
	if w = do(t, h, http.MethodGet, "/v1/pois?name=apotheke&in=Fulda", bearer); w.Code != http.StatusOK || f.gotPoi.At != nil || f.gotPoi.Limit != 10 || !strings.Contains(w.Body.String(), `"nextCursor":null`) {
		t.Errorf("in a town: %d %s %+v", w.Code, w.Body.String(), f.gotPoi)
	}
	for _, bad := range []string{"name=x&in=Fulda", "name=apotheke&in=Fulda&at=50.9,6.9", "name=apotheke&cursor=50", "name=apotheke&at=99.5,6.9"} {
		assertProblem(t, do(t, h, http.MethodGet, "/v1/pois?"+bad, bearer), 400, "invalid-parameter")
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/pois?name=apotheke", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/pois?name=apotheke", bearer), 403, "source-not-permitted")
}
