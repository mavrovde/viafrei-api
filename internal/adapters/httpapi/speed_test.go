package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) SpeedCameras(_ context.Context, key access.Record, q roads.SpeedQuery) (features.SpeedAnswer, error) {
	f.gotKey, f.gotSpeed = key, q
	return f.speed, f.err
}

func TestSpeedCamerasAnswer(t *testing.T) {
	req.Covers(t, "ENV-040")
	f := everyAnswer()
	from := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	f.speed = features.SpeedAnswer{
		SpeedCameras: roads.SpeedCameras{
			Official: []roads.SpeedCamera{{ID: "se:1", Kind: "mobile_announced", Street: sp("Hauptstraße"), ValidFrom: &from,
				Location: &geo.Point{Lat: 50.9, Lon: 6.9}, Family: "src-alpha-roads"}},
			OfficialTotal: 2,
			OSM:           []roads.SpeedCamera{{ID: "osm:node1", Kind: "fixed", Family: "src-alpha-roads"}}, OSMTotal: 1,
		},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/speed-cameras?at=50.94,6.96&limit=1", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	if f.gotSpeed.RadiusM != 5000 || f.gotSpeed.Limit != 1 {
		t.Errorf("asked %+v", f.gotSpeed)
	}
	for _, want := range []string{`"firstDay":"2026-03-30"`, `"officialTotal":2`, `"id":"osm:node1"`, `"location":null`, `"hasMore":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	w = do(t, h, http.MethodGet, "/v1/speed-cameras?at=50.94,6.96&radius_m=25000", bearer)
	if w.Code != http.StatusOK || f.gotSpeed.Limit != 20 || f.gotSpeed.RadiusM != 25000 {
		t.Errorf("defaults: %d %+v", w.Code, f.gotSpeed)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/speed-cameras?at=50.94,6.96&radius_m=26000", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/speed-cameras?at=50.94,6.96", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/speed-cameras?at=50.94,6.96", bearer), 403, "source-not-permitted")
}

// An answer that holds no camera (#301: a place with only a webcam in
// reach) lists none in either block, credits no family, and still carries
// the § 23 Abs. 1c StVO notice as its condition.
func TestSpeedCamerasNoneHeld(t *testing.T) {
	req.Covers(t, "ENV-040")
	f := everyAnswer()
	f.speed = features.SpeedAnswer{Meta: features.Answer[struct{}]{At: fixedNow}, Notice: roads.SpeedNotice("de")}
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/speed-cameras?at=53.55,10.0", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	for _, want := range []string{`"official":[]`, `"officialTotal":0`, `"osm":[]`, `"osmTotal":0`, `"hasMore":false`, "§ 23 Abs. 1c StVO"} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	if strings.Contains(body, "hh_verkehrskameras") {
		t.Errorf("a webcam family credited: %s", body)
	}
}
