package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
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
		Location: places.Location{Address: &places.Address{ID: "osm:node1", DistanceM: ip(40)}, District: &places.Place{ID: "d1", Name: "Kreis", DistanceM: ip(9000)},
			Gemeinde: &places.Gemeinde{Name: "Köln", Code: "05315000", Inside: true, Kreis: sp("Köln"), Bezirk: sp("Köln"), Family: "f1"}},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK || f.gotLoc.At.Lat != 50.94 {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	for _, want := range []string{`"distanceM":40`, `"municipality":null`, `"name":"Kreis"`, `"state":null`, `"mayLieOutsideGermany":false`, `"lowEmissionZone":null`,
		`"gemeinde":{"name":"Köln","code":"05315000","inside":true,"kreis":"Köln","bezirk":"Köln","sourceFamily":"f1"},"governmentRegion":"Köln"`} {
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
	zone := rules.Zone{Key: "k1", Name: "Umweltzone Köln", DistanceM: 1200}
	road := rules.Zone{Key: "k2", Name: "A 4"}
	area := rules.Zone{Key: "k3", Name: "Freigabe"}
	read := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	changed := time.Date(2025, 6, 24, 0, 0, 0, 0, time.UTC)
	lifted := time.Date(2024, 2, 22, 0, 0, 0, 0, time.UTC)
	printed, green := "Umweltzone Hannover", "green"
	none := `"plaque":null,"liftedOn":null,"dataDate":null,"boundaryReadAt":null}`
	for _, c := range []struct {
		v    rules.Verdict
		a    *rules.ZoneAnswer
		want string
	}{
		{rules.Verdict{Kind: "inside", Zone: &zone, ExemptRoad: &road}, nil, `"lowEmissionZone":{"verdict":"inside","zone":"Umweltzone Köln","zoneKey":"k1","distanceM":null,"exemptRoad":"A 4","releasedArea":null,` + none},
		{rules.Verdict{Kind: "outside", Zone: &zone}, nil, `"lowEmissionZone":{"verdict":"outside","zone":"Umweltzone Köln","zoneKey":"k1","distanceM":1200,"exemptRoad":null,"releasedArea":null,` + none},
		{rules.Verdict{Kind: "transfer", Transfer: &area}, nil, `"lowEmissionZone":{"verdict":"transfer","zone":null,"zoneKey":null,"distanceM":null,"exemptRoad":null,"releasedArea":"Freigabe",` + none},
		{rules.Verdict{Kind: "unknown"}, &rules.ZoneAnswer{}, `"lowEmissionZone":{"verdict":"unknown","zone":null,"zoneKey":null,"distanceM":null,"exemptRoad":null,"releasedArea":null,` + none},
		{rules.Verdict{Kind: "inside", Zone: &zone}, &rules.ZoneAnswer{Name: &printed, Plaque: true, Entry: &rules.ListEntry{Name: "Hannover", Covered: true}, LiftedOn: &lifted, DataDate: &changed, BoundaryReadAt: &read},
			`"zone":"Umweltzone Hannover","zoneKey":"k1","distanceM":null,"exemptRoad":null,"releasedArea":null,"plaque":{"from":"curated_entry","entry":"Hannover","plakette":null,"covered":true},"liftedOn":"2024-02-22","dataDate":"2025-06-24","boundaryReadAt":"2026-11-16T08:00:00Z"}`},
		{rules.Verdict{Kind: "inside", Zone: &zone}, &rules.ZoneAnswer{Name: &printed, Plaque: true, Entry: &rules.ListEntry{Name: "München", Plaque: &green, Covered: true}, BoundaryReadAt: &read},
			`"plaque":{"from":"curated_entry","entry":"München","plakette":"green","covered":true},"liftedOn":null,"dataDate":null`},
		{rules.Verdict{Kind: "inside", Zone: &zone}, &rules.ZoneAnswer{Name: &printed, Plaque: true, BoundaryReadAt: &read},
			`"plaque":{"from":"national_fact","entry":null,"plakette":"green","covered":null}`},
	} {
		f.location = features.LocationAnswer{Zone: &c.v, ZoneAnswer: c.a, Meta: features.Answer[struct{}]{At: fixedNow}}
		if w = do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.v.Kind, w.Code, w.Body.String())
		}
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/location?at=50.94,6.96", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer), 403, "source-not-permitted")
}
