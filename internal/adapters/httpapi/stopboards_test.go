package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestStopBoardsAnswer(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-002")
	f := everyAnswer()
	f.stops = features.StopBoards{Answer: features.Answer[transit.StopBoard]{Items: []transit.StopBoard{
		{Stop: transit.NearStop{StopNear: transit.StopNear{Root: "1:de:05315:11201", Name: "Köln Hbf", Metres: 612.4, At: geo.Point{Lat: 50.943, Lon: 6.959}, Family: "src-alpha-transit"}},
			Board: transit.BoardAnswer{Rows: []transit.Departure{{JourneyID: "t1@2026-03-29", Mode: "rail", PlannedAt: fixedNow, Family: "src-alpha-transit"}}}},
		{Stop: transit.NearStop{StopNear: transit.StopNear{Root: "1:de:05315:1", Name: "Dom", Family: "src-alpha-transit"}}},
	}, Families: []provenance.Family{transitFamily}, At: fixedNow}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/stop-boards?at=50.94,6.96&radius_m=100&limit=2&per_stop=15&duration_min=1440&modes=rail&from=2026-03-29T03:00:00%2B02:00", bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	g := f.gotStops
	if g.RadiusM != 100 || g.Limit != 2 || g.PerStop != 15 || g.Window != 24*time.Hour || len(g.Modes) != 1 || !g.From.Equal(time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("asked %+v", g)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	stops, _ := data["stops"].([]any)
	win, _ := data["window"].(map[string]any)
	s0, _ := stops[0].(map[string]any)
	s1, _ := stops[1].(map[string]any)
	if len(stops) != 2 || s0["stopId"] != "de:05315:11201" || s0["id"] != "1:de:05315:11201" || s0["distanceM"] != 612.0 ||
		len(s0["departures"].([]any)) != 1 || s1["departures"] == nil || len(s1["departures"].([]any)) != 0 || win["until"] != "2026-03-30T01:00:00Z" {
		t.Errorf("data %v", data)
	}
	// Defaults: 5 km, eight stops, four each, the next hour from now.
	w = do(t, h, http.MethodGet, "/v1/stop-boards?at=50.94,6.96", bearer)
	if g := f.gotStops; w.Code != http.StatusOK || g.RadiusM != 5000 || g.Limit != 8 || g.PerStop != 4 || g.Window != time.Hour || !g.From.Equal(fixedNow) {
		t.Errorf("defaults %d %+v", w.Code, g)
	}
	for _, q := range []string{"", "?at=50.94,6.96&radius_m=99", "?at=50.94,6.96&radius_m=5001", "?at=50.94,6.96&duration_min=4",
		"?at=50.94,6.96&duration_min=1441", "?at=50.94,6.96&modes=rail,bike", "?at=x"} {
		if w := do(t, h, http.MethodGet, "/v1/stop-boards"+q, bearer); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, w.Code)
		}
	}
	f.err = transit.ErrNotFound
	if w := do(t, h, http.MethodGet, "/v1/stop-boards?at=50.94,6.96", bearer); w.Code == http.StatusOK {
		t.Errorf("an error answered %d", w.Code)
	}
}
