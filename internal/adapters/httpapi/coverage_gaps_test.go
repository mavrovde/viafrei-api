package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// The sources, a Land's road conditions and the lorry rules are refused with
// the features off and when the key may read none of their sources.
func TestSourcesStateConditionsAndLorryRefusals(t *testing.T) {
	off := featureHandler(t, config.Defaults(), nil, nil)
	none := featureHandler(t, config.Defaults(), &fakeFeatures{err: access.ErrNonePermitted}, nil)
	for _, target := range []string{"/v1/sources", "/v1/states/09/road-conditions", "/v1/lorry-rules?vehicle=lorry&at=51.2,6.98"} {
		assertProblem(t, do(t, off, http.MethodGet, target, bearer), 404, "feature-disabled")
		assertProblem(t, do(t, none, http.MethodGet, target, bearer), 403, "source-not-permitted")
	}
}

// One row past the limit is read to tell that more exist; it is not listed.
func TestLorryRulesTellMoreAndListTheLimit(t *testing.T) {
	f := everyAnswer()
	f.lorry = features.LorryAnswer{Rules: []roads.LorryRule{{ID: "1", Layer: "restriction", Family: "rd"}, {ID: "2", Layer: "restriction", Family: "rd"}}}
	body := decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/lorry-rules?vehicle=lorry&at=51.2,6.98&limit=1", bearer))
	if data, _ := body["data"].([]any); len(data) != 1 {
		t.Fatalf("%v", body)
	}
}

// A low-emission-zone read that fails fails the driving-rules answer.
func TestDrivingRulesRefuseWhenTheZoneReadFails(t *testing.T) {
	f := everyAnswer()
	f.topic = rules.Topic{Code: "lez", Title: "Umweltzone", ReviewedOn: fixedNow}
	f.zonesErr = access.ErrNonePermitted
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/driving-rules?topic=lez&at=52.5,13.4", bearer), 403, "source-not-permitted")
}

// A fuel answer read at another instant states the opening at that instant.
func TestFuelStationsStateTheOpeningAtTheAnswersInstant(t *testing.T) {
	f := everyAnswer()
	at := fixedNow.Add(3 * time.Hour)
	f.fuel = features.FuelStations{OpenAt: &at, Answer: features.Answer[energy.FuelStation]{
		Items: []energy.FuelStation{{ID: "f1", Family: "src-gamma-energy"}}, At: fixedNow}}
	if w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/fuel-options?at=50.1,8.6", bearer); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// A station's page and the stop boards are refused with the features off.
func TestStationPageAndStopBoardsRefusedWithTheFeaturesOff(t *testing.T) {
	off := featureHandler(t, config.Defaults(), nil, nil)
	for _, target := range []string{"/v1/stations/de:11000:900100003/page", "/v1/stop-boards?at=52.52,13.41"} {
		assertProblem(t, do(t, off, http.MethodGet, target, bearer), 404, "feature-disabled")
	}
}
