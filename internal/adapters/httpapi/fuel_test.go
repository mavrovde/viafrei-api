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
	allDay, closed := true, false
	fam := provenance.Family{Source: access.Source{Family: "src-gamma-energy", Domain: "energy", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/f"}, StaleAfter: time.Hour}
	f.fuel = features.FuelStations{
		Answer: features.Answer[energy.FuelStation]{Items: []energy.FuelStation{
			{ID: "f1", Name: sp("ARAL Nord"), Brand: sp("ARAL"), Location: geo.Point{Lat: 50.1, Lon: 8.6}, DistanceM: 300, Open: "closed", Hours: energy.Hours{WholeDay: &allDay},
				Prices: []energy.FuelPrice{{Product: "e5", Price: 1.859, ObservedAt: fixedNow.Add(-10 * time.Minute), Open: &closed}, {Product: "diesel", Price: 1.7, ObservedAt: fixedNow.Add(-48 * time.Hour)}},
				Family: "src-gamma-energy"},
		}, HasMore: true, Families: []provenance.Family{fam}, At: fixedNow},
		Matched: 3, DroppedClosed: 1, DroppedUnknown: 2,
		OSMWithoutPrice: []energy.OSMFuelStation{{Name: "Tank Süd", Address: energy.Address{City: sp("Leipzig")}, Location: geo.Point{Lat: 50.2, Lon: 8.7}, DistanceM: 1260, Family: "src-gamma-energy"}},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6&radius_m=10000&fuel=e5&brand=aral&name=nord&open_now=true&sort=name&limit=1", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	// OpenStreetMap's stations without a price, beside the list
	if osm, _ := decode(t, w)["osmWithoutPrice"].([]any); len(osm) != 1 {
		t.Errorf("osmWithoutPrice %v", osm)
	} else if o0, _ := osm[0].(map[string]any); o0["name"] != "Tank Süd" || o0["distanceM"] != 1300.0 || o0["brand"] != nil {
		t.Errorf("osmWithoutPrice %v", o0)
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
	if p0, _ := prices[0].(map[string]any); len(prices) != 1 || p0["fuel"] != "e5" || s0["openingState"] != "closed" || f.gotFuel.Sort != "price" ||
		s0["openingObservedAt"] != fixedNow.Add(-10*time.Minute).UTC().Format(time.RFC3339) {
		t.Errorf("by price %v", s0)
	}
	// the asked grade's price stated no state
	f.fuel.Items[0].Prices[0].Open = nil
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6&fuel=e5&sort=price&limit=1", bearer))["data"].([]any)
	if s0, _ = data[0].(map[string]any); s0["openingState"] != "unknown" || s0["openingObservedAt"] != nil {
		t.Errorf("by price, no reported state: %v", s0)
	}
	// no price of the asked grade: no reported state
	f.fuel.Items[0].Prices = f.fuel.Items[0].Prices[1:]
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6&fuel=e5&sort=price&limit=1", bearer))["data"].([]any)
	if s0, _ = data[0].(map[string]any); s0["openingState"] != "unknown" || s0["openingObservedAt"] != nil {
		t.Errorf("by price, no price of the grade: %v", s0)
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

// The coverage is stated as the platform's fuel searches state it: held
// without a read when stations are listed, the read's state, stations and,
// on an empty answer, the nearest priced station; the stations without a
// current price, the read's under sort=price, else the listed ones'.
func TestFuelCoverageIsStated(t *testing.T) {
	req.Covers(t, "FR-ENERGY-002")
	f := everyAnswer()
	fresh, old := fixedNow.Add(-10*time.Minute), fixedNow.Add(-2*time.Hour)
	f.fuel = features.FuelStations{Answer: features.Answer[energy.FuelStation]{Items: []energy.FuelStation{
		{ID: "a", Family: "src-gamma-energy", Prices: []energy.FuelPrice{{Product: "e5", Price: 1.8, ObservedAt: fresh}}},
		{ID: "b", Family: "src-gamma-energy", Prices: []energy.FuelPrice{{Product: "e5", Price: 1.8, ObservedAt: old}, {Product: "diesel", Price: 1.7, ObservedAt: fresh}}},
		{ID: "c", Family: "src-gamma-energy"},
	}, At: fixedNow}}
	h := featureHandler(t, config.Defaults(), f, nil)
	query := func(target string) map[string]any {
		t.Helper()
		w := do(t, h, http.MethodGet, target, bearer)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, w.Code, w.Body)
		}
		q, _ := meta(t, decode(t, w))["query"].(map[string]any)
		return q
	}
	q := query("/v1/fuel-options?at=50.1,8.6&fuel=e5")
	if g := f.gotFuel; !g.Coverage || g.Fresh != 30*time.Minute {
		t.Errorf("asked %+v", g)
	}
	_, inRadius := q["stationsInRadius"]
	if q["areaCoverage"] != "held" || inRadius || q["stationsWithoutCurrentPrice"] != 2.0 {
		t.Errorf("listed, e5: %v", q)
	}
	if q = query("/v1/fuel-options?at=50.1,8.6"); q["stationsWithoutCurrentPrice"] != 1.0 {
		t.Errorf("listed, any grade: %v", q)
	}
	m := 6234.6
	f.fuel.Coverage = &energy.FuelCoverage{StationsInRadius: 7, NearestPricedM: &m, WithoutCurrent: 3}
	q = query("/v1/fuel-options?at=50.1,8.6&fuel=e5&sort=price")
	_, nearest := q["nearestPricedKm"]
	if q["areaCoverage"] != "held" || q["stationsInRadius"] != 7.0 || q["stationsWithoutCurrentPrice"] != 3.0 || nearest {
		t.Errorf("by price: %v", q)
	}
	f.fuel.Items, f.fuel.Coverage = nil, &energy.FuelCoverage{NearestPricedM: &m}
	if q = query("/v1/fuel-options?at=50.1,8.6"); q["areaCoverage"] != "priced_beyond" || q["stationsInRadius"] != 0.0 || q["nearestPricedKm"] != 6.235 || q["stationsWithoutCurrentPrice"] != 0.0 {
		t.Errorf("empty, priced beyond: %v", q)
	}
	f.fuel.Coverage = &energy.FuelCoverage{}
	_, nearest = query("/v1/fuel-options?at=50.1,8.6")["nearestPricedKm"]
	if q = query("/v1/fuel-options?at=50.1,8.6"); q["areaCoverage"] != "unknown" || nearest {
		t.Errorf("empty, unknown: %v", q)
	}
}

// TestFuelLimitBound: the contract's maximum (10) is answered, one beyond it
// is a 400 naming the parameter; the minimum likewise.
func TestFuelLimitBound(t *testing.T) {
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, c := range []struct {
		limit string
		code  int
	}{{"1", 200}, {"10", 200}, {"0", 400}, {"11", 400}, {"100", 400}} {
		w := do(t, h, http.MethodGet, "/v1/fuel-options?at=50.1,8.6&limit="+c.limit, bearer)
		if w.Code != c.code {
			t.Errorf("limit=%s: status %d, want %d: %s", c.limit, w.Code, c.code, w.Body)
		}
		if c.code == 400 && (!strings.Contains(w.Body.String(), "limit") || !strings.Contains(w.Body.String(), "10") && c.limit != "0") {
			t.Errorf("limit=%s: the problem names neither the parameter nor the maximum: %s", c.limit, w.Body)
		}
	}
}
