package conformance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
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
