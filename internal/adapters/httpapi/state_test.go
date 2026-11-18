package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// statesFake is what the fake answers for one Land's sections.
type statesFake struct {
	warnings []weather.StateWarning
	alerts   features.StateAlertsAnswer
	meta     features.Answer[struct{}]
	gotLand  string
	gotLimit int
}

func (f *fakeFeatures) StateWarnings(_ context.Context, _ access.Record, land string) ([]weather.StateWarning, features.Answer[struct{}], error) {
	f.states.gotLand = land
	return f.states.warnings, f.states.meta, f.err
}

func (f *fakeFeatures) StateAlerts(_ context.Context, _ access.Record, land string, limit int) (features.StateAlertsAnswer, error) {
	f.states.gotLand, f.states.gotLimit = land, limit
	return f.states.alerts, f.err
}

func TestStateWeatherWarnings(t *testing.T) {
	f := everyAnswer()
	ev := "STURMBÖEN"
	until := time.Date(2026, 3, 30, 14, 0, 0, 0, time.FixedZone("x", 7200))
	f.states.warnings = []weather.StateWarning{{Event: &ev, Level: 3, Notices: 2, Cells: 5, Until: &until, Families: []string{"f"}}, {Level: 1, Notices: 1, Cells: 1}}
	f.states.meta = features.Answer[struct{}]{Families: []provenance.Family{fresh}, At: fixedNow}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/states/09/weather", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK || f.states.gotLand != "09" {
		t.Fatalf("status %d land %q: %s", w.Code, f.states.gotLand, body)
	}
	for _, want := range []string{`"state":"09"`, `{"event":"STURMBÖEN","level":3,"notices":2,"cells":5,"until":"2026-03-30T12:00:00Z"}`, `{"event":null,"level":1,"notices":1,"cells":1,"until":null}`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	f.states.warnings = nil
	if w := do(t, h, http.MethodGet, "/v1/states/09/weather", bearer); !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Errorf("none: %s", w.Body.String())
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/states/de/weather", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/states/09/weather", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/states/09/weather", bearer), 403, "source-not-permitted")
}

func TestStateServiceAlerts(t *testing.T) {
	f := everyAnswer()
	until := time.Date(2026, 3, 30, 14, 0, 0, 0, time.FixedZone("x", 3600))
	f.states.alerts = features.StateAlertsAnswer{
		Alerts: []transit.StateAlert{{Header: "Störung", Lines: []string{"S 1", "S 2"}, Until: &until, CarriedBy: []string{"sa", "sx"}}, {Header: "Streik", Lines: []string{}, CarriedBy: []string{"sa"}}},
		Total:  7, Meta: features.Answer[struct{}]{Families: []provenance.Family{fresh}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/states/05/service-alerts", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK || f.states.gotLand != "05" || f.states.gotLimit != 5 {
		t.Fatalf("status %d land %q limit %d: %s", w.Code, f.states.gotLand, f.states.gotLimit, body)
	}
	for _, want := range []string{`"state":"05"`, `"total":7`, `{"header":"Störung","lines":["S 1","S 2"],"until":"2026-03-30T13:00:00Z","sourceFamilies":["sa","sx"]}`, `{"header":"Streik","lines":[],"until":null,"sourceFamilies":["sa"]}`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	if do(t, h, http.MethodGet, "/v1/states/05/service-alerts?limit=20", bearer); f.states.gotLimit != 20 {
		t.Errorf("limit %d", f.states.gotLimit)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/states/5/service-alerts", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/states/05/service-alerts", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/states/05/service-alerts", bearer), 403, "source-not-permitted")
}

// TestStatePathsTakeTheSixteenLaender (#152): a code that is no Land (00,
// 17, 99) is refused on every state operation with a 400 naming path.state,
// never a believable all-clear; 01 and 16, the first and the last Land, are
// answered.
func TestStatePathsTakeTheSixteenLaender(t *testing.T) {
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, op := range []string{"weather", "road-conditions", "service-alerts"} {
		for _, code := range []string{"00", "17", "99", "1", "016"} {
			w := do(t, h, http.MethodGet, "/v1/states/"+code+"/"+op, bearer)
			if assertProblem(t, w, http.StatusBadRequest, "invalid-parameter"); !strings.Contains(w.Body.String(), `"parameter":"path.state"`) {
				t.Errorf("%s %s: %s", op, code, w.Body)
			}
		}
		for _, code := range []string{"01", "16"} {
			if w := do(t, h, http.MethodGet, "/v1/states/"+code+"/"+op, bearer); w.Code != http.StatusOK {
				t.Errorf("%s %s: %d %s", op, code, w.Code, w.Body)
			}
		}
	}
}
