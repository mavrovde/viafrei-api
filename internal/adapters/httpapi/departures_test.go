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
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Departures(_ context.Context, key access.Record, q transit.DepartureQuery) (features.Board, error) {
	f.gotKey, f.gotBoard = key, q
	return f.board, f.err
}

func TestDeparturesAnswer(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002", "ENV-040")
	f := everyAnswer()
	exp := fixedNow.Add(3 * time.Minute)
	f.board = features.Board{
		Answer: features.Answer[transit.Departure]{Items: []transit.Departure{
			{JourneyID: "t1@2026-03-29", CallOrder: 4, Line: sp("RE 5"), Mode: "rail", RouteType: 2, Destination: sp("Köln"),
				PlannedAt: fixedNow, PlannedPlatform: sp("2"), Platform: sp("3"), PlatformChanged: true, Live: true,
				Source: sp("src-alpha-transit"), DelayMin: ip(3), ExpectedAt: &exp, Family: "src-alpha-transit"},
			{JourneyID: "t2@2026-03-29", Mode: "rail", PlannedAt: fixedNow, Family: "src-alpha-transit"},
		}, HasMore: true, Families: []provenance.Family{transitFamily}, At: fixedNow},
		Station: transit.Station{ID: "sp-05-0001", Name: "Köln Hbf", Location: geo.Point{Lat: 50.943, Lon: 6.959}},
		Matched: 4, MatchedAll: 9, Dropped: map[int]int{3: 4, 0: 1},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet,
		"/v1/stations/sp-05-0001/departures?from=2026-03-29T03:00:00%2B02:00&duration_min=90&modes=rail,sbahn&categories=RE&limit=1", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	g := f.gotBoard
	if g.StationID != "sp-05-0001" || !g.From.Equal(time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)) || g.Window != 90*time.Minute ||
		!slices.Equal(g.Modes, []transit.Mode{"rail", "sbahn"}) || !slices.Equal(g.Categories, []string{"RE"}) || g.Limit != 1 {
		t.Errorf("asked %+v", g)
	}
	body := decode(t, w)
	data, _ := body["data"].(map[string]any)
	win, _ := data["window"].(map[string]any)
	deps, _ := data["departures"].([]any)
	drop, _ := win["dropped"].([]any)
	if len(deps) != 2 || len(drop) != 2 {
		t.Fatalf("board %v", data)
	}
	d0, _ := deps[0].(map[string]any)
	d, _ := drop[0].(map[string]any)
	if d0["id"] != "t1@2026-03-29/4" || d0["destination"] != "Köln" || d0["delayMin"] != 3.0 || d0["platform"] != "3" ||
		d0["platformChanged"] != true || win["matched"] != 4.0 || win["matchedAll"] != 9.0 || d["code"] != "0" ||
		win["until"] != "2026-03-29T02:30:00Z" {
		t.Errorf("board %v", data)
	}
	meta, _ := body["meta"].(map[string]any)
	if page, _ := meta["page"].(map[string]any); page["hasMore"] != true {
		t.Errorf("meta %v", meta)
	}
	// Defaults: now, an hour, ten rows, every mode.
	f.board = features.Board{}
	w = do(t, h, http.MethodGet, "/v1/stations/sp-05-0001/departures", bearer)
	if g := f.gotBoard; w.Code != http.StatusOK || !g.From.Equal(fixedNow) || g.Window != time.Hour || g.Limit != 10 || g.Modes != nil {
		t.Errorf("defaults %d %+v", w.Code, g)
	}
	meta, _ = decode(t, w)["meta"].(map[string]any)
	q, _ := meta["query"].(map[string]any)
	if m, _ := q["modes"].([]any); m == nil || len(m) != 0 {
		t.Errorf("query %v", q)
	}
	if d1, _ := deps[1].(map[string]any); d1["expectedAt"] != nil {
		t.Errorf("no figure %v", d1)
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/stations/sp-05-0001/departures", bearer), 404, "feature-disabled")
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/sp-05-0001/departures?modes=tube", bearer), 400, "invalid-parameter")
	f.err = transit.ErrNotFound
	assertProblem(t, do(t, h, http.MethodGet, "/v1/stations/nope-1/departures", bearer), 404, "not-found")
}
