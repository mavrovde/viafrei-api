package httpapi_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) WeatherRisk(_ context.Context, key access.Record, q weather.Query) (features.WeatherRisk, error) {
	f.gotKey, f.gotRisk = key, q
	return f.risk, f.err
}

func (f *fakeFeatures) WeatherCell(_ context.Context, key access.Record, target string, at *geo.Point, minLevel int) (features.CellAnswer, error) {
	f.gotKey, f.gotCell = key, weather.CellQuery{Cell: target, At: at, MinLevel: minLevel}
	return f.cell, f.cellErr
}

func (f *fakeFeatures) LowEmissionZones(_ context.Context, _ access.Record, q rules.ZoneQuery) (features.ZonesAnswer, error) {
	f.gotZones = &q
	return f.zones, f.zonesErr
}

func (f *fakeFeatures) DrivingRules(_ context.Context, key access.Record, topic string) (rules.Topic, features.Answer[rules.Rule], error) {
	f.gotKey, f.gotTopic = key, topic
	return f.topic, features.Answer[rules.Rule]{Items: f.topic.Rules, Families: []provenance.Family{rulesFamily}, At: fixedNow}, f.err
}

var weatherFamily = provenance.Family{
	Source:     access.Source{Family: "src-alpha-weather", Domain: "weather", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/w"},
	StaleAfter: 10 * time.Minute, DataAsOf: tp(fixedNow.Add(-time.Minute)),
}

var rulesFamily = provenance.Family{
	Source:     access.Source{Family: "src-alpha-rules", Domain: "rules", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/k"},
	StaleAfter: 24 * time.Hour, DataAsOf: tp(fixedNow.Add(-time.Hour)),
}

func riskAnswer() features.WeatherRisk {
	return features.WeatherRisk{
		Answer: features.Answer[weather.Warning]{Items: []weather.Warning{
			{ID: "w-1", Family: "src-alpha-weather", Hazard: "snow", Level: 3, LevelName: "Unwetter", Issuer: "Wetterdienst",
				Headline: "Schneefall", TextLanguage: "de", From: fixedNow.Add(-time.Hour), Until: tp(fixedNow.Add(26 * time.Hour)), AreaName: "Kreis A",
				CellID: sp("814626000"), Event: sp("FROST"), Identifier: sp("2.49.0.0.276.0.DWD.PVW.1"), Published: tp(fixedNow.Add(-2 * time.Hour))},
			{ID: "w-2", Family: "src-alpha-weather", Hazard: weather.Other, Level: 1, LevelName: "Wetterwarnung", Issuer: "Wetterdienst",
				Headline: "Hinweis", TextLanguage: "de", From: fixedNow, AreaName: "Kreis A"},
		}, HasMore: true, Families: []provenance.Family{weatherFamily}, At: fixedNow},
		Lamp: weather.LampRed,
	}
}

func topicAnswer() rules.Topic {
	return rules.Topic{
		Code: "winter_tyres", Title: "Winterreifen", ReviewedOn: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Rules: []rules.Rule{{Text: "Bei Glätte Winterreifen.", LegalBasis: sp("§ 2 Abs. 3a StVO")}, {Text: "Ohne Grundlage."}},
	}
}

func TestWeatherRiskAnswer(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001", "FR-WEATHER-002", "ENV-040")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/weather-risk?at=52.52,13.405&min_level=2&hazards=snow,other", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if f.gotRisk.At != (geo.Point{Lat: 52.52, Lon: 13.405}) || f.gotRisk.MinLevel != 2 || !slices.Equal(f.gotRisk.Hazards, []weather.Hazard{"snow", weather.Other}) {
		t.Errorf("asked %+v", f.gotRisk)
	}
	body := decode(t, w)
	data, _ := body["data"].(map[string]any)
	ws, _ := data["warnings"].([]any)
	if data["lamp"] != "red" || len(ws) != 2 || data["truncated"] != true {
		t.Fatalf("data %v", data)
	}
	w0, _ := ws[0].(map[string]any)
	v0, _ := w0["validity"].(map[string]any)
	if w0["id"] != "w-1" || w0["hazard"] != "snow" || w0["level"] != 3.0 || w0["levelName"] != "Unwetter" || w0["issuer"] != "Wetterdienst" ||
		w0["headline"] != "Schneefall" || w0["textLanguage"] != "de" || w0["sourceFamily"] != "src-alpha-weather" ||
		v0["fromLocal"] != "2026-03-29T01:30:00+01:00" || v0["untilLocal"] != "2026-03-30T05:30:00+02:00" {
		t.Errorf("first warning %v", w0)
	}
	if area, _ := w0["area"].(map[string]any); len(area) != 3 || area["name"] != "Kreis A" || area["geometry"] != nil || area["cellId"] != "814626000" {
		t.Errorf("area %v", w0["area"])
	}
	if w0["event"] != "FROST" || w0["identifier"] != "2.49.0.0.276.0.DWD.PVW.1" || w0["published"] != "2026-03-28T23:30:00Z" || w0["publishedLocal"] != "2026-03-29T00:30:00+01:00" {
		t.Errorf("issued %v %v %v %v", w0["event"], w0["identifier"], w0["published"], w0["publishedLocal"])
	}
	if rw, ok := data["roadWeather"].([]any); !ok || len(rw) != 0 {
		t.Errorf("roadWeather %v", data["roadWeather"])
	}
	w1, _ := ws[1].(map[string]any)
	v1, _ := w1["validity"].(map[string]any)
	if w1["hazard"] != "other" || v1["until"] != nil || w1["event"] != nil || w1["published"] != nil || w1["publishedLocal"] != nil {
		t.Errorf("second warning %v", w1)
	}
	q, _ := meta(t, body)["query"].(map[string]any)
	at, _ := q["at"].(map[string]any)
	if hz, _ := q["hazards"].([]any); len(hz) != 2 || q["minLevel"] != 2.0 || at["lat"] != 52.52 {
		t.Errorf("query %v", q)
	}

	f = everyAnswer()
	f.risk = features.WeatherRisk{Answer: features.Answer[weather.Warning]{At: fixedNow, Partial: true}, Lamp: weather.LampAmber}
	body = decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/weather-risk?at=52.52,13.405", bearer))
	data, _ = body["data"].(map[string]any)
	q, _ = meta(t, body)["query"].(map[string]any)
	if ws, _ := data["warnings"].([]any); data["lamp"] != "amber" || data["truncated"] != false || ws == nil || len(ws) != 0 || f.gotRisk.MinLevel != 0 || f.gotRisk.Hazards != nil {
		t.Errorf("defaults: data %v, asked %+v", data, f.gotRisk)
	}
	if hz, _ := q["hazards"].([]any); hz == nil || len(hz) != 0 {
		t.Errorf("an empty hazard list is [] in meta.query: %v", q)
	}

	// A warning is in force from its start, at the answer's instant
	// included; one starting later is listed as not yet in force.
	f = everyAnswer()
	f.risk = riskAnswer()
	f.risk.Items = append(f.risk.Items, weather.Warning{ID: "w-3", Family: "src-alpha-weather", Hazard: "wind", Level: 2, LevelName: "Markante Wetterwarnung",
		Issuer: "Wetterdienst", Headline: "Sturmböen", TextLanguage: "de", From: fixedNow.Add(2 * time.Hour), AreaName: "Kreis A"})
	data, _ = decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/weather-risk?at=52.52,13.405", bearer))["data"].(map[string]any)
	ws, _ = data["warnings"].([]any)
	var inForce []any
	for _, x := range ws {
		m, _ := x.(map[string]any)
		inForce = append(inForce, m["inForce"])
	}
	if !slices.Equal(inForce, []any{true, true, false}) {
		t.Errorf("inForce %v, want true, true, false", inForce)
	}
}

func TestDrivingRulesAnswer(t *testing.T) {
	req.Covers(t, "FR-RULES-001", "ENV-040")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/driving-rules?topic=winter_tyres", bearer)
	if w.Code != http.StatusOK || f.gotTopic != "winter_tyres" {
		t.Fatalf("status %d, asked %q: %s", w.Code, f.gotTopic, w.Body)
	}
	body := decode(t, w)
	data, _ := body["data"].(map[string]any)
	rs, _ := data["rules"].([]any)
	r0, _ := rs[0].(map[string]any)
	r1, _ := rs[1].(map[string]any)
	if data["topic"] != "winter_tyres" || data["title"] != "Winterreifen" || data["reviewedOn"] != "2026-10-01" || data["disclaimer"] != rules.Disclaimer {
		t.Errorf("data %v", data)
	}
	if v, has := r1["legalBasis"]; len(rs) != 2 || r0["text"] != "Bei Glätte Winterreifen." || r0["legalBasis"] != "§ 2 Abs. 3a StVO" || !has || v != nil {
		t.Errorf("rules %v", rs)
	}
	if attr, _ := meta(t, body)["attribution"].([]any); len(attr) != 1 {
		t.Errorf("attribution %v", attr)
	}
}

func TestDrivingRulesZones(t *testing.T) {
	req.Covers(t, "FR-RULES-001")
	f := everyAnswer()
	f.topic.Code = "lez"
	z := rules.Zone{Key: "lez_alpha/z1", Role: "zone", Name: "Umweltzone", DistanceM: 420, Family: "lez_alpha"}
	f.zones = features.ZonesAnswer{Zones: []rules.Zone{z}, Verdict: &rules.Verdict{Kind: "outside", Zone: &z}, Answer: &rules.ZoneAnswer{}}
	h := featureHandler(t, config.Defaults(), f, nil)
	body := decode(t, do(t, h, http.MethodGet, "/v1/driving-rules?topic=lez&at=52.52,13.405", bearer))
	data, _ := body["data"].(map[string]any)
	v, _ := data["zoneVerdict"].(map[string]any)
	if v["distanceM"] != 420.0 || v["zone"] != "lez_alpha/z1" || data["zoneCity"] != nil || f.gotZones == nil || f.gotZones.City != "" {
		t.Errorf("outside: %v, asked %+v", data, f.gotZones)
	}
	f.zones.Verdict.Kind = "inside"
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/driving-rules?topic=lez&at=52.52,13.405", bearer))["data"].(map[string]any)
	if v, _ = data["zoneVerdict"].(map[string]any); v["distanceM"] != nil {
		t.Errorf("inside states no distance: %v", v)
	}
	green := "green"
	f.zones = features.ZonesAnswer{City: &rules.ZoneCity{Name: "Matfienland", Caveat: "no zone held"}}
	body = decode(t, do(t, h, http.MethodGet, "/v1/driving-rules?topic=lez&city=Matfienland", bearer))
	data, _ = body["data"].(map[string]any)
	c, _ := data["zoneCity"].(map[string]any)
	notes, _ := meta(t, body)["notes"].(map[string]any)
	cond, _ := notes["condition"].([]any)
	if c["name"] != "Matfienland" || c["covered"] != false || c["zone"] != nil || data["zones"] != nil || f.gotZones.City != "Matfienland" ||
		len(cond) != 1 || cond[0] != "no zone held" {
		t.Errorf("a city with no entry: %v %v", data, notes)
	}
	f.zones = features.ZonesAnswer{City: &rules.ZoneCity{Name: "Berlin", Plakette: &green, Covered: true}}
	body = decode(t, do(t, h, http.MethodGet, "/v1/driving-rules?topic=lez&city=Berlin", bearer))
	c, _ = body["data"].(map[string]any)["zoneCity"].(map[string]any)
	notes, _ = meta(t, body)["notes"].(map[string]any)
	if cond, _ = notes["condition"].([]any); c["covered"] != true || c["plakette"] != "green" || len(cond) != 0 {
		t.Errorf("an entry: %v %v", c, notes)
	}
}

func TestWeatherAndRulesRefusals(t *testing.T) {
	req.Covers(t, "FR-RULES-001", "NFR-SEC-002")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: rules.ErrUnknownTopic}, nil), http.MethodGet, "/v1/driving-rules?topic=nope", bearer), 404, "not-found")
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, target := range []string{
		"/v1/weather-risk", "/v1/weather-risk?at=91,13", "/v1/weather-risk?at=52,13&min_level=5", "/v1/weather-risk?at=52,13&min_level=-1",
		"/v1/weather-risk?at=52,13&hazards=tornado", "/v1/driving-rules", "/v1/driving-rules?topic=Winter", "/v1/driving-rules?topic=1x",
	} {
		assertProblem(t, do(t, h, http.MethodGet, target, bearer), 400, "invalid-parameter")
	}
}

func (f *fakeFeatures) LorryRules(context.Context, access.Record, roads.LorryQuery) (features.LorryAnswer, error) {
	return f.lorry, f.err
}

func (f *fakeFeatures) Sources(context.Context, access.Record, string) ([]provenance.Family, features.Answer[struct{}], error) {
	return nil, features.Answer[struct{}]{}, f.err
}

func (f *fakeFeatures) Motorways(_ context.Context, _ access.Record, q roads.CatalogueQuery) (features.MotorwaysAnswer, error) {
	f.gotCat = q
	return f.motorways, f.err
}
