package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/national"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// objectsFake holds the answers of the by-id and national reads.
type objectsFake struct {
	fuel     energy.FuelStationPage
	fuelMeta features.Answer[struct{}]
	gotID    string
	gotAge   time.Duration
	national features.NationalAnswer
}

func (f *fakeFeatures) FuelStation(_ context.Context, _ access.Record, id string, held time.Duration) (energy.FuelStationPage, features.Answer[struct{}], error) {
	f.objects.gotID, f.objects.gotAge = id, held
	return f.objects.fuel, f.objects.fuelMeta, f.err
}

func (f *fakeFeatures) ChargingSite(_ context.Context, _ access.Record, id string, age time.Duration) (features.Answer[energy.Site], error) {
	f.objects.gotID, f.objects.gotAge = id, age
	return f.charging, f.err
}

func TestFuelStationByID(t *testing.T) {
	yes := true
	f := everyAnswer()
	f.objects.fuel = energy.FuelStationPage{
		FuelStation: energy.FuelStation{ID: "fu:1", Name: sp("Tanke"), Family: "src-alpha-energy", Location: geo.Point{Lat: 52.5, Lon: 13.4},
			Hours:  energy.Hours{WholeDay: &yes},
			Prices: []energy.FuelPrice{{Product: "e10", Price: 1.789, ObservedAt: fixedNow.Add(-time.Minute)}, {Product: "diesel", Price: 1.659, ObservedAt: fixedNow.Add(-48 * time.Hour)}}},
		Address: energy.Address{Street: sp("Hauptstr."), HouseNumber: sp("1"), Postcode: sp("10115"), City: sp("Berlin")},
	}
	f.objects.fuelMeta = features.Answer[struct{}]{Families: []provenance.Family{fresh}, At: fixedNow}
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/fuel-options/fu:1", bearer)
	if w.Code != http.StatusOK || f.objects.gotID != "fu:1" {
		t.Fatalf("status %d, asked %q: %s", w.Code, f.objects.gotID, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	addr, _ := data["address"].(map[string]any)
	prices, _ := data["prices"].([]any)
	if data["openingState"] != "open" || data["aroundTheClock"] != true || addr["place"] != "Berlin" || addr["number"] != "1" || len(prices) != 2 {
		t.Fatalf("data %v", data)
	}
	if p0, _ := prices[0].(map[string]any); p0["current"] != true {
		t.Errorf("a price a minute old is current: %v", p0)
	}
	if p1, _ := prices[1].(map[string]any); p1["current"] != false {
		t.Errorf("a price two days old is held, not current: %v", p1)
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: energy.ErrNotFound}, nil), http.MethodGet, "/v1/fuel-options/nope", bearer), 404, "not-found")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/fuel-options/a%20b", bearer), 400, "invalid-parameter")
}

func TestChargingSiteByID(t *testing.T) {
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/charging-options/cs-01-0000", bearer)
	if w.Code != http.StatusOK || f.objects.gotID != "cs-01-0000" || f.objects.gotAge != config.Defaults().Charging.StatusMaxAge {
		t.Fatalf("status %d, asked %q %v: %s", w.Code, f.objects.gotID, f.objects.gotAge, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	if data["id"] != "cs-01-0000" || data["distanceM"] != 0.0 {
		t.Fatalf("data %v", data)
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: energy.ErrNotFound}, nil), http.MethodGet, "/v1/charging-options/nope", bearer), 404, "not-found")
}

type pressureFake bool

func (p pressureFake) Saturated() bool { return bool(p) }

func TestPressureSaysOneWord(t *testing.T) {
	for _, c := range []struct {
		p    httpapi.Pressure
		want string
	}{{nil, "ok"}, {pressureFake(false), "ok"}, {pressureFake(true), "saturated"}} {
		h, err := httpapi.NewHandler(httpapi.Deps{Config: config.Defaults(), ContractVersion: "0.1", Now: func() time.Time { return fixedNow }, Pressure: c.p})
		if err != nil {
			t.Fatal(err)
		}
		w := do(t, h, http.MethodGet, "/v1/pressure", nil)
		data, _ := decode(t, w)["data"].(map[string]any)
		if w.Code != http.StatusOK || data["state"] != c.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%v: %d %v %q", c.p, w.Code, data, w.Header().Get("Cache-Control"))
		}
	}
}

func (f *fakeFeatures) National(_ context.Context, _ access.Record, window time.Duration) (features.NationalAnswer, error) {
	f.objects.gotAge = window
	return f.objects.national, f.err
}

func TestNationalSummarySortsTheSections(t *testing.T) {
	two, late := 2, 5
	low, mid, delay := 1.599, 1.689, 240.0
	name := "Bayern"
	f := everyAnswer()
	f.objects.national = features.NationalAnswer{Rows: []national.Row{
		{Section: national.SectionSituations, Key: "closure", Family: "r", N: 3},
		{Section: national.SectionStates, Key: "09", Family: "r", N: 1},
		{Section: national.SectionFuel, Key: "e10", Family: "f", N: 4, Low: &low, Mid: &mid},
		{Section: national.SectionCharging, Key: "c", Family: "c", N: 2, M: &two},
		{Section: national.SectionParking, Key: "truck", Family: "p", N: 7},
		{Section: national.SectionFacilities, Key: "elevator/inactive", Family: "t", N: 2, M: &two},
		{Section: national.SectionPunctuality, Key: "state:09", Name: &name, Family: "t", N: 40, M: &late, Mid: &delay},
		{Section: national.SectionWarnings, Key: "2", Family: "w", N: 6, M: &two},
		{Section: "other", Key: "x", Family: "x"},
	}, Meta: features.Answer[struct{}]{Families: []provenance.Family{fresh}, At: fixedNow}}
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/germany", bearer)
	if w.Code != http.StatusOK || f.objects.gotAge != config.Defaults().Fuel.PriceFresh {
		t.Fatalf("status %d window %v: %s", w.Code, f.objects.gotAge, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	for k, n := range map[string]int{"situations": 1, "states": 1, "fuel": 1, "charging": 1, "parking": 1, "facilities": 1, "punctuality": 1, "warnings": 1} {
		if l, _ := data[k].([]any); len(l) != n {
			t.Errorf("%s: %v", k, data[k])
		}
	}
	fac := data["facilities"].([]any)[0].(map[string]any)
	pun := data["punctuality"].([]any)[0].(map[string]any)
	fuel := data["fuel"].([]any)[0].(map[string]any)
	if fac["kind"] != "elevator" || fac["state"] != "inactive" || pun["level"] != "state" || pun["code"] != "09" || pun["late"] != 5.0 || fuel["cheapestEur"] != 1.599 {
		t.Errorf("fields: %v %v %v", fac, pun, fuel)
	}
	f.objects.national = features.NationalAnswer{Rows: []national.Row{{Section: national.SectionFuel, Key: "e5", Family: "f"}, {Section: national.SectionCharging, Family: "c"}}, Meta: f.objects.national.Meta}
	w = do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/germany", bearer)
	data, _ = decode(t, w)["data"].(map[string]any)
	if fu := data["fuel"].([]any)[0].(map[string]any); fu["cheapestEur"] != 0.0 || data["charging"].([]any)[0].(map[string]any)["chargePoints"] != 0.0 {
		t.Errorf("a missing figure reads 0: %v", data)
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: access.ErrNonePermitted}, nil), http.MethodGet, "/v1/germany", bearer), 403, "source-not-permitted")
}
