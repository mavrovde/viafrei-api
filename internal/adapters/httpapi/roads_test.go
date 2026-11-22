package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
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
		Subtitle: sp("Richtung Salzburg"), Blocked: true,
	}
}

func conditionsAnswer() features.Conditions {
	bare := roads.Situation{
		ID: "ev-01-0002", Family: "src-alpha-roads", Kind: roads.KindOther, Severity: roads.SeverityOther, Class: roads.ClassOther,
		Direction: roads.DirectionUnknown, From: fixedNow.Add(-time.Hour), Headline: "Hinweis", TextLanguage: "de",
		UpdatedAt: fixedNow.Add(-time.Hour), DistanceM: ip(800),
	}
	reason := roads.ReasonRoadClosed
	return features.Conditions{
		Answer: features.Answer[roads.Situation]{Items: []roads.Situation{closure(), bare}, HasMore: true, Families: []provenance.Family{roadFamily}, At: fixedNow},
		Road:   &roads.Road{Ref: "A8", Class: roads.ClassMotorway, States: []string{"DE-BW", "DE-BY"}},
		Lamp:   roads.LampRed, Reason: &reason,
		Reasons: []roads.ReasonCount{{Reason: roads.ReasonRoadClosed, Lamp: roads.LampRed, Count: 1}, {Reason: roads.ReasonLimitedSight, Lamp: roads.LampAmber, Count: 2}},
		Sight: []ampel.SightEntry{{Group: roads.GroupRoads, State: ampel.SightStale, Family: "src-alpha-roads"}, {Group: roads.GroupRoads, State: ampel.SightMissing},
			{Group: ampel.GroupWeather, State: ampel.SightFresh}, {Group: ampel.GroupMessages, State: ampel.SightFresh, Land: "08"}},
	}
}

// TestEveryConditionsAnswerAccountsForItsLamp: the three conditions answers
// list every reason of the lamp with its count and the sight of every group
// read; a green lamp lists no reason.
func TestEveryConditionsAnswerAccountsForItsLamp(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "ENV-040")
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	reasons := `"lampReasons":[{"reason":"road_closed","lamp":"red","count":1},{"reason":"limited_sight","lamp":"amber","count":2}]`
	sight := `"sight":[{"group":"roads","state":"stale","family":"src-alpha-roads","land":null},{"group":"roads","state":"missing","family":null,"land":null},` +
		`{"group":"weather","state":"fresh","family":null,"land":null},{"group":"messages","state":"fresh","family":null,"land":"08"}]`
	urls := []string{"/v1/motorways/A8/conditions", "/v1/road-conditions?at=48.7,9.1", "/v1/states/08/road-conditions"}
	for _, u := range urls {
		w := do(t, h, http.MethodGet, u, bearer)
		if body := w.Body.String(); w.Code != http.StatusOK || !strings.Contains(body, reasons) || !strings.Contains(body, sight) {
			t.Errorf("%s: %d %s", u, w.Code, body)
		}
	}
	f.conditions = features.Conditions{Answer: features.Answer[roads.Situation]{At: fixedNow}, Road: &roads.Road{Ref: "A8", Class: roads.ClassMotorway}, Lamp: roads.LampGreen}
	for _, u := range urls {
		if body := do(t, h, http.MethodGet, u, bearer).Body.String(); !strings.Contains(body, `"lampReasons":[]`) || !strings.Contains(body, `"sight":[]`) {
			t.Errorf("%s green: %s", u, body)
		}
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
	if s0["kind"] != "closure" || s0["severity"] != "road_closed" || s0["road"] != "A8" || s0["directionText"] != "Salzburg" ||
		val["fromLocal"] != "2026-03-29T01:30:00+01:00" || val["untilLocal"] != "2026-03-30T05:30:00+02:00" ||
		lanes["closed"] != 2.0 || loc["lat"] != 48.1 || s0["updatedAtLocal"] != "2026-03-29T03:28:00+02:00" || s0["sourceFamily"] != "src-alpha-roads" {
		t.Errorf("first situation %v", s0)
	}
	if s0["subtitle"] != "Richtung Salzburg" || s0["blocked"] != true {
		t.Errorf("first situation's subtitle and block: %v %v", s0["subtitle"], s0["blocked"])
	}
	if v, has := s1["subtitle"]; !has || v != nil || s1["blocked"] != false {
		t.Errorf("bare situation's subtitle and block: %v %v", s1["subtitle"], s1["blocked"])
	}
	if _, has := s0["distanceM"]; has {
		t.Error("a corridor situation carries a distance")
	}
	pos, _ := s0["position"].(map[string]any)
	if v, has := pos["junctionTo"]; pos["fromKm"] != 41.3 || pos["toKm"] != nil || pos["junctionFrom"] != nil || !has || v != nil {
		t.Errorf("position %v", s0["position"])
	}
	v1, _ := s1["validity"].(map[string]any)
	if p, has := s1["position"]; !has || p != nil || s1["kind"] != "other" || s1["road"] != nil || s1["location"] != nil || s1["lanes"] != nil || v1["until"] != nil || v1["untilLocal"] != nil {
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
	if cs, ok := data["counts"].([]any); !ok || len(cs) != 0 {
		t.Errorf("counts of an empty answer %v", data["counts"])
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
	// The counts per family, in family order, past the limit too.
	f.conditions.Counts = map[string]int{"src-b": 7, "src-a": 2}
	for _, path := range []string{"/v1/road-conditions?at=48.1,11.6", "/v1/states/09/road-conditions", "/v1/motorways/A8/conditions"} {
		if path == "/v1/motorways/A8/conditions" {
			f.conditions.Road = &roads.Road{Ref: "A8", Class: roads.ClassMotorway}
		}
		d, _ := decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, path, bearer))["data"].(map[string]any)
		cs, _ := d["counts"].([]any)
		if len(cs) != 2 {
			t.Errorf("%s: counts %v, data %v", path, d["counts"], d)
			continue
		}
		c0, _ := cs[0].(map[string]any)
		c1, _ := cs[1].(map[string]any)
		if c0["sourceFamily"] != "src-a" || c0["situations"] != 2.0 || c1["sourceFamily"] != "src-b" || c1["situations"] != 7.0 {
			t.Errorf("%s: counts %v", path, d["counts"])
		}
	}
	f.conditions.Counts, f.conditions.Road = nil, nil
	q, _ := meta(t, body)["query"].(map[string]any)
	if types, _ := q["roadClasses"].([]any); len(types) != 2 {
		t.Errorf("query %v", q)
	}
	// A page with more continues by its cursor, an area's and a Land's alike.
	f.conditions.HasMore = true
	for _, path := range []string{"/v1/road-conditions?at=48.1,11.6&limit=2&cursor=4", "/v1/states/09/road-conditions?limit=2&cursor=4"} {
		page, _ := meta(t, decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, path, bearer)))["page"].(map[string]any)
		if f.gotArea.Offset != 4 || f.gotArea.Limit != 2 || page["hasMore"] != true || page["nextCursor"] != fmt.Sprint(4+len(f.conditions.Items)) {
			t.Errorf("%s: asked %+v, page %v", path, f.gotArea, page)
		}
	}
	f.conditions.HasMore = false
	do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/road-conditions?at=48.1,11.6", bearer)
	if f.gotArea.Classes != nil || f.gotArea.RadiusM != 2000 || f.gotArea.Limit != 20 || f.gotArea.Offset != 0 {
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
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: roads.ErrUnknownRoad}, nil), http.MethodGet, "/v1/motorways/A999/parking-options", bearer), 404, "not-found")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: roads.ErrUnknownSituation}, nil), http.MethodGet, "/v1/road-situations/ev-x", bearer), 404, "not-found")
}

func TestRoadInputsAreChecked(t *testing.T) {
	req.Covers(t, "FR-ROAD-001", "NFR-SEC-002")
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, target := range []string{
		"/v1/motorways/C8/conditions", "/v1/motorways/K7864/conditions", "/v1/motorways/L12345/conditions", "/v1/motorways/A%20%208/conditions", "/v1/motorways/A8/conditions?direction=up",
		"/v1/motorways/A8/conditions?kinds=flood", "/v1/motorways/A8/conditions?min_severity=other", "/v1/motorways/A8/conditions?limit=0",
		"/v1/road-conditions", "/v1/road-conditions?at=48,11&road_class=lane", "/v1/road-situations/ev%201",
	} {
		assertProblem(t, do(t, h, http.MethodGet, target, bearer), 400, "invalid-parameter")
	}
}

func TestMotorwayReferencesAreNormalised(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	for _, target := range []string{"/v1/motorways/A08/conditions", "/v1/motorways/a%20008/conditions", "/v1/motorways/A8/conditions"} {
		f.gotRoad = roads.CorridorQuery{}
		if w := do(t, h, http.MethodGet, target, bearer); w.Code != http.StatusOK || f.gotRoad.Road != "A8" {
			t.Errorf("%s: status %d, asked %q", target, w.Code, f.gotRoad.Road)
		}
	}
	// G6: a Land road is read too.
	for target, want := range map[string]string{"/v1/motorways/l%201025/conditions": "L1025", "/v1/motorways/S296/conditions": "S296", "/v1/motorways/b6/conditions": "B6"} {
		f.gotRoad = roads.CorridorQuery{}
		if w := do(t, h, http.MethodGet, target, bearer); w.Code != http.StatusOK || f.gotRoad.Road != want {
			t.Errorf("%s: status %d, asked %q", target, w.Code, f.gotRoad.Road)
		}
	}
	// The path pattern admits only what ParseAnyRoad reads; Resolve refuses
	// the rest on its own all the same.
	in := &httpapi.MotorwayInput{Road: "C8"}
	if errs := in.Resolve(nil); len(errs) != 1 {
		t.Errorf("C8 resolved: %v", errs)
	}
}

// TestTheLampEnumerationsAreTheDomains: the served document's lamp and
// lampReason enumerations of both conditions answers are the domain's lists,
// in the domain's order, which is the platform's order of the reasons.
func TestTheLampEnumerationsAreTheDomains(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "FR-SVC-005")
	w := do(t, newHandler(t, config.Defaults(), nil), http.MethodGet, "/v1/openapi.json", nil)
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Enum []any `json:"enum"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	reasons := []any{}
	for _, r := range roads.Reasons {
		reasons = append(reasons, string(r))
	}
	lamps := []any{}
	for _, l := range roads.Lamps {
		lamps = append(lamps, string(l))
	}
	for _, name := range []string{"MotorwayData", "RoadConditionsData"} {
		p := doc.Components.Schemas[name].Properties
		if got := slices.DeleteFunc(slices.Clone(p["lampReason"].Enum), func(v any) bool { return v == nil }); !slices.Equal(got, reasons) {
			t.Errorf("%s.lampReason %v, want %v", name, got, reasons)
		}
		if !slices.Equal(p["lamp"].Enum, lamps) {
			t.Errorf("%s.lamp %v, want %v", name, p["lamp"].Enum, lamps)
		}
	}
}

func TestConditionsWindow(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/motorways/A8/conditions?horizon_days=3", bearer)
	if w.Code != http.StatusOK || !f.gotRoad.From.Equal(fixedNow) || !f.gotRoad.Until.Equal(fixedNow.Add(72*time.Hour)) {
		t.Fatalf("horizon: %d %+v", w.Code, f.gotRoad.Filter)
	}
	q, _ := meta(t, decode(t, w))["query"].(map[string]any)
	if q["windowFrom"] != "2026-03-29T01:30:00Z" || q["windowUntil"] != "2026-04-01T01:30:00Z" {
		t.Errorf("query %v", q)
	}
	w = do(t, h, http.MethodGet, "/v1/road-conditions?at=52.52,13.405&from=2026-04-01&to=2026-04-02", bearer)
	if w.Code != http.StatusOK || !f.gotArea.From.Equal(time.Date(2026, 3, 31, 22, 0, 0, 0, time.UTC)) || !f.gotArea.Until.Equal(time.Date(2026, 4, 2, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("days: %d %+v", w.Code, f.gotArea.Filter)
	}
	if w = do(t, h, http.MethodGet, "/v1/road-conditions?at=52.52,13.405", bearer); w.Code != http.StatusOK || !f.gotArea.Until.IsZero() {
		t.Errorf("no window: %d %+v", w.Code, f.gotArea.Filter)
	}
	for _, target := range []string{"/v1/motorways/A8/conditions?horizon_days=2&from=2026-04-01", "/v1/road-conditions?at=52.52,13.405&from=2026-04-02&to=2026-04-01"} {
		if w := do(t, h, http.MethodGet, target, bearer); w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
}

func TestMotorwayConditionsCursor(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	f := everyAnswer()
	f.conditions.HasMore = true
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/motorways/A8/conditions?limit=1&cursor=4", bearer)
	page, _ := meta(t, decode(t, w))["page"].(map[string]any)
	if w.Code != http.StatusOK || f.gotRoad.Offset != 4 || page["nextCursor"] != "6" || page["hasMore"] != true {
		t.Fatalf("%d, offset %d, page %v", w.Code, f.gotRoad.Offset, page)
	}
	f.conditions.HasMore = false
	page, _ = meta(t, decode(t, do(t, h, http.MethodGet, "/v1/motorways/A8/conditions", bearer)))["page"].(map[string]any)
	if f.gotRoad.Offset != 0 || page["nextCursor"] != nil {
		t.Errorf("the last page: offset %d, page %v", f.gotRoad.Offset, page)
	}
}

// TestAnUnregisteredRoadAnswersItsCoverage (G6): a road the register does
// not hold answers 200 with data.coverage (its class and the families read
// that hold a situation on a road of that class), never 404; a registered
// road's coverage is null.
func TestAnUnregisteredRoadAnswersItsCoverage(t *testing.T) {
	req.Covers(t, "FR-ROAD-001", "FR-ROAD-003")
	f := everyAnswer()
	f.conditions.Road = &roads.Road{Ref: "L1025", Class: roads.ClassState, States: []string{}}
	f.conditions.Covering = []string{"roadworks_ni"}
	f.conditions.Items = nil
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/motorways/L1025/conditions?from=2026-07-01&to=2026-07-08", bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	cov, _ := data["coverage"].(map[string]any)
	if cov == nil || cov["registered"] != false || cov["roadType"] != "state" || !reflect.DeepEqual(cov["families"], []any{"roadworks_ni"}) {
		t.Errorf("coverage %v", data["coverage"])
	}
	if sits, ok := data["situations"].([]any); !ok || len(sits) != 0 {
		t.Errorf("situations %v", data["situations"])
	}
	f.conditions.Covering = nil
	f.conditions.Road = &roads.Road{Ref: "A8", Class: roads.ClassMotorway}
	w = do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/motorways/A8/conditions", bearer)
	if data, _ := decode(t, w)["data"].(map[string]any); w.Code != http.StatusOK || data["coverage"] != nil {
		t.Errorf("registered road: status %d, coverage %v", w.Code, data["coverage"])
	}
}

// Every road status form states each attributed source's Stand, judged at
// the answer's instant.
func TestRoadAnswersStateTheSourcesStand(t *testing.T) {
	req.Covers(t, "FR-ROAD-002")
	f := everyAnswer()
	iv := 60.0
	changed := fixedNow.Add(-time.Hour)
	f.conditions.Stamps = []roads.Stamp{
		{Family: "src-a", CheckedAt: fixedNow.Add(-time.Minute), UnchangedSince: &changed, IntervalS: &iv},
		{Family: "src-b", CheckedAt: fixedNow.Add(-10 * time.Minute), IntervalS: &iv},
	}
	for _, path := range []string{"/v1/motorways/A8/conditions", "/v1/road-conditions?at=48.1,11.6", "/v1/states/09/road-conditions"} {
		if path != "/v1/motorways/A8/conditions" {
			f.conditions.Road = nil
		}
		w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, path, bearer)
		d, _ := decode(t, w)["data"].(map[string]any)
		st, _ := d["sourceStamps"].([]any)
		if len(st) != 2 {
			t.Fatalf("%s: %d %v", path, w.Code, d["sourceStamps"])
		}
		a, _ := st[0].(map[string]any)
		b, _ := st[1].(map[string]any)
		if a["family"] != "src-a" || a["lastCheckedAt"] != "2026-03-29T01:29:00Z" || a["unchangedSince"] != "2026-03-29T00:30:00Z" || a["possiblyStale"] != false ||
			b["unchangedSince"] != nil || b["possiblyStale"] != true {
			t.Errorf("%s: %v", path, st)
		}
	}
}
