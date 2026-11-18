package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

func watchAnswer() *fakeFeatures {
	f := everyAnswer()
	f.charging.HasMore = false
	late, onTime := 12, 2
	f.board = features.Board{Answer: features.Answer[transit.Departure]{Items: []transit.Departure{
		{JourneyID: "j1", CallOrder: 1, Mode: "rail", PlannedAt: fixedNow.Add(10 * time.Minute), DelayMin: &late, Family: "t"},
		{JourneyID: "j2", CallOrder: 1, Mode: "rail", PlannedAt: fixedNow.Add(20 * time.Minute), DelayMin: &onTime, Family: "t"},
		{JourneyID: "j3", CallOrder: 1, Mode: "rail", PlannedAt: fixedNow.Add(30 * time.Minute), Cancelled: true, Family: "t"},
	}, Families: []provenance.Family{fresh}, At: fixedNow}}
	f.disruption = features.DisruptionAnswer{Disruptions: transit.Disruptions{Punctuality: &transit.Punctuality{AreaLevel: "state", AreaCode: "05", TripsObserved: 40, TripsLate: 2, Family: "t"},
		Trips: &transit.TripCount{Observed: 40, Late: 12, ObservedAt: fixedNow, Family: "t"}},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{fresh}, At: fixedNow}}
	cellName := "Stadt Berlin"
	f.cell = features.CellAnswer{Cell: weather.Cell{Key: "811000000", Name: &cellName, Warnings: f.risk.Items}, Meta: features.Answer[struct{}]{Families: []provenance.Family{fresh}, At: fixedNow}}
	return f
}

func watch(t *testing.T, h http.Handler, q url.Values) (int, map[string]any) {
	t.Helper()
	w := do(t, h, http.MethodGet, "/v1/watch?"+q.Encode(), bearer)
	if w.Code != http.StatusOK {
		return w.Code, nil
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	return w.Code, data
}

func TestWatchTheOtherKinds(t *testing.T) {
	f := watchAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	for _, c := range []struct {
		kind, target, cond, list string
		n                        int
	}{
		{"station", "1:de:05315:11201", "", "departures", 2},
		{"station", "1:de:05315:11201", `{"delay_min":20}`, "departures", 1},
		{"region", "05", "", "punctuality", -1},
		{"region", "05", `{"late_share":0.5}`, "punctuality", -1},
		{"region", "05", `{"min_trips":100}`, "punctuality", -1},
		{"region", "05", `{"delay_min":20}`, "punctuality", -1},
		{"weather", "811000000", `{"min_level":0}`, "warnings", 2},
		{"weather", "Berlin", "", "warnings", 2},
		{"weather", "52.52,13.405", "", "warnings", 2},
		{"charger", "cs-01-0000", "", "site", -1},
		{"charger", "cs-01-0000", `{"point_id":"cp-2"}`, "site", -1},
	} {
		q := url.Values{"kind": {c.kind}, "target": {c.target}}
		if c.cond != "" {
			q.Set("condition", c.cond)
		}
		code, d := watch(t, h, q)
		if code != http.StatusOK {
			t.Fatalf("%s %s: %d", c.kind, c.cond, code)
		}
		if d["holds"] != nil || d["cell"] != nil || d[c.list] == nil || d["kind"] != c.kind || len(d["situations"].([]any)) != 0 {
			t.Errorf("%s %s: %v", c.kind, c.cond, d)
		}
		if l, ok := d[c.list].([]any); ok && len(l) != c.n {
			t.Errorf("%s %s: %d %s, want %d", c.kind, c.cond, len(l), c.list, c.n)
		}
		// The id stands for the watch: read again by it, the same answer.
		if code, again := watch(t, h, url.Values{"watch_id": {d["watchId"].(string)}}); code != http.StatusOK || again["kind"] != c.kind {
			t.Errorf("%s again by id: %d %v", c.kind, code, again)
		}
	}
	if f.gotBoard.StationID != "1:de:05315:11201" || f.gotBoard.Window != 3*time.Hour || f.gotCell.MinLevel != 2 ||
		f.gotDisrupt.Area != "05" || f.gotDisrupt.LateOverMin != 20 {
		t.Errorf("reads: %+v %+v %+v", f.gotBoard, f.gotCell, f.gotDisrupt)
	}
	_, d := watch(t, h, url.Values{"kind": {"region"}, "target": {"05"}, "condition": {`{"delay_min":15}`}})
	if tr, _ := d["trips"].(map[string]any); f.gotDisrupt.LateOverMin != 15 || tr["lateShare"] != 0.3 || tr["observed"] != 40.0 {
		t.Errorf("region trips: %v %+v", d, f.gotDisrupt)
	}
	_, d = watch(t, h, url.Values{"kind": {"weather"}, "target": {"52.52,13.405"}})
	if f.gotCell.At == nil || f.gotCell.At.Lat != 52.52 {
		t.Errorf("weather at a position: %v %+v", d, f.gotCell)
	}
	f.cell.Place = &places.Place{Name: "Berlin"}
	if _, d = watch(t, h, url.Values{"kind": {"weather"}, "target": {"Berlin"}}); f.gotCell.Cell != "Berlin" || f.gotCell.At != nil {
		t.Errorf("weather by name: %v %+v", d, f.gotCell)
	}
	f.disruption.Trips = nil
	if _, d := watch(t, h, url.Values{"kind": {"region"}, "target": {"05"}}); d["trips"] != nil {
		t.Errorf("no trips observed: %v", d)
	}
	// A region or weather watch credits no family, as the platform's watch.
	for _, q := range []url.Values{{"kind": {"region"}, "target": {"05"}}, {"kind": {"weather"}, "target": {"Berlin"}}} {
		w := do(t, h, http.MethodGet, "/v1/watch?"+q.Encode(), bearer)
		if m, _ := decode(t, w)["meta"].(map[string]any); w.Code != http.StatusOK || len(m["attribution"].([]any)) != 0 {
			t.Errorf("%s credits: %d %v", q.Get("kind"), w.Code, m["attribution"])
		}
	}
}

func TestWatchRefusesWhatDoesNotFit(t *testing.T) {
	h := featureHandler(t, config.Defaults(), watchAnswer(), nil)
	for _, q := range []url.Values{
		{"kind": {"station"}, "target": {"x:1"}, "condition": {`{"radius_km":5}`}},
		{"kind": {"region"}, "target": {"Hamburg"}},
		{"kind": {"region"}, "target": {"05"}, "condition": {`{"delay_min":0}`}},
		{"kind": {"region"}, "target": {"05"}, "condition": {`{"delay_min":601}`}},
		{"kind": {"region"}, "target": {"05"}, "condition": {`{"late_share":0.001}`}},
		{"kind": {"region"}, "target": {"05"}, "condition": {`{"min_trips":10001}`}},
		{"kind": {"place"}, "target": {"52.5,13.4"}, "condition": {`{"radius_km":201}`}},
		{"kind": {"weather"}, "target": {"Kiel"}, "condition": {`{"min_level":5}`}},
		{"kind": {"road"}, "target": {"A7"}, "condition": {`{"min_level":2}`}},
		{"kind": {"station"}, "target": {"x:1"}, "hours": {"2"}, "until": {fixedNow.Add(time.Hour).Format(time.RFC3339)}},
		{"watch_id": {"!!"}},
		{"watch_id": {base64.RawURLEncoding.EncodeToString([]byte("[]"))}},
	} {
		if code, _ := watch(t, h, q); code != http.StatusBadRequest {
			t.Errorf("%v: %d, want 400", q, code)
		}
	}
	if code, _ := watch(t, h, url.Values{"kind": {"weather"}, "target": {"52.5,13.4"}, "until": {fixedNow.Add(30 * time.Hour).Format(time.RFC3339)}}); code != http.StatusBadRequest {
		t.Errorf("a window over 24 hours: %d", code)
	}
	cells := watchAnswer()
	h = featureHandler(t, config.Defaults(), cells, nil)
	cells.cellErr = features.ErrAreaNotCell
	assertProblem(t, do(t, h, http.MethodGet, "/v1/watch?kind=weather&target=Bayern", bearer), 400, "invalid-parameter")
	cells.cellErr = weather.ErrNoCell
	assertProblem(t, do(t, h, http.MethodGet, "/v1/watch?kind=weather&target=Nirgendwo", bearer), 404, "not-found")
	errs := watchAnswer()
	errs.err = access.ErrNonePermitted
	errs.cellErr = access.ErrNonePermitted
	he := featureHandler(t, config.Defaults(), errs, nil)
	for _, k := range [][2]string{{"station", "x:1"}, {"region", "05"}, {"weather", "52.5,13.4"}, {"charger", "cs-1"}} {
		w := do(t, he, http.MethodGet, "/v1/watch?kind="+k[0]+"&target="+url.QueryEscape(k[1]), bearer)
		assertProblem(t, w, 403, "source-not-permitted")
	}
}

func TestWatchNeedsTheScopeOfItsKind(t *testing.T) {
	p := principal("commercial")
	p.Key.Plan.Scopes = []access.Scope{"roads"}
	h, err := httpapi.NewHandler(httpapi.Deps{Config: config.Defaults(), Build: buildinfo.Info{Version: "t", Commit: "c"}, ContractVersion: "0.1",
		Now: func() time.Time { return fixedNow }, Auth: &fakeAuth{p: p}, Sources: &fakeSources{register: fixtureRegister}, Features: watchAnswer()})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range [][2]string{{"station", "x:1"}, {"region", "05"}, {"weather", "52.5,13.4"}, {"charger", "cs-1"}} {
		assertProblem(t, do(t, h, http.MethodGet, "/v1/watch?kind="+k[0]+"&target="+url.QueryEscape(k[1]), bearer), 403, "scope-denied")
	}
	raw, _ := json.Marshal(map[string]any{"k": "road", "key": "A7", "c": map[string]any{}})
	if code, d := watch(t, h, url.Values{"watch_id": {base64.RawURLEncoding.EncodeToString(raw)}}); code != http.StatusOK || d["holds"] != nil {
		t.Errorf("a road watch answers no holds: %d %v", code, d)
	}
}

func TestWatchARoadOrAPlace(t *testing.T) {
	f := watchAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	since := fixedNow.Add(-30 * time.Minute).Format(time.RFC3339)
	for _, q := range []url.Values{
		{"kind": {"road"}, "target": {"A7"}, "condition": {`{"events":["jams","restrictions"]}`}, "hours": {"2"}},
		{"kind": {"road"}, "target": {"B27"}, "request": {"Stau auf der B27"}, "since": {since}},
		{"kind": {"place"}, "target": {"52.52,13.405"}, "condition": {`{"radius_km":10}`}, "until": {fixedNow.Add(time.Hour).Format(time.RFC3339)}},
		{"kind": {"place"}, "target": {"52.52,13.405"}},
	} {
		if code, d := watch(t, h, q); code != http.StatusOK || d["lamp"] != nil || d["holds"] != nil {
			t.Errorf("%v: %d %v", q, code, d)
		}
	}
	if _, d := watch(t, h, url.Values{"kind": {"road"}, "target": {"B27"}, "request": {"Stau"}}); d["note"] == nil {
		t.Errorf("a jam off a motorway is noted: %v", d)
	}
	f.conditions.Items = nil
	if code, d := watch(t, h, url.Values{"kind": {"place"}, "target": {"52.52,13.405"}}); code != http.StatusOK || len(d["situations"].([]any)) != 0 {
		t.Errorf("an empty place: %d %v", code, d)
	}
	for _, q := range []url.Values{
		{"kind": {"place"}, "target": {"nowhere"}},
		{"target": {"A7"}},
		{"kind": {"road"}, "target": {"Husum"}},
		{"kind": {"road"}, "target": {"A7"}, "until": {"later"}},
		{"kind": {"road"}, "target": {"A7"}, "since": {"earlier"}},
		{"kind": {"road"}, "target": {"A7"}, "condition": {`{"event":`}},
	} {
		if code, _ := watch(t, h, q); code != http.StatusBadRequest {
			t.Errorf("%v: %d, want 400", q, code)
		}
	}
	errs := watchAnswer()
	errs.err = access.ErrNonePermitted
	he := featureHandler(t, config.Defaults(), errs, nil)
	assertProblem(t, do(t, he, http.MethodGet, "/v1/watch?kind=road&target=A7", bearer), 403, "source-not-permitted")
	assertProblem(t, do(t, he, http.MethodGet, "/v1/watch?kind=place&target=52.5,13.4", bearer), 403, "source-not-permitted")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/watch?kind=road&target=A7", bearer), 404, "feature-disabled")
}

// FuzzWatch reads a watch's kind, target, condition and id: whatever they
// hold, the answer is never a server error.
func FuzzWatch(f *testing.F) {
	h, err := httpapi.NewHandler(httpapi.Deps{Config: config.Defaults(), Build: buildinfo.Info{Version: "t", Commit: "c"}, ContractVersion: "0.1",
		Now: func() time.Time { return fixedNow }, Auth: &fakeAuth{p: featureScopes()}, Sources: &fakeSources{register: fixtureRegister}, Features: watchAnswer()})
	if err != nil {
		f.Fatal(err)
	}
	f.Add("station", "1:de:05315:11201", `{"delay_min":10}`, "")
	f.Add("region", "05", `{"late_share":0.3}`, "")
	f.Add("weather", "52.5,13.4", `{"min_level":9}`, "")
	f.Add("charger", "cs-1", `{"point_id":""}`, "")
	f.Add("place", "91,181", `{"radius_km":-1}`, "")
	f.Add("", "", "", "eyJrIjoicm9hZCIsImtleSI6IkE3In0")
	f.Fuzz(func(t *testing.T, kind, target, cond, id string) {
		q := url.Values{"kind": {kind}, "target": {target}, "condition": {cond}, "watch_id": {id}}
		w := do(t, h, http.MethodGet, "/v1/watch?"+q.Encode(), bearer)
		if w.Code >= 500 {
			t.Fatalf("%v: status %d", q, w.Code)
		}
	})
}
