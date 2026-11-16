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

func (f *fakeFeatures) Stations(_ context.Context, key access.Record, q transit.Query) (features.Answer[transit.Station], error) {
	f.gotKey, f.gotStation = key, q
	return f.stations, f.err
}

var transitFamily = provenance.Family{
	Source:     access.Source{Family: "src-alpha-transit", Domain: "transit", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/t"},
	StaleAfter: 24 * time.Hour, DataAsOf: tp(fixedNow.Add(-time.Hour)),
}

func stationsAnswer() features.Answer[transit.Station] {
	return features.Answer[transit.Station]{Items: []transit.Station{
		{ID: "sp-05-0001", Name: "Köln Hbf", Location: geo.Point{Lat: 50.9430001, Lon: 6.9589999}, NationalCode: sp("de:05315:11201"),
			RegionCode: sp("05315"), Modes: []transit.Mode{"long_distance_rail", "bus"}, DistanceM: ip(120), Family: "src-alpha-transit"},
		{ID: "sp-05-0002", Name: "Köln Messe/Deutz", Location: geo.Point{Lat: 50.94, Lon: 6.975}, Modes: []transit.Mode{}, Family: "src-alpha-transit"},
	}, HasMore: true, Families: []provenance.Family{transitFamily}, At: fixedNow}
}

func TestStationsAnswer(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001", "ENV-040")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/stations?q=koeln&at=50.94,6.96&radius_m=3000&limit=8", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if g := f.gotStation; g.Text != "koeln" || g.At == nil || *g.At != (geo.Point{Lat: 50.94, Lon: 6.96}) || g.RadiusM != 3000 || g.Limit != 8 {
		t.Errorf("asked %+v", g)
	}
	body := decode(t, w)
	data, _ := body["data"].([]any)
	s0, _ := data[0].(map[string]any)
	loc, _ := s0["location"].(map[string]any)
	modes, _ := s0["modes"].([]any)
	if len(data) != 2 || s0["id"] != "sp-05-0001" || s0["name"] != "Köln Hbf" || loc["lat"] != 50.943 || loc["lon"] != 6.959 ||
		s0["stopCode"] != "de:05315:11201" || s0["districtCode"] != "05315" || s0["distanceM"] != 120.0 ||
		s0["sourceFamily"] != "src-alpha-transit" || len(modes) != 2 || modes[0] != "long_distance_rail" {
		t.Fatalf("first station %v", s0)
	}
	s1, _ := data[1].(map[string]any)
	if m, _ := s1["modes"].([]any); m == nil || len(m) != 0 || s1["stopCode"] != nil || s1["districtCode"] != nil || s1["distanceM"] != nil {
		t.Errorf("second station %v", s1)
	}
	m := meta(t, body)
	q, _ := m["query"].(map[string]any)
	at, _ := q["at"].(map[string]any)
	page, _ := m["page"].(map[string]any)
	if q["q"] != "koeln" || at["lat"] != 50.94 || q["radiusM"] != 3000.0 || page["limit"] != 8.0 || page["hasMore"] != true {
		t.Errorf("query %v, page %v", q, page)
	}

	// A name alone: no position, no radius; the default limit.
	f = everyAnswer()
	body = decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/stations?q=K%C3%B6ln", bearer))
	q, _ = meta(t, body)["query"].(map[string]any)
	if g := f.gotStation; g.Text != "Köln" || g.At != nil || g.RadiusM != 0 || g.Limit != config.Defaults().Pagination.DefaultLimit {
		t.Errorf("a name alone: asked %+v", g)
	}
	if v, has := q["at"]; !has || v != nil || q["radiusM"] != nil || q["q"] != "Köln" {
		t.Errorf("a name alone: query %v", q)
	}

	// A position alone: the default radius.
	f = everyAnswer()
	body = decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/stations?at=50.94,6.96", bearer))
	q, _ = meta(t, body)["query"].(map[string]any)
	if g := f.gotStation; g.Text != "" || g.At == nil || g.RadiusM != config.Defaults().Geo.DefaultRadiusM || q["q"] != nil {
		t.Errorf("a position alone: asked %+v, query %v", g, q)
	}
	if !slices.Contains(featureScopes().Key.Plan.Scopes, "transit") {
		t.Fatal("the rig's key has no transit scope")
	}
}

func TestStationsRefusals(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001", "NFR-SEC-002")
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, target := range []string{
		"/v1/stations", "/v1/stations?q=k", "/v1/stations?at=91,13", "/v1/stations?at=52,13&limit=61",
		"/v1/stations?q=koeln&radius_m=0", "/v1/stations?at=52",
	} {
		assertProblem(t, do(t, h, http.MethodGet, target, bearer), 400, "invalid-parameter")
	}
}
