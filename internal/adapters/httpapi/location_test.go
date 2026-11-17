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

func (f *fakeFeatures) Location(_ context.Context, key access.Record, q places.LocationQuery) (features.LocationAnswer, error) {
	f.gotKey, f.gotLoc = key, q
	return f.location, f.err
}

func TestLocationAnswer(t *testing.T) {
	req.Covers(t, "ENV-040")
	f := everyAnswer()
	f.location = features.LocationAnswer{
		Location: places.Location{Address: &places.Address{ID: "osm:node1", DistanceM: ip(40)}, District: &places.Place{ID: "d1", Name: "Kreis", DistanceM: ip(9000)}},
		Meta:     features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK || f.gotLoc.At.Lat != 50.94 {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	for _, want := range []string{`"distanceM":40`, `"municipality":null`, `"name":"Kreis"`, `"state":null`, `"mayLieOutsideGermany":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	// A settlement is named by its name and point, without a code; a
	// position that may lie abroad says so.
	f.location = features.LocationAnswer{Location: places.Location{Municipality: &places.Place{ID: "m1", Name: "Ort", RegionCode: sp("01002"), DistanceM: ip(37900)}, MayLieAbroad: true}, Meta: features.Answer[struct{}]{At: fixedNow}}
	if body = do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer).Body.String(); !strings.Contains(body, `"municipality":{"id":"m1","name":"Ort","location":`) || strings.Contains(body, "01002") || !strings.Contains(body, `"mayLieOutsideGermany":true`) {
		t.Errorf("settlement: %s", body)
	}
	f.location = features.LocationAnswer{Location: places.Location{Address: &places.Address{ID: "osm:node2"}}, Meta: features.Answer[struct{}]{At: fixedNow}}
	if w = do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer); !strings.Contains(w.Body.String(), `"distanceM":0`) {
		t.Errorf("no distance: %s", w.Body.String())
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/location?at=50.94,6.96", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer), 403, "source-not-permitted")
}
