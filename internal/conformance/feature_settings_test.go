package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/livesources"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
)

// everyScope admits every request with a key of every feature scope.
type everyScope struct{}

func (everyScope) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{Key: access.Record{Plan: access.Plan{Scopes: []access.Scope{"places", "energy", "parking"}}}}, nil
}

// askedFeatures records the query each feature operation was asked.
type askedFeatures struct {
	laterFeatures
	asked string
}

func (f *askedFeatures) Charging(_ context.Context, _ access.Record, q energy.Query) (features.Answer[energy.Site], error) {
	f.asked = fmt.Sprintf("radius=%d limit=%d", q.RadiusM, q.Limit)
	return features.Answer[energy.Site]{}, nil
}

func (f *askedFeatures) Parking(_ context.Context, _ access.Record, q parking.Query) (features.Answer[parking.Site], error) {
	f.asked = fmt.Sprintf("radius=%d limit=%d", q.RadiusM, q.Limit)
	return features.Answer[parking.Site]{}, nil
}

func (f *askedFeatures) Overview(_ context.Context, _ access.Record, q features.OverviewQuery) (features.Overview, error) {
	f.asked = fmt.Sprintf("radius=%d per_part=%d", q.RadiusM, q.PerPartLimit)
	return features.Overview{}, features.ErrNoPart
}

// featureAsk sends target to a handler with every feature scope and returns
// what the use case was asked.
func featureAsk(target string) func(t *testing.T, c *config.Config) string {
	return func(t *testing.T, c *config.Config) string {
		t.Helper()
		f := &askedFeatures{}
		h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: f})
		if err != nil {
			t.Fatal(err)
		}
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
		return f.asked
	}
}

func (f *askedFeatures) MotorwayConditions(_ context.Context, _ access.Record, q roads.CorridorQuery) (features.Conditions, error) {
	f.asked = fmt.Sprintf("limit=%d", q.Limit)
	return features.Conditions{}, roads.ErrUnknownRoad
}

func (f *askedFeatures) RoadConditions(_ context.Context, _ access.Record, q roads.AreaQuery) (features.Conditions, error) {
	f.asked = fmt.Sprintf("radius=%d limit=%d", q.RadiusM, q.Limit)
	return features.Conditions{}, roads.ErrUnknownRoad
}

func (f *askedFeatures) RoadSituation(_ context.Context, _ access.Record, id string) (features.Answer[roads.Situation], error) {
	f.asked = id
	return features.Answer[roads.Situation]{}, roads.ErrUnknownSituation
}

func (f *askedFeatures) WeatherRisk(_ context.Context, _ access.Record, q weather.Query) (features.WeatherRisk, error) {
	f.asked = fmt.Sprintf("min_level=%d", q.MinLevel)
	return features.WeatherRisk{}, access.ErrNonePermitted
}

func (f *askedFeatures) DrivingRules(_ context.Context, _ access.Record, topic string) (rules.Topic, features.Answer[rules.Rule], error) {
	f.asked = topic
	return rules.Topic{}, features.Answer[rules.Rule]{}, rules.ErrUnknownTopic
}

func (f *askedFeatures) Stations(_ context.Context, _ access.Record, q transit.Query) (features.Answer[transit.Station], error) {
	f.asked = fmt.Sprintf("radius=%d limit=%d", q.RadiusM, q.Limit)
	return features.Answer[transit.Station]{}, nil
}

func (f *askedFeatures) Departures(_ context.Context, _ access.Record, q transit.DepartureQuery) (features.Board, error) {
	f.asked = fmt.Sprintf("window=%s limit=%d", q.Window, q.Limit)
	return features.Board{}, nil
}

// tenMinutesOld answers one charging site whose one point was reported
// occupied ten minutes before the answer, by a family whose design threshold
// is an hour: only the status age limit can make it stale.
type tenMinutesOld struct{ askedFeatures }

func (*tenMinutesOld) Charging(context.Context, access.Record, energy.Query) (features.Answer[energy.Site], error) {
	at := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	site := energy.Site{ID: "cs-1", Family: "src-a", Points: []energy.ChargePoint{{ID: "cp-1", Availability: &energy.Availability{
		State: energy.StateOccupied, ObservedAt: at.Add(-10 * time.Minute), Family: "src-a",
	}}}}
	fam := provenance.Family{Source: access.Source{Family: "src-a"}, StaleAfter: time.Hour}
	return features.Answer[energy.Site]{Items: []energy.Site{site}, Families: []provenance.Family{fam}, At: at}, nil
}

// chargingState is the state the one point of tenMinutesOld is answered as.
func chargingState(t *testing.T, c *config.Config) string {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: &tenMinutesOld{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/charging-options?at=1,2", nil))
	var body struct {
		Data []struct {
			ChargePoints []struct {
				Availability struct{ State string } `json:"availability"`
			} `json:"chargePoints"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || len(body.Data[0].ChargePoints) != 1 {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
	return body.Data[0].ChargePoints[0].Availability.State
}

func (f *askedFeatures) FuelStations(_ context.Context, _ access.Record, q energy.FuelQuery) (features.FuelStations, error) {
	f.asked = q.Window.String()
	return features.FuelStations{}, nil
}

// fuelWindow is the price window a fuel search is asked under.
func fuelWindow(t *testing.T, c *config.Config) string {
	t.Helper()
	f := &askedFeatures{}
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: f})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/fuel-options?at=1,2", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return f.asked
}

func (f *askedFeatures) Places(_ context.Context, _ access.Record, q places.Query) (features.Answer[places.Place], error) {
	f.asked = fmt.Sprintf("limit=%d", q.Limit)
	return features.Answer[places.Place]{}, nil
}

func (f *askedFeatures) Facilities(_ context.Context, _ access.Record, q transit.FacilityQuery) (features.StationFacilities, error) {
	f.asked = fmt.Sprintf("limit=%d", q.Limit)
	return features.StationFacilities{}, nil
}

func (f *askedFeatures) HydrogenStations(_ context.Context, _ access.Record, q energy.HydrogenQuery) (features.HydrogenStations, error) {
	f.asked = fmt.Sprintf("limit=%d", q.Limit)
	return features.HydrogenStations{}, nil
}

// pricedFeatures answers a fuel search with one station whose one price was
// reported 45 minutes before the answer's instant.
type pricedFeatures struct{ askedFeatures }

func (*pricedFeatures) FuelStations(context.Context, access.Record, energy.FuelQuery) (features.FuelStations, error) {
	at := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	st := energy.FuelStation{ID: "s", Prices: []energy.FuelPrice{{Product: "e10", Price: 1.7, ObservedAt: at.Add(-45 * time.Minute)}}}
	a := features.FuelStations{At: at}
	a.Items = []energy.FuelStation{st}
	return a, nil
}

// fuelFresh says whether a price reported 45 minutes ago is stated current:
// not inside the default 30-minute window, current inside an hour's.
func fuelFresh(t *testing.T, c *config.Config) string {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: &pricedFeatures{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/fuel-options?at=1,2", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var body struct {
		Data []struct {
			Prices []struct {
				Current bool `json:"current"`
			} `json:"prices"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || len(body.Data[0].Prices) != 1 {
		t.Fatalf("%v %s", err, w.Body)
	}
	return fmt.Sprint(body.Data[0].Prices[0].Current)
}

// heldFeatures answers a filling station read by its id with the held
// window it was asked with, as the station's name.
type heldFeatures struct{ askedFeatures }

func (*heldFeatures) FuelStation(_ context.Context, _ access.Record, id string, held time.Duration) (energy.FuelStationPage, features.Answer[struct{}], error) {
	name := held.String()
	return energy.FuelStationPage{FuelStation: energy.FuelStation{ID: id, Name: &name, Family: "f"}}, features.Answer[struct{}]{}, nil
}

// fuelHeld is the held window a filling station read by its id is asked with.
func fuelHeld(t *testing.T, c *config.Config) string {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: &heldFeatures{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/fuel-options/x1", nil))
	var body struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %v %s", w.Code, err, w.Body)
	}
	return body.Data.Name
}

// refreshWait: how long a refresh ask waits for its refresh to settle.
func refreshWait(t *testing.T, c *config.Config) string {
	t.Helper()
	r, err := wire.Refreshes(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r.Wait.String()
}

// refreshSources: the live sources a refresh may name.
func refreshSources(t *testing.T, c *config.Config) string {
	t.Helper()
	r, err := wire.Refreshes(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(r.Sources.(*livesources.Registry).Names())
}
