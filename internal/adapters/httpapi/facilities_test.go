package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Facilities(_ context.Context, key access.Record, q transit.FacilityQuery) (features.StationFacilities, error) {
	f.gotKey, f.gotFacil = key, q
	return f.facilities, f.err
}

func TestFacilitiesAnswer(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-003", "ENV-040")
	f := everyAnswer()
	seen := fixedNow
	f.facilities = features.StationFacilities{
		Answer: features.Answer[transit.Facility]{Items: []transit.Facility{
			{ID: "e1", Kind: "elevator", Position: sp("zu Gleis 1"), State: "inactive", ObservedAt: &seen, Family: "src-alpha-transit"},
			{ID: "e2", Kind: "elevator", State: "unknown", Family: "src-alpha-transit"},
		}, HasMore: true, Families: []provenance.Family{transitFamily}, At: fixedNow},
		Station: transit.Station{ID: "sp-1", Name: "Köln Hbf"}, Active: 3, Inactive: 1, Unknown: 1,
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities?kind=elevator&limit=2", bearer)
	if w.Code != http.StatusOK || f.gotFacil != (transit.FacilityQuery{StationID: "sp-1", Kind: "elevator", Limit: 2}) {
		t.Fatalf("status %d %+v: %s", w.Code, f.gotFacil, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	counts, _ := data["counts"].(map[string]any)
	rows, _ := data["facilities"].([]any)
	if len(rows) != 2 || counts["active"] != 3.0 {
		t.Fatalf("data %v", data)
	}
	r0, _ := rows[0].(map[string]any)
	r1, _ := rows[1].(map[string]any)
	if r0["state"] != "inactive" || r0["observedAtLocal"] == nil || r1["observedAt"] != nil {
		t.Errorf("rows %v", rows)
	}
	if do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities", bearer); f.gotFacil.Limit != 20 {
		t.Errorf("default %+v", f.gotFacil)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities?kind=ramp", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/stations/sp-1/facilities", bearer), 404, "feature-disabled")
	f.err = transit.ErrNotFound
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities", bearer), 404, "not-found")
}
