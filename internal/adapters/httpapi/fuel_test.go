package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) FuelStations(_ context.Context, key access.Record, q energy.FuelQuery) (features.FuelStations, error) {
	f.gotKey, f.gotFuel = key, q
	return f.fuel, f.err
}

func TestFuelStationsAnswer(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002", "ENV-040")
	f := everyAnswer()
	// The station reports closed, and its published hours have it open
	// around the clock: the answer states the hours' state, as the platform
	// does. A price is current inside fuel.price_fresh (30 minutes).
	allDay := true
	fam := provenance.Family{Source: access.Source{Family: "src-gamma-energy", Domain: "energy", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/f"}, StaleAfter: time.Hour}
	f.fuel = features.FuelStations{
		Answer: features.Answer[energy.FuelStation]{Items: []energy.FuelStation{
			{ID: "f1", Name: sp("ARAL Nord"), Brand: sp("ARAL"), Location: geo.Point{Lat: 50.1, Lon: 8.6}, DistanceM: 300, Open: "closed", Hours: energy.Hours{WholeDay: &allDay},
				Prices: []energy.FuelPrice{{Product: "e5", Price: 1.859, ObservedAt: fixedNow.Add(-10 * time.Minute)}, {Product: "diesel", Price: 1.7, ObservedAt: fixedNow.Add(-48 * time.Hour)}},
				Family: "src-gamma-energy"},
		}, HasMore: true, Families: []provenance.Family{fam}, At: fixedNow},
		Matched: 3, DroppedClosed: 1, DroppedUnknown: 2,
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6&radius_m=10000&fuel=e5&brand=aral&name=nord&open_now=true&sort=name&limit=1", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if g := f.gotFuel; g.At != (geo.Point{Lat: 50.1, Lon: 8.6}) || g.RadiusM != 10000 || g.Fuel != "e5" || g.Brand != "aral" || g.Name != "nord" ||
		!g.OpenNow || g.Sort != "name" || g.Limit != 1 {
		t.Errorf("asked %+v", g)
	}
	body := decode(t, w)
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data %v", body["data"])
	}
	s0, _ := data[0].(map[string]any)
	prices, _ := s0["prices"].([]any)
	p0, _ := prices[0].(map[string]any)
	p1, _ := prices[1].(map[string]any)
	if s0["id"] != "f1" || s0["openingState"] != "open" || s0["distanceM"] != 300.0 || p0["priceEur"] != 1.859 || p0["current"] != true || p1["current"] != false {
		t.Errorf("station %v", s0)
	}
	m := meta(t, body)
	q, _ := m["query"].(map[string]any)
	if q["matched"] != 3.0 || q["droppedClosed"] != 1.0 || q["droppedUnknown"] != 2.0 || q["sort"] != "name" {
		t.Errorf("query %v", q)
	}
	// By price, the station is stated as the platform's cheapest-fuel search
	// states it: the asked grade's price only, and the reported state.
	w = do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6&fuel=e5&sort=price&limit=1", bearer)
	data, _ = decode(t, w)["data"].([]any)
	s0, _ = data[0].(map[string]any)
	prices, _ = s0["prices"].([]any)
	if p0, _ := prices[0].(map[string]any); len(prices) != 1 || p0["fuel"] != "e5" || s0["openingState"] != "closed" || f.gotFuel.Sort != "price" {
		t.Errorf("by price %v", s0)
	}
	f.fuel.Items[0].Open = ""
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6&fuel=e5&sort=price&limit=1", bearer))["data"].([]any)
	if s0, _ = data[0].(map[string]any); s0["openingState"] != "unknown" {
		t.Errorf("by price, no reported state: %v", s0)
	}
	f.fuel = features.FuelStations{}
	if w = do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6", bearer); w.Code != http.StatusOK {
		t.Fatalf("defaults %d", w.Code)
	}
	if g := f.gotFuel; g.RadiusM != 5000 || g.Limit != 5 || g.Sort != "distance" {
		t.Errorf("defaults %+v", g)
	}
	for _, target := range []string{"/v1/fuel-options", "/v1/fuel-options?at=91,8", "/v1/fuel-options?at=50,8&sort=price", "/v1/fuel-options?at=50,8&radius_m=30000", "/v1/fuel-options?at=50,8&fuel=lpg"} {
		assertProblem(t, do(t, h, http.MethodGet, target, bearer), 400, "invalid-parameter")
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/fuel-options?at=50,8", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/fuel-options?at=50,8", bearer), 403, "source-not-permitted")
}

func TestFuelOpenAtAndWholeDay(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002", "ENV-040")
	f := everyAnswer()
	at := time.Date(2026, 3, 30, 21, 30, 0, 0, time.UTC)
	f.fuel.OpenAt, f.fuel.DroppedNotWholeDay = &at, 2
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/fuel-options?at=52.52,13.405&open_at=23:30&whole_day=true", bearer)
	if body := w.Body.String(); w.Code != http.StatusOK || f.gotFuel.OpenAt != "23:30" || !f.gotFuel.WholeDay ||
		!strings.Contains(body, `"droppedNotWholeDay":2`) || !strings.Contains(body, `"openAt":"2026-03-30T21:30:00Z"`) {
		t.Errorf("%d asked %+v: %s", w.Code, f.gotFuel, body)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/fuel-options?at=52.52,13.405&open_at=23:30&open_now=true", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, h, http.MethodGet, "/v1/fuel-options?at=52.52,13.405&open_at=25:00", bearer), 400, "invalid-parameter")
}
