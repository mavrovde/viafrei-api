package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Nearby(_ context.Context, key access.Record, q features.NearbyQuery) (features.NearbyAnswer, error) {
	f.gotKey, f.gotNearby = key, q
	return f.nearby, f.err
}

func TestNearbyAnswer(t *testing.T) {
	req.Covers(t, "FR-BRIEF-005", "ENV-040")
	f := everyAnswer()
	ch, pk, st := chargingAnswer(), parkingAnswer(), stationsAnswer()
	f.nearby = features.NearbyAnswer{
		Fuel:      &features.FuelStations{Answer: features.Answer[energy.FuelStation]{Items: []energy.FuelStation{{ID: "fu:1", Family: "src-alpha-energy"}}, At: fixedNow}},
		Charging:  &ch,
		Parking:   &pk,
		Stations:  &st,
		Junctions: &features.Answer[places.Junction]{Items: []places.Junction{{ID: "j:1", Name: "Köln-Ost", Location: geo.Point{Lat: 50.9, Lon: 7}, Family: "src-alpha-places"}}},
		Needs: map[string]places.NeedPick{"food": {Need: "food", RadiusM: 3000, Widened: true,
			Places: []places.NeedPlace{{ID: "osm:1", Name: "Café", Kind: "amenity=cafe", DistanceM: 2500, Family: "src-alpha-places"}}}},
		Parts: []features.PartResult{{Part: "fuel", Status: features.StatusOK}, {Part: "needs", Status: features.StatusOK}},
		Meta:  features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow, Partial: true},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96&categories=fuel,safety,food,police", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	if !slices.Equal(f.gotNearby.Categories, []string{"fuel", "police", "toilets", "food"}) || f.gotNearby.At.RadiusM != 5000 {
		t.Errorf("asked %+v", f.gotNearby)
	}
	for _, want := range []string{`"id":"fu:1"`, `"id":"j:1"`, `"need":"food"`, `"radiusM":3000`, `"kind":"amenity=cafe"`, `"part":"needs"`, `"stations":[`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	f.nearby = features.NearbyAnswer{Junctions: &features.Answer[places.Junction]{}, Meta: features.Answer[struct{}]{At: fixedNow},
		Parts: []features.PartResult{{Part: "fuel", Status: features.StatusUnavailable, Err: errors.New("down")}, {Part: "junction", Status: features.StatusOK}}}
	w = do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96&radius_m=15000", bearer)
	if body = w.Body.String(); w.Code != http.StatusOK || !slices.Equal(f.gotNearby.Categories, features.VehicleCategories) ||
		f.gotNearby.At.RadiusM != 15000 || !strings.Contains(body, `"junctions":[]`) || !strings.Contains(body, "/problems/upstream-unavailable") {
		t.Errorf("defaults: %d %s %+v", w.Code, body, f.gotNearby)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96&radius_m=16000", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96&categories=pizza", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/nearby?at=50.94,6.96", bearer), 404, "feature-disabled")
	f.nearby, f.err = features.NearbyAnswer{Parts: []features.PartResult{{Part: "fuel", Status: features.StatusNotPermitted}}}, features.ErrNoPart
	assertProblem(t, do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96", bearer), 403, "source-not-permitted")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96", bearer), 403, "source-not-permitted")
}
