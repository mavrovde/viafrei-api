package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) ParkingBays(_ context.Context, key access.Record, q parking.BayQuery) (features.BaysAnswer, error) {
	f.gotKey, f.gotBays = key, q
	return f.bays, f.err
}

func TestParkingBaysAnswer(t *testing.T) {
	req.Covers(t, "FR-PARK-001")
	f := everyAnswer()
	seen := fixedNow.Add(-10 * time.Minute)
	two := 2
	f.bays = features.BaysAnswer{Bays: parking.Bays{Items: []parking.Bay{
		{ID: "b1", Kind: "disabled", Street: sp("Kuhstraße"), HouseNumber: sp("12"), City: sp("Dortmund"), Location: geo.Point{Lat: 51.51, Lon: 7.46},
			DistanceM: 40, State: sp("free"), MessageAt: &seen, Family: "src-alpha-parking"},
		{ID: "s1", Kind: "disabled", Location: geo.Point{Lat: 51.52, Lon: 7.47}, DistanceM: 300, Spaces: &two, ReservedWhen: sp("werktags"), Family: "src-alpha-parking"},
	}, Counts: parking.BayCounts{Total: 4, Sensors: 3, Free: 1, Occupied: 1, StaticSites: 1, StaticSpaces: 2}},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/parking-bays?at=51.51,7.46&radius_m=2000&limit=2&only_free=true", bearer)
	if w.Code != http.StatusOK || f.gotBays.Kind != "disabled" || f.gotBays.RadiusM != 2000 || f.gotBays.Limit != 2 || !f.gotBays.OnlyFree {
		t.Fatalf("status %d %+v: %s", w.Code, f.gotBays, w.Body)
	}
	body := decode(t, w)
	data, _ := body["data"].(map[string]any)
	counts, _ := data["counts"].(map[string]any)
	rows, _ := data["bays"].([]any)
	if len(rows) != 2 || counts["unknown"] != 1.0 || counts["total"] != 4.0 || counts["staticSpaces"] != 2.0 {
		t.Fatalf("data %v", data)
	}
	r0, _ := rows[0].(map[string]any)
	r1, _ := rows[1].(map[string]any)
	if r0["sensor"] != true || r0["state"] != "free" || r1["sensor"] != false || r1["spaces"] != 2.0 || r1["state"] != nil {
		t.Errorf("rows %v", rows)
	}
	if page, _ := body["meta"].(map[string]any)["page"].(map[string]any); page["hasMore"] != true {
		t.Errorf("page %v", page)
	}
	if do(t, h, http.MethodGet, "/v1/parking-bays?at=51.51,7.46&kind=ev_charging", bearer); f.gotBays.Kind != "ev_charging" || f.gotBays.RadiusM != 10000 ||
		f.gotBays.Limit != 5 || f.gotBays.OnlyFree {
		t.Errorf("defaults %+v", f.gotBays)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/parking-bays?at=51.51,7.46&kind=regular", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/parking-bays?at=51.51,7.46", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/parking-bays?at=51.51,7.46", bearer), 403, "source-not-permitted")
}
