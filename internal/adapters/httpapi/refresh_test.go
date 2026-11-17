package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

func (f *fakeFeatures) RequestRefresh(_ context.Context, key access.Record, a refresh.Ask) (refresh.Request, error) {
	f.gotKey, f.gotRefresh = key, a
	return f.refresh, f.refreshErr
}

func (f *fakeFeatures) Refresh(_ context.Context, key access.Record, id string) (refresh.Request, error) {
	f.gotKey, f.gotRefID = key, id
	return f.refresh, f.refreshErr
}

const refreshID = "01968a3c-1f00-7000-8000-000000000001"

type refreshBody struct {
	Data struct {
		ID, Source, State string
		SettledAt         *time.Time
		Reason            *string
		Queue             *struct{ Position int }
	}
	Links struct{ Self string }
}

func readRefresh(t *testing.T, w interface{ Bytes() []byte }) refreshBody {
	t.Helper()
	var b refreshBody
	if err := json.Unmarshal(w.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCreateRefreshAnswersSettled201AndOpen202WithLocation(t *testing.T) {
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	at := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	f.refresh = refresh.Request{ID: refreshID, Source: "fuel", State: refresh.Refused, CreatedAt: at, SettledAt: &at, Reason: "no provider key"}
	w := post(t, h, "/v1/refreshes", `{"source":"fuel","subject":{"at":"ags:09162000"}}`, bearer)
	b := readRefresh(t, w.Body)
	if w.Code != http.StatusCreated || w.Header().Get("Location") != b.Links.Self || b.Data.ID != refreshID || b.Data.State != "refused" ||
		*b.Data.Reason != "no provider key" || b.Data.Queue != nil || f.gotRefresh.Source != "fuel" || f.gotRefresh.Subject["at"] != "ags:09162000" {
		t.Fatalf("settled: %d %s %+v", w.Code, w.Body, f.gotRefresh)
	}
	zero := 0
	f.refresh = refresh.Request{ID: refreshID, Source: "fuel", State: refresh.Claimed, CreatedAt: at, Position: &zero}
	w = post(t, h, "/v1/refreshes", `{"source":"fuel","subject":{"at":"ags:09162000"}}`, bearer)
	if b = readRefresh(t, w.Body); w.Code != http.StatusAccepted || b.Data.State != "pending" || b.Data.Queue == nil || b.Data.Queue.Position != 0 ||
		b.Data.SettledAt != nil || w.Header().Get("Location") == "" {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
}

func TestRefreshProblems(t *testing.T) {
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	ask := `{"source":"fuel","subject":{"at":"ags:09162000"}}`
	f.refreshErr = refresh.ErrSource
	if w := post(t, h, "/v1/refreshes", ask, bearer); assertProblem(t, w, 400, "invalid-parameter") != nil && !bytes.Contains(w.Body.Bytes(), []byte(`"body.source"`)) {
		t.Errorf("source not named: %s", w.Body)
	}
	f.refreshErr = &refresh.SubjectError{Location: "subject.at", Detail: "expected string to match pattern"}
	if w := post(t, h, "/v1/refreshes", ask, bearer); assertProblem(t, w, 400, "invalid-parameter") != nil && !bytes.Contains(w.Body.Bytes(), []byte(`"body.subject.at"`)) {
		t.Errorf("field not named: %s", w.Body)
	}
	f.refreshErr = features.ErrOwnKeyOnly
	assertProblem(t, post(t, h, "/v1/refreshes", ask, bearer), 403, "scope-denied")
	assertProblem(t, do(t, h, http.MethodGet, "/v1/refreshes/"+refreshID, bearer), 403, "scope-denied")
	f.refreshErr = features.ErrNoRefreshes
	assertProblem(t, post(t, h, "/v1/refreshes", ask, bearer), 404, "feature-disabled")
	f.refreshErr = refresh.ErrNotFound
	assertProblem(t, do(t, h, http.MethodGet, "/v1/refreshes/"+refreshID, bearer), 404, "not-found")
	f.refreshErr = errors.New("boom")
	if w := post(t, h, "/v1/refreshes", ask, bearer); w.Code < 500 {
		t.Errorf("storage fault: %d", w.Code)
	}
	if w := do(t, h, http.MethodGet, "/v1/refreshes/"+refreshID, bearer); w.Code < 500 {
		t.Errorf("storage fault on read: %d", w.Code)
	}
	assertProblem(t, post(t, h, "/v1/refreshes", `{"source":"fuel"}`, bearer), 400, "invalid-parameter")
	off := featureHandler(t, config.Defaults(), nil, nil)
	assertProblem(t, post(t, off, "/v1/refreshes", ask, bearer), 404, "feature-disabled")
	assertProblem(t, do(t, off, http.MethodGet, "/v1/refreshes/"+refreshID, bearer), 404, "feature-disabled")
}

func TestGetRefreshReadsItAgain(t *testing.T) {
	f := everyAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/refreshes/"+refreshID, bearer)
	b := readRefresh(t, w.Body)
	if w.Code != http.StatusOK || f.gotRefID != refreshID || b.Data.State != "none_due" || b.Links.Self == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
