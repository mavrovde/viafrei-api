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
		Location: places.Location{Address: &places.Address{ID: "osm:node1", DistanceM: ip(40)}, District: &places.Place{ID: "d1", Name: "Kreis", DistanceM: ip(9000)}},
		Meta:     features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK || f.gotLoc.At.Lat != 50.94 {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	for _, want := range []string{`"id":"osm:node1"`, `"distanceM":40`, `"municipality":null`, `"name":"Kreis"`, `"state":null`, `"lowEmissionZone":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	f.location = features.LocationAnswer{Location: places.Location{Address: &places.Address{ID: "osm:node2"}}, Meta: features.Answer[struct{}]{At: fixedNow}}
	if w = do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer); !strings.Contains(w.Body.String(), `"distanceM":0`) {
		t.Errorf("no distance: %s", w.Body.String())
	}
	zone := rules.Zone{Key: "k1", Name: "Umweltzone Köln", DistanceM: 1200}
	road := rules.Zone{Key: "k2", Name: "A 4"}
	area := rules.Zone{Key: "k3", Name: "Freigabe"}
	for _, c := range []struct {
		v    rules.Verdict
		want string
	}{
		{rules.Verdict{Kind: "inside", Zone: &zone, ExemptRoad: &road}, `"lowEmissionZone":{"verdict":"inside","zone":"Umweltzone Köln","zoneKey":"k1","distanceM":null,"exemptRoad":"A 4","releasedArea":null}`},
		{rules.Verdict{Kind: "outside", Zone: &zone}, `"lowEmissionZone":{"verdict":"outside","zone":"Umweltzone Köln","zoneKey":"k1","distanceM":1200,"exemptRoad":null,"releasedArea":null}`},
		{rules.Verdict{Kind: "transfer", Transfer: &area}, `"lowEmissionZone":{"verdict":"transfer","zone":null,"zoneKey":null,"distanceM":null,"exemptRoad":null,"releasedArea":"Freigabe"}`},
		{rules.Verdict{Kind: "unknown"}, `"lowEmissionZone":{"verdict":"unknown","zone":null,"zoneKey":null,"distanceM":null,"exemptRoad":null,"releasedArea":null}`},
	} {
		f.location = features.LocationAnswer{Zone: &c.v, Meta: features.Answer[struct{}]{At: fixedNow}}
		if w = do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.v.Kind, w.Code, w.Body.String())
		}
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/location?at=50.94,6.96", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/location?at=50.94,6.96", bearer), 403, "source-not-permitted")
}
