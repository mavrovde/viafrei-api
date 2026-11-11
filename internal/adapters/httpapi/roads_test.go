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
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) MotorwayConditions(_ context.Context, key access.Record, q roads.CorridorQuery) (features.Conditions, error) {
	f.gotKey, f.gotRoad = key, q
	return f.conditions, f.err
}

func (f *fakeFeatures) RoadConditions(_ context.Context, key access.Record, q roads.AreaQuery) (features.Conditions, error) {
	f.gotKey, f.gotArea = key, q
	return f.conditions, f.err
}

func (f *fakeFeatures) RoadSituation(_ context.Context, key access.Record, id string) (features.Answer[roads.Situation], error) {
	f.gotKey, f.gotSitID = key, id
	return f.situation, f.err
}

var roadFamily = provenance.Family{
	Source:     access.Source{Family: "src-alpha-roads", Domain: "roads", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/r"},
	StaleAfter: 10 * time.Minute, DataAsOf: tp(fixedNow.Add(-time.Minute)),
}

func closure() roads.Situation {
	return roads.Situation{
		ID: "ev-01-0001", Family: "src-alpha-roads", Kind: roads.KindClosure, Severity: roads.SeverityRoadClosed,
		Road: sp("A8"), Class: roads.ClassMotorway, Direction: roads.DirectionSouth, Towards: sp("Salzburg"),
		Location: &geo.Point{Lat: 48.1000004, Lon: 11.5999996}, From: fixedNow.Add(-time.Hour), Until: tp(fixedNow.Add(26 * time.Hour)),
		LanesClosed: ip(2), LanesTotal: ip(2), DelayMin: ip(25), LengthM: ip(1200),
		Headline: "Vollsperrung", Description: sp("Bergung"), TextLanguage: "de", UpdatedAt: fixedNow.Add(-2 * time.Minute), AtKm: fp(41.3),
	}
}

func conditionsAnswer() features.Conditions {
	bare := roads.Situation{
		ID: "ev-01-0002", Family: "src-alpha-roads", Kind: roads.KindOther, Severity: roads.SeverityOther, Class: roads.ClassOther,
		Direction: roads.DirectionUnknown, From: fixedNow.Add(-time.Hour), Headline: "Hinweis", TextLanguage: "de",
		UpdatedAt: fixedNow.Add(-time.Hour), DistanceM: ip(800),
	}
	reason := roads.SeverityRoadClosed
	return features.Conditions{
		Answer: features.Answer[roads.Situation]{Items: []roads.Situation{closure(), bare}, HasMore: true, Families: []provenance.Family{roadFamily}, At: fixedNow},
		Road:   &roads.Road{Ref: "A8", Class: roads.ClassMotorway, States: []string{"DE-BW", "DE-BY"}},
		Lamp:   roads.LampRed, Reason: &reason,
	}
}

func situationAnswer() features.Answer[roads.Situation] {
	return features.Answer[roads.Situation]{Items: []roads.Situation{closure()}, Families: []provenance.Family{roadFamily}, At: fixedNow}
}

func TestMotorwayConditionsAnswer(t *testing.T) {
	req.Covers(t, "FR-ROAD-001", "FR-ROAD-002", "FR-ROAD-003", "FR-ROAD-004", "ENV-040", "CONV-004")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/motorways/a%208/conditions?direction=south&kinds=closure,other&min_severity=minor&limit=5", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	want := roads.CorridorQuery{Road: "A8", Direction: roads.DirectionSouth, Filter: roads.Filter{Kinds: []roads.Kind{roads.KindClosure, roads.KindOther}, MinSeverity: roads.SeverityMinor}, Limit: 5}
	if f.gotRoad.Road != want.Road || f.gotRoad.Direction != want.Direction || !slices.Equal(f.gotRoad.Kinds, want.Kinds) || f.gotRoad.MinSeverity != want.MinSeverity || f.gotRoad.Limit != 5 {
		t.Errorf("asked %+v", f.gotRoad)
	}
	body := decode(t, w)
	data, _ := body["data"].(map[string]any)
	corridor, _ := data["corridor"].(map[string]any)
	if corridor["road"] != "A8" || corridor["roadType"] != "motorway" || corridor["direction"] != "south" || data["lamp"] != "red" || data["lampReason"] != "road_closed" {
		t.Errorf("data %v", data)
	}
	sits, _ := data["situations"].([]any)
	s0, _ := sits[0].(map[string]any)
	s1, _ := sits[1].(map[string]any)
	val, _ := s0["validity"].(map[string]any)
	lanes, _ := s0["lanes"].(map[string]any)
	loc, _ := s0["location"].(map[string]any)
	if s0["kind"] != "closure" || s0["severity"] != "road_closed" || s0["road"] != "A8" || s0["directionText"] != "Salzburg" || s0["atKm"] != 41.3 ||
		val["fromLocal"] != "2026-03-29T01:30:00+01:00" || val["untilLocal"] != "2026-03-30T05:30:00+02:00" ||
		lanes["closed"] != 2.0 || loc["lat"] != 48.1 || s0["updatedAtLocal"] != "2026-03-29T03:28:00+02:00" || s0["sourceFamily"] != "src-alpha-roads" {
		t.Errorf("first situation %v", s0)
	}
	if _, has := s0["distanceM"]; has {
		t.Error("a corridor situation carries a distance")
	}
	v1, _ := s1["validity"].(map[string]any)
	if s1["kind"] != "other" || s1["road"] != nil || s1["location"] != nil || s1["lanes"] != nil || v1["until"] != nil || v1["untilLocal"] != nil {
		t.Errorf("bare situation %v", s1)
	}
	m := meta(t, body)
	page, _ := m["page"].(map[string]any)
	q, _ := m["query"].(map[string]any)
	if page["hasMore"] != true || page["limit"] != 5.0 || q["road"] != "A8" || q["minSeverity"] != "minor" {
		t.Errorf("meta %v", m)
	}
	if attr, _ := m["attribution"].([]any); len(attr) != 1 {
		t.Errorf("attribution %v", m["attribution"])
	}
}

func TestMotorwayConditionsGreenWithoutSituations(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	f := everyAnswer()
	f.conditions = features.Conditions{Answer: features.Answer[roads.Situation]{At: fixedNow}, Road: &roads.Road{Ref: "B27", Class: roads.ClassFederal}, Lamp: roads.LampGreen}
	body := decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/motorways/B27/conditions", bearer))
	data, _ := body["data"].(map[string]any)
	corridor, _ := data["corridor"].(map[string]any)
	if data["lamp"] != "green" || data["lampReason"] != nil || corridor["direction"] != "both" || corridor["states"] == nil {
		t.Errorf("data %v", data)
	}
	if sits, _ := data["situations"].([]any); len(sits) != 0 {
		t.Errorf("situations %v", sits)
	}
	if f.gotRoad.MinSeverity != roads.SeverityInfo || f.gotRoad.Kinds != nil {
		t.Errorf("defaults %+v", f.gotRoad)
	}
}

func TestRoadConditionsAnswer(t *testing.T) {
	req.Covers(t, "FR-ROAD-006", "FR-ROAD-002")
	f := everyAnswer()
	f.conditions.Road = nil
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/road-conditions?at=48.1,11.6&radius_m=5000&road_class=motorway,other&kinds=jam", bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if f.gotArea.At != (geo.Point{Lat: 48.1, Lon: 11.6}) || f.gotArea.RadiusM != 5000 || !slices.Equal(f.gotArea.Classes, []roads.Class{roads.ClassMotorway, roads.ClassOther}) || !slices.Equal(f.gotArea.Kinds, []roads.Kind{roads.KindJam}) {
		t.Errorf("asked %+v", f.gotArea)
	}
	body := decode(t, w)
	data, _ := body["data"].(map[string]any)
	area, _ := data["area"].(map[string]any)
	sits, _ := data["situations"].([]any)
	s1, _ := sits[1].(map[string]any)
	if area["radiusM"] != 5000.0 || s1["distanceM"] != 800.0 {
		t.Errorf("data %v", data)
	}
	q, _ := meta(t, body)["query"].(map[string]any)
	if types, _ := q["roadClasses"].([]any); len(types) != 2 {
		t.Errorf("query %v", q)
	}
	do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/road-conditions?at=48.1,11.6", bearer)
	if f.gotArea.Classes != nil || f.gotArea.RadiusM != 2000 || f.gotArea.Limit != 20 {
		t.Errorf("defaults %+v", f.gotArea)
	}
}

func TestRoadSituationAnswer(t *testing.T) {
	req.Covers(t, "FR-ROAD-002")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/road-situations/ev-01-0001", bearer)
	if w.Code != http.StatusOK || f.gotSitID != "ev-01-0001" {
		t.Fatalf("status %d, asked %q: %s", w.Code, f.gotSitID, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	if data["id"] != "ev-01-0001" || data["delayMin"] != 25.0 || data["lengthM"] != 1200.0 {
		t.Errorf("data %v", data)
	}
}

func TestRoadOperationsNotFound(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: roads.ErrUnknownRoad}, nil), http.MethodGet, "/v1/motorways/A999/conditions", bearer), 404, "not-found")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: roads.ErrUnknownSituation}, nil), http.MethodGet, "/v1/road-situations/ev-x", bearer), 404, "not-found")
}

func TestRoadInputsAreChecked(t *testing.T) {
	req.Covers(t, "FR-ROAD-001", "NFR-SEC-002")
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, target := range []string{
		"/v1/motorways/C8/conditions", "/v1/motorways/A%20%208/conditions", "/v1/motorways/A8/conditions?direction=up",
		"/v1/motorways/A8/conditions?kinds=flood", "/v1/motorways/A8/conditions?min_severity=other", "/v1/motorways/A8/conditions?limit=0",
		"/v1/road-conditions", "/v1/road-conditions?at=48,11&road_class=lane", "/v1/road-situations/ev%201",
	} {
		assertProblem(t, do(t, h, http.MethodGet, target, bearer), 400, "invalid-parameter")
	}
}
