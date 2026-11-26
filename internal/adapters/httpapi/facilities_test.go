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

func (f *fakeFeatures) Facilities(ctx context.Context, key access.Record, q transit.FacilityQuery) (features.StationFacilities, error) {
	f.gotKey, f.gotFacil = key, q
	f.gotLang, _ = features.LanguageOf(ctx)
	return f.facilities, f.err
}

func TestFacilitiesAnswer(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-003", "ENV-040")
	f := everyAnswer()
	seen := fixedNow
	f.facilities = features.StationFacilities{
		Answer: features.Answer[transit.Facility]{Items: []transit.Facility{
			{ID: "e1", Kind: "elevator", Position: sp("zu Gleis 1"), State: "inactive", ObservedAt: &seen, Family: "src-alpha-transit", EquipmentNo: sp("10500001")},
			{ID: "e2", Kind: "elevator", State: "unknown", Family: "src-alpha-transit"},
		}, HasMore: true, Families: []provenance.Family{transitFamily}, At: fixedNow},
		Station: transit.Station{ID: "sp-1", Name: "Köln Hbf"}, Active: 3, Inactive: 1, Unknown: 1,
		Platforms: []transit.StationPlatforms{{StationName: sp("Köln Hbf"), Station: &transit.PlatformFacts{StepFree: "yes", Wheelchair: "yes", TactileGuidance: "no", LevelBoarding: "unknown"},
			StepFreeCounts: map[string]int{"yes": 1, "unknown": 1}, Platforms: []transit.Platform{
				{Name: sp("Bahnsteig Gleis 1/2"), Tracks: []string{"1", "2"}, Facts: &transit.PlatformFacts{StepFree: "yes", Wheelchair: "no", TactileGuidance: "unknown", LevelBoarding: "partial"}, Height: &transit.HeightRange{Min: 55, Max: 76}},
				{Tracks: []string{}}}}},
		PlatformsAt: &seen,
	}
	// A publication without a stated date has no releasedOn.
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
	// The platform's facilities check states no report instant per unit and names the station only (docs/parity.md).
	if _, has := r0["observedAt"]; r0["state"] != "inactive" || has || r1["state"] != "unknown" {
		t.Errorf("rows %v", rows)
	}
	// The unit's DB equipment number, null when its source gives none.
	if v, has := r1["equipmentNo"]; r0["equipmentNo"] != "10500001" || !has || v != nil {
		t.Errorf("rows %v", rows)
	}
	pl, _ := data["platforms"].(map[string]any)
	pst, _ := pl["stations"].([]any)
	if len(pst) != 1 || pl["releasedOn"] == nil {
		t.Fatalf("platforms %v", pl)
	}
	p0, _ := pst[0].(map[string]any)
	list, _ := p0["list"].([]any)
	l0, _ := list[0].(map[string]any)
	l1, _ := list[1].(map[string]any)
	if p0["total"] != 2.0 || len(list) != 2 || l0["stated"] != true || l1["stated"] != false || l1["heightCm"] != nil ||
		l1["facts"].(map[string]any)["stepFree"] != "unknown" || p0["station"].(map[string]any)["tactileGuidance"] != "no" {
		t.Errorf("platforms %v", p0)
	}
	f.facilities.Platforms, f.facilities.PlatformsAt = nil, nil
	if data, _ := decode(t, do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities", bearer))["data"].(map[string]any); data["platforms"] != nil {
		t.Errorf("no platforms: %v", data["platforms"])
	}
	if st, _ := data["station"].(map[string]any); len(st) != 1 || st["name"] == nil {
		t.Errorf("station %v", data["station"])
	}
	if do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities?lang=en", bearer); f.gotLang != "en" {
		t.Errorf("lang not asked: %q", f.gotLang)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities?lang=fr", bearer), 400, "invalid-parameter")
	if do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities", bearer); f.gotFacil.Limit != 20 || f.gotLang != "" {
		t.Errorf("default %+v", f.gotFacil)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities?kind=ramp", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/stations/sp-1/facilities", bearer), 404, "feature-disabled")
	f.err = transit.ErrNotFound
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/sp-1/facilities", bearer), 404, "not-found")
}
