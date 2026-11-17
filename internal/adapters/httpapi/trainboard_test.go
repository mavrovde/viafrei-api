package httpapi_test

import (
	"bytes"
	"context"
	"errors"
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

func (f *fakeFeatures) RecordTrainBoard(_ context.Context, key access.Record, b transit.TrainBoard) error {
	f.gotKey, f.gotTrain = key, b
	return f.boardErr
}

func (f *fakeFeatures) TrainBoard(_ context.Context, key access.Record, eva string) (features.TrainBoardAnswer, error) {
	f.gotKey, f.gotEVA = key, eva
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

const boardBody = `{"eva":"8000105","fetchedAt":"2026-03-28T11:59:30Z","departures":[{"plannedAt":"2026-03-28T12:10:00Z","changedAt":"2026-03-28T12:15:00Z",
"platform":"5","changedPlatform":"7","category":"ICE","number":"571","line":"ICE 571","destination":"München Hbf","cancelled":false}]}`

func TestRecordTrainBoardHandsTheBoardOver(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	w := post(t, h, "/v1/train-boards", boardBody, bearer)
	if w.Code != http.StatusNoContent || f.gotTrain.EVA != "8000105" || len(f.gotTrain.Departures) != 1 || *f.gotTrain.Departures[0].ChangedPlatform != "7" ||
		!f.gotTrain.FetchedAt.Equal(time.Date(2026, 3, 28, 11, 59, 30, 0, time.UTC)) {
		t.Fatalf("status %d %+v: %s", w.Code, f.gotTrain, w.Body)
	}
	// the schema names the field it refuses
	w = post(t, h, "/v1/train-boards", `{"eva":"80001","fetchedAt":"2026-03-28T11:59:30Z","departures":[]}`, bearer)
	if assertProblem(t, w, 400, "invalid-parameter"); !bytes.Contains(w.Body.Bytes(), []byte("body.eva")) {
		t.Errorf("field not named: %s", w.Body)
	}
	// so does the domain's check
	f.boardErr = &transit.BoardFieldError{Field: "departures[0].plannedAt", Detail: "must lie within a day of fetchedAt"}
	if w = post(t, h, "/v1/train-boards", boardBody, bearer); !bytes.Contains(w.Body.Bytes(), []byte("body.departures[0].plannedAt")) {
		t.Errorf("domain field not named: %s", w.Body)
	}
	assertProblem(t, w, 400, "invalid-parameter")
	f.boardErr = features.ErrOwnKeyOnly
	assertProblem(t, post(t, h, "/v1/train-boards", boardBody, bearer), 403, "scope-denied")
	f.boardErr = features.ErrNoWriter
	assertProblem(t, post(t, h, "/v1/train-boards", boardBody, bearer), 404, "feature-disabled")
	f.boardErr = errors.New("boom")
	if w := post(t, h, "/v1/train-boards", boardBody, bearer); w.Code < 500 {
		t.Errorf("storage fault: %d", w.Code)
	}
	assertProblem(t, post(t, featureHandler(t, config.Defaults(), nil, nil), "/v1/train-boards", boardBody, bearer), 404, "feature-disabled")
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
	if w.Code != 200 || f.gotEVA != "8000105" || data["fresh"] != true || data["ageSeconds"] != 10.0 || len(rows) != 2 {
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
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/train-boards/8000105", bearer), 403, "source-not-permitted")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/train-boards/8000105", bearer), 404, "feature-disabled")
}
