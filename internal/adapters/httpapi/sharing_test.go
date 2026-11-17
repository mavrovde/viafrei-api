package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Sharing(_ context.Context, key access.Record, q transit.SharingQuery) (features.SharingAnswer, error) {
	f.gotKey, f.gotSharing = key, q
	return f.sharing, f.err
}

func TestSharingAnswer(t *testing.T) {
	req.Covers(t, "ENV-040")
	f := everyAnswer()
	seen := fixedNow
	electric := "electric"
	f.sharing = features.SharingAnswer{
		Stations: []transit.SharingStation{{ID: "nb:1", VehiclesAvailable: ip(2), StatusSeenAt: &seen, Family: "src-alpha-transit"}},
		InRadius: 3,
		Vehicles: []transit.SharingVehicle{{Propulsion: &electric}, {}, {}, {}},
		Meta:     features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/sharing-options?at=50.94,6.96&only_available=false&limit=1", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	if f.gotSharing.RadiusM != 1000 || f.gotSharing.OnlyAvailable || f.gotSharing.Limit != 1 {
		t.Errorf("asked %+v", f.gotSharing)
	}
	for _, want := range []string{`"available":true`, `"stationsInRadius":3`, `"count":4`, `"electric":1`, `"nearest":null`, `"hasMore":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	f.sharing = features.SharingAnswer{Nearest: &transit.SharingStation{ID: "nb:9"}, Meta: features.Answer[struct{}]{At: fixedNow}}
	w = do(t, h, http.MethodGet, "/v1/sharing-options?at=50.94,6.96", bearer)
	if body = w.Body.String(); w.Code != http.StatusOK || !strings.Contains(body, `"nearest":{"id":"nb:9"`) || !f.gotSharing.OnlyAvailable || f.gotSharing.Limit != 8 {
		t.Errorf("nearest: %d %s %+v", w.Code, body, f.gotSharing)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/sharing-options?at=50.94,6.96&radius_m=6000", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/sharing-options?at=50.94,6.96", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/sharing-options?at=50.94,6.96", bearer), 403, "source-not-permitted")
}
