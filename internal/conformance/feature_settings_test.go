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
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// everyScope admits every request with a key of every feature scope.
type everyScope struct{}

func (everyScope) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{Key: access.Record{Plan: access.Plan{Scopes: []access.Scope{"places", "energy", "parking"}}}}, nil
}

// askedFeatures records the query each feature operation was asked.
type askedFeatures struct{ asked string }

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
