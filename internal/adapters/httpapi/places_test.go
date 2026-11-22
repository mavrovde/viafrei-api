package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Places(_ context.Context, key access.Record, q places.Query) (features.Answer[places.Place], error) {
	f.gotKey, f.gotPlaces = key, q
	return f.places, f.err
}

func TestPlacesAnswer(t *testing.T) {
	req.Covers(t, "FR-PLACE-001", "ENV-040")
	f := everyAnswer()
	fam := provenance.Family{Source: access.Source{Family: "src-zeta-places", Domain: "places", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/p"}, StaleAfter: time.Hour}
	f.places = features.Answer[places.Place]{Items: []places.Place{
		{ID: "p1", Name: "Neustadt", Kind: "municipality", Location: geo.Point{Lat: 53.5, Lon: 10.1}, RegionCode: sp("01055"), DistanceM: ip(1200), OfficialKey: sp("01055033"), NameEn: sp("Newtown"), Family: "src-zeta-places"},
	}, HasMore: true, Families: []provenance.Family{fam}, At: fixedNow}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/places?q=neustadt&kind=municipality&at=53.55,10&radius_m=50000&limit=1", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if g := f.gotPlaces; g.Text != "neustadt" || g.Kind != "municipality" || g.At == nil || *g.At != (geo.Point{Lat: 53.55, Lon: 10}) || g.RadiusM != 50000 || g.Limit != 1 {
		t.Errorf("asked %+v", g)
	}
	body := decode(t, w)
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data %v", body["data"])
	}
	p0, _ := data[0].(map[string]any)
	q, _ := meta(t, body)["query"].(map[string]any)
	if p0["id"] != "p1" || p0["districtCode"] != "01055" || p0["distanceM"] != 1200.0 || p0["ags"] != "01055033" || p0["nameEn"] != "Newtown" || q["kind"] != "municipality" || q["radiusM"] != 50000.0 {
		t.Errorf("place %v, query %v", p0, q)
	}
	// A name alone: no position, no radius, the default limit.
	f.places = features.Answer[places.Place]{}
	w = do(t, h, http.MethodGet, "/v1/places?q=neustadt&radius_m=5000", bearer)
	q, _ = meta(t, decode(t, w))["query"].(map[string]any)
	if g := f.gotPlaces; w.Code != http.StatusOK || g.At != nil || g.RadiusM != 0 || g.Limit != 5 || q["kind"] != nil || q["at"] != nil {
		t.Errorf("a name alone: %d %+v %v", w.Code, g, q)
	}
	for _, target := range []string{"/v1/places", "/v1/places?q=n", "/v1/places?q=nn&at=91,1", "/v1/places?q=nn&kind=city"} {
		assertProblem(t, do(t, h, http.MethodGet, target, bearer), 400, "invalid-parameter")
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/places?q=nn", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/places?q=nn", bearer), 403, "source-not-permitted")
}
