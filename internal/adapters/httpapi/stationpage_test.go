package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func pageAnswer() features.StationPage {
	at := fixedNow
	slug, land, line := "hamburg-hbf", "Hamburg", "RE1"
	secs := map[string]features.PageSection{}
	for _, id := range features.PageSections {
		secs[id] = features.PageSection{At: &at, Sources: []string{"src-alpha-transit"}}
	}
	secs["lifts"] = features.PageSection{}
	return features.StationPage{
		Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow},
		Record: transit.StationRecord{StationNo: "2514", Name: "Hamburg Hbf", Slug: &slug, Land: &land, At: &geo.Point{Lat: 53.55, Lon: 10.01},
			Flags: []string{"wifi"}},
		Sections: secs,
		Ampel: transit.RailAmpel{Lamp: "yellow", Trips: 3, Reasons: []transit.RailReason{{Kind: "alert", Lamp: "yellow", N: 1}},
			Lines: []transit.RailLine{{Line: "RE1", Lamp: "yellow", Trips: 3, Reasons: []transit.RailReason{{Kind: "alert", Lamp: "yellow", N: 1}}}}},
		StepFree:   []transit.StepFreePlatform{{Tracks: []string{"1"}, Lift: "ok"}},
		Blocked:    []transit.StepFreePlatform{},
		Lifts:      []transit.Facility{{ID: "e1", Kind: "elevator", State: "active"}},
		Platforms:  []transit.StationPlatforms{{StepFreeCounts: map[string]int{"yes": 1}, Platforms: []transit.Platform{{Tracks: []string{"1"}}}}},
		Departures: []transit.Departure{{JourneyID: "j", Line: &line, Mode: "rail", PlannedAt: fixedNow, Family: "src-alpha-transit"}},
		Alerts:     []transit.StationAlert{{Header: "Bauarbeiten", Lines: []string{"RE1"}, AtStation: true}},
	}
}

func TestStationPageAnswer(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001")
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/stations/8002549/page?lang=en", bearer)
	if w.Code != http.StatusOK || f.gotPage != (features.StationPageQuery{StationID: "8002549", Departures: 10}) {
		t.Fatalf("%d %+v: %s", w.Code, f.gotPage, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	secs, _ := data["sections"].([]any)
	if data["pageUrl"] != "https://viafrei.de/en/station/hamburg-hbf" || len(secs) != 8 {
		t.Fatalf("data %v", data)
	}
	ids := ""
	for _, s := range secs {
		ids += s.(map[string]any)["id"].(string) + " "
	}
	if ids != "header rail_ampel step_free_now lifts platforms departures service_alerts parking_charging " {
		t.Errorf("order %s", ids)
	}
	h0 := secs[0].(map[string]any)["header"].(map[string]any)
	l3 := secs[3].(map[string]any)
	if h0["land"] != "Hamburg" || l3["asOf"] != nil || secs[7].(map[string]any)["facilities"].(map[string]any)["location"] == nil {
		t.Errorf("sections %v", secs)
	}
	f.page.Record.Land = sp("Bayern")
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/stations/8002549/page?departures=50&lang=en", bearer))["data"].(map[string]any)
	if land := data["sections"].([]any)[0].(map[string]any)["header"].(map[string]any)["land"]; land != "Bavaria" || f.gotPage.Departures != 50 {
		t.Errorf("land %v", land)
	}
	f.page.Record.At = nil
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/stations/8002549/page", bearer))["data"].(map[string]any)
	if land := data["sections"].([]any)[0].(map[string]any)["header"].(map[string]any)["land"]; land != "Bayern" || data["pageUrl"] != "https://viafrei.de/de/station/hamburg-hbf" {
		t.Errorf("de %v", data)
	}
	for _, q := range []string{"departures=0", "departures=51"} {
		assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/8002549/page?"+q, bearer), 400, "invalid-parameter")
	}
	f.page.Record.Land = nil
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/stations/8002549/page?lang=en", bearer))["data"].(map[string]any)
	if land := data["sections"].([]any)[0].(map[string]any)["header"].(map[string]any)["land"]; land != nil {
		t.Errorf("no land %v", land)
	}
	f.pageErr = transit.ErrNotFound
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/x/page", bearer), 404, "not-found")
	f.pageErr = errFake
	if w := do(t, h, http.MethodGet, "/v1/stations/x/page", bearer); w.Code < 500 {
		t.Errorf("failure %d", w.Code)
	}
}
