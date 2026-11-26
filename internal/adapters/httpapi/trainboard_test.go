package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) TrainBoard(_ context.Context, key access.Record, q features.TrainBoardQuery) (features.TrainBoardAnswer, error) {
	f.gotKey, f.gotEVA, f.gotTrainBoard = key, q.EVA, q
	return f.trainBoard, f.err
}

// post sends body as JSON, held to the document like do.
func post(t *testing.T, h http.Handler, target, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for k, vs := range header {
		r.Header[k] = vs
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	checkContract(t, http.MethodPost, target, w)
	return w
}

func TestTrainBoardServesAFreshBoardAsTheMCPServerShowsIt(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	f := everyAnswer()
	fetched := fixedNow.Add(-10 * time.Second)
	changed := time.Date(2026, 3, 28, 12, 15, 0, 0, time.UTC)
	fam := transitFamily
	f.trainBoard = features.TrainBoardAnswer{AsOf: &fetched, Board: &transit.TrainBoard{EVA: "8000105", FetchedAt: fetched, Departures: []transit.TrainDeparture{
		{PlannedAt: time.Date(2026, 3, 28, 12, 10, 0, 0, time.UTC), ChangedAt: &changed, Platform: sp("5"), ChangedPlatform: sp("7"), Line: sp("ICE 571")},
		{PlannedAt: time.Date(2026, 3, 28, 12, 20, 0, 0, time.UTC), Platform: sp("3"), Cancelled: true},
	}}, Meta: features.Answer[struct{}]{Families: []provenance.Family{fam}, At: fetched}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/train-boards/8000105", bearer)
	data, _ := decode(t, w)["data"].(map[string]any)
	rows, _ := data["departures"].([]any)
	if w.Code != 200 || f.gotEVA != "8000105" || f.gotTrainBoard.Limit != 5 || f.gotTrainBoard.DurationMin != 60 || data["fresh"] != true || data["ageSeconds"] != 10.0 || len(rows) != 2 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	r0, _ := rows[0].(map[string]any)
	r1, _ := rows[1].(map[string]any)
	if r0["delayMin"] != 5.0 || r0["platform"] != "7" || r0["plannedPlatform"] != "5" || r0["platformChanged"] != true ||
		r1["platform"] != "3" || r1["platformChanged"] != false || r1["cancelled"] != true || r1["delayMin"] != 0.0 {
		t.Errorf("rows %v", rows)
	}
	// none fresh: no departures, the newest fetch held and its age
	old := fixedNow.Add(-5 * time.Minute)
	f.trainBoard = features.TrainBoardAnswer{AsOf: &old}
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/train-boards/8000105", bearer))["data"].(map[string]any)
	if rows, _ := data["departures"].([]any); data["fresh"] != false || data["ageSeconds"] != 300.0 || len(rows) != 0 {
		t.Errorf("stale: %v", data)
	}
	f.trainBoard = features.TrainBoardAnswer{}
	data, _ = decode(t, do(t, h, http.MethodGet, "/v1/train-boards/8000105", bearer))["data"].(map[string]any)
	if data["fresh"] != false || data["fetchedAt"] != nil || data["ageSeconds"] != nil {
		t.Errorf("none held: %v", data)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/train-boards/80001", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, h, http.MethodGet, "/v1/train-boards/8000105?limit=0", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, h, http.MethodGet, "/v1/train-boards/8000105?limit=51", bearer), 400, "invalid-parameter")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/train-boards/8000105", bearer), 403, "source-not-permitted")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/train-boards/8000105", bearer), 404, "feature-disabled")
}

func TestTrainBoardCarriesTheStoredBoardsCapAndInterchange(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	f := everyAnswer()
	read := fixedNow.Add(-4 * time.Minute)
	part, u := "Frankfurt (Main) Hbf (tief)", "U4"
	late := fixedNow.Add(3 * time.Minute)
	two := 2
	f.trainBoard = features.TrainBoardAnswer{AsOf: &read,
		Board: &transit.TrainBoard{EVA: "8000105", EVAs: []string{"8000105", "8098105"}, FetchedAt: read, Stored: true, Degraded: true,
			Departures: []transit.TrainDeparture{{PlannedAt: fixedNow.Add(time.Minute), Line: sp("S 8"), StationPart: &part}}},
		Cap: &transit.BoardCap{More: 9, Until: fixedNow.Add(50 * time.Minute)},
		Interchange: &transit.Interchange{
			Departures: []transit.Departure{{Line: &u, RouteType: 400, PlannedAt: fixedNow.Add(time.Minute), ExpectedAt: &late, DelayMin: &two, Live: true, Family: "src-alpha-transit"},
				{Line: &u, RouteType: 0, PlannedAt: fixedNow.Add(2 * time.Minute), Family: "src-alpha-transit"}},
			Lines: []transit.LineGroup{{Modes: []string{"subway"}, Lines: []string{"U4"}}}},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: read}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/train-boards/8000105?limit=1&duration_min=90", bearer)
	body := w.Body.String()
	for _, want := range []string{`"evaNumbers":["8000105","8098105"]`, `"degraded":true`, `"stationPart":"Frankfurt (Main) Hbf (tief)"`, `"capped":{"more":9,`,
		`"interchange":{"departures":[{"mode":"subway"`, `"mode":"tram"`, `"expectedAt":null`, `"lines":[{"modes":["subway"],"lines":["U4"]}]`, `"ageSeconds":240`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %s: %s", want, body)
		}
	}
	if w.Code != 200 || f.gotTrainBoard.Limit != 1 || f.gotTrainBoard.DurationMin != 90 {
		t.Errorf("%d asked %+v", w.Code, f.gotTrainBoard)
	}
	f.trainBoard = features.TrainBoardAnswer{}
	if body = do(t, h, http.MethodGet, "/v1/train-boards/8000105", bearer).Body.String(); !strings.Contains(body, `"interchange":null`) || !strings.Contains(body, `"capped":null`) {
		t.Errorf("none: %s", body)
	}
}
