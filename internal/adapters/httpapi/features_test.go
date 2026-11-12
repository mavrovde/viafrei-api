package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/logging"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// fakeFeatures answers every feature operation from fixed values and
// records what it was asked.
type fakeFeatures struct {
	charging   features.Answer[energy.Site]
	parking    features.Answer[parking.Site]
	overview   features.Overview
	err        error
	gotCharge  energy.Query
	gotPark    parking.Query
	gotOver    features.OverviewQuery
	gotKey     access.Record
	chargeCall int
}

func (f *fakeFeatures) Charging(_ context.Context, key access.Record, q energy.Query) (features.Answer[energy.Site], error) {
	f.gotKey, f.gotCharge = key, q
	f.chargeCall++
	return f.charging, f.err
}

func (f *fakeFeatures) Parking(_ context.Context, key access.Record, q parking.Query) (features.Answer[parking.Site], error) {
	f.gotKey, f.gotPark = key, q
	return f.parking, f.err
}

func (f *fakeFeatures) Overview(_ context.Context, key access.Record, q features.OverviewQuery) (features.Overview, error) {
	f.gotKey, f.gotOver = key, q
	return f.overview, f.err
}

func sp(s string) *string { return &s }

func fp(v float64) *float64 { return &v }

func ip(v int) *int { return &v }

func tp(t time.Time) *time.Time { return &t }

var (
	fresh = provenance.Family{
		Source:     access.Source{Family: "src-alpha-energy", Domain: "energy", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/a"},
		StaleAfter: time.Hour, DataAsOf: tp(fixedNow.Add(-5 * time.Minute)),
	}
	purpose = provenance.Family{
		Source:     access.Source{Family: "src-gamma-energy", Domain: "energy", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/g"},
		StaleAfter: time.Hour, DataAsOf: tp(fixedNow.Add(-2 * time.Hour)),
		Notes: []provenance.Note{{Kind: provenance.Purpose, Text: "For consumer information only (synthetic)."}},
	}
	terms = provenance.Family{
		Source:     access.Source{Family: "src-delta-parking", Domain: "parking", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/d"},
		StaleAfter: 30 * time.Minute, DataAsOf: tp(fixedNow.Add(-time.Minute)),
		Notes: []provenance.Note{{Kind: provenance.Condition, Text: "Use only after accepting the terms (synthetic)."}},
	}
	place = provenance.Family{
		Source:     access.Source{Family: "src-zeta-places", Domain: "places", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/z"},
		StaleAfter: 7 * 24 * time.Hour,
	}
	ac = energy.CurrentAC
)

func chargingAnswer() features.Answer[energy.Site] {
	return features.Answer[energy.Site]{
		Items: []energy.Site{{
			ID: "cs-01-0000", Family: "src-alpha-energy", Name: sp("Laden"), Access: energy.AccessPublic,
			Location: geo.Point{Lat: 52.5200004, Lon: 13.4049996}, DistanceM: 12,
			Points: []energy.ChargePoint{
				{ID: "cp-1", Current: &ac, MaxPowerKw: fp(22), Availability: &energy.Availability{State: energy.StateAvailable, ObservedAt: fixedNow.Add(-time.Minute).In(time.FixedZone("CET", 3600)), Family: "src-alpha-energy"}},
				{ID: "cp-2"},
			},
		}},
		HasMore: true, Families: []provenance.Family{fresh}, At: fixedNow,
	}
}

func parkingAnswer() features.Answer[parking.Site] {
	return features.Answer[parking.Site]{
		Items: []parking.Site{
			{ID: "pk-01-0000", Family: "src-alpha-parking", Kind: parking.KindGarage, Occupancy: &parking.Occupancy{Free: ip(3), Total: ip(40), State: parking.StateFree, ObservedAt: fixedNow.Add(-time.Minute)}},
			{ID: "pk-01-0001", Family: "src-delta-parking", Kind: parking.KindTruckParking, TermsRequired: true},
		},
		Families: []provenance.Family{terms}, Partial: true, At: fixedNow,
	}
}

func overviewAnswer() features.Overview {
	c, p := chargingAnswer(), parkingAnswer()
	return features.Overview{
		Place:    places.Place{ID: "pl-01-0001", Name: "Berlin", Kind: "locality", Location: geo.Point{Lat: 52.52, Lon: 13.405}, Family: "src-zeta-places"},
		Charging: &c, Parking: &p,
		Parts:    []features.PartResult{{Part: features.PartCharging, Status: features.StatusOK}, {Part: features.PartParking, Status: features.StatusOK}},
		Families: []provenance.Family{fresh, terms, place}, Partial: true,
	}
}

func everyAnswer() *fakeFeatures {
	return &fakeFeatures{charging: chargingAnswer(), parking: parkingAnswer(), overview: overviewAnswer()}
}

func featureScopes() auth.Principal {
	p := principal("commercial")
	p.Key.Plan.Scopes = []access.Scope{"places", "energy", "parking"}
	return p
}

func featureHandler(t *testing.T, cfg *config.Config, f httpapi.Features, log *bytes.Buffer) http.Handler {
	t.Helper()
	d := httpapi.Deps{
		Config: cfg, Build: buildinfo.Info{Version: "t", Commit: "c"}, ContractVersion: "0.1", Now: func() time.Time { return fixedNow },
		Auth: &fakeAuth{p: featureScopes()}, Sources: &fakeSources{register: fixtureRegister},
	}
	if f != nil {
		d.Features = f
	}
	if log != nil {
		d.Logger = logging.New(config.Log{Level: "debug", Format: "json"}, log)
	}
	h, err := httpapi.NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func meta(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	m, _ := body["meta"].(map[string]any)
	if m == nil {
		t.Fatalf("no meta in %v", body)
	}
	return m
}

func warningCodes(m map[string]any) []string {
	var out []string
	ws, _ := m["warnings"].([]any)
	for _, w := range ws {
		e, _ := w.(map[string]any)
		c, _ := e["code"].(string)
		out = append(out, c)
	}
	return out
}

func TestChargingOptionsAnswer(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004", "FR-ENERGY-005", "ENV-003", "ENV-004", "ENV-011", "ENV-040", "ENV-041")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/charging-options?at=52.52,13.405", bearer)
	// This handler reads no freshness versions, so nothing tells when the
	// answer changes and no one may keep it (the versioned max-age is
	// TestARepeatedRequestIsANotModified's).
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, cache %q: %s", w.Code, w.Header().Get("Cache-Control"), w.Body)
	}
	if f.gotCharge != (energy.Query{At: geo.Point{Lat: 52.52, Lon: 13.405}, RadiusM: 2000, Access: energy.FilterPublic, Limit: 20}) || f.gotKey.ID != "abcdefghij" {
		t.Errorf("asked %+v for %q", f.gotCharge, f.gotKey.ID)
	}
	body := decode(t, w)
	data, _ := body["data"].([]any)
	site, _ := data[0].(map[string]any)
	loc, _ := site["location"].(map[string]any)
	if loc["lat"] != 52.52 || loc["lon"] != 13.405 || site["sourceFamily"] != "src-alpha-energy" || site["operator"] != nil {
		t.Errorf("site %v", site)
	}
	sum, _ := site["summary"].(map[string]any)
	if sum["chargePoints"] != 2.0 || sum["availableNow"] != 1.0 || sum["maxPowerKw"] != 22.0 {
		t.Errorf("summary %v", sum)
	}
	pts, _ := site["chargePoints"].([]any)
	p0, _ := pts[0].(map[string]any)
	p1, _ := pts[1].(map[string]any)
	av, _ := p0["availability"].(map[string]any)
	if p0["current"] != "ac" || av["state"] != "available" || av["observedAt"] != "2026-03-29T01:29:00Z" {
		t.Errorf("point %v", p0)
	}
	if p1["current"] != nil || p1["maxPowerKw"] != nil || p1["availability"] != nil {
		t.Errorf("unpublished point %v", p1)
	}
	m := meta(t, body)
	page, _ := m["page"].(map[string]any)
	if page["limit"] != 20.0 || page["hasMore"] != true || m["asOf"] != "2026-03-29T01:25:00Z" || len(warningCodes(m)) != 0 {
		t.Errorf("meta %v", m)
	}
	fr, _ := m["freshness"].([]any)
	f0, _ := fr[0].(map[string]any)
	if f0["family"] != "src-alpha-energy" || f0["ageS"] != 300.0 || f0["staleAfterS"] != 3600.0 || f0["stale"] != false {
		t.Errorf("freshness %v", f0)
	}
	if attr, _ := m["attribution"].([]any); len(attr) != 1 {
		t.Errorf("attribution %v", m["attribution"])
	}
	q, _ := m["query"].(map[string]any)
	if q["radiusM"] != 2000.0 || q["access"] != "public" || q["minKw"] != 0.0 {
		t.Errorf("query %v", q)
	}

	do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/charging-options?at=-1.5,-2&radius_m=500&limit=3&min_kw=50&access=all", bearer)
	if f.gotCharge != (energy.Query{At: geo.Point{Lat: -1.5, Lon: -2}, RadiusM: 500, MinKw: 50, Access: energy.FilterAll, Limit: 3}) {
		t.Errorf("asked %+v", f.gotCharge)
	}
}

func TestChargingOptionsWarnsOfStaleAndWithheldData(t *testing.T) {
	req.Covers(t, "ENV-004", "SEC-033", "FR-ENERGY-004")
	f := everyAnswer()
	f.charging.Families = []provenance.Family{fresh, purpose}
	f.charging.Partial = true
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/charging-options?at=52.52,13.405", bearer)
	m := meta(t, decode(t, w))
	if got := warningCodes(m); !slices.Equal(got, []string{"partial", "data_stale"}) {
		t.Errorf("warnings %v", got)
	}
	notes, _ := m["notes"].(map[string]any)
	if p, _ := notes["purpose"].([]any); len(p) != 1 || p[0] != "For consumer information only (synthetic)." {
		t.Errorf("notes %v", notes)
	}
	if m["asOf"] != "2026-03-28T23:30:00Z" {
		t.Errorf("asOf %v is not the oldest", m["asOf"])
	}
	f.charging.Families = []provenance.Family{place}
	m = meta(t, decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/charging-options?at=52.52,13.405", bearer)))
	fr, _ := m["freshness"].([]any)
	f0, _ := fr[0].(map[string]any)
	if m["asOf"] != nil || f0["dataAsOf"] != nil || f0["ageS"] != nil || f0["stale"] != true {
		t.Errorf("a family that published nothing: %v", m)
	}
}

func TestParkingOptionsAnswer(t *testing.T) {
	req.Covers(t, "FR-PARK-002", "FR-PARK-003", "SEC-030", "ENV-003")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/parking-options?at=52.52,13.405&free_min=2&limit=5", bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if f.gotPark != (parking.Query{At: geo.Point{Lat: 52.52, Lon: 13.405}, RadiusM: 2000, FreeMin: 2, Limit: 5}) {
		t.Errorf("asked %+v", f.gotPark)
	}
	body := decode(t, w)
	data, _ := body["data"].([]any)
	open, _ := data[0].(map[string]any)
	held, _ := data[1].(map[string]any)
	occ, _ := open["occupancy"].(map[string]any)
	if occ["free"] != 3.0 || occ["total"] != 40.0 || occ["state"] != "free" || open["termsRequired"] != false {
		t.Errorf("site %v", open)
	}
	if held["occupancy"] != nil || held["termsRequired"] != true || held["kind"] != "truck_parking" {
		t.Errorf("withheld site %v", held)
	}
	m := meta(t, body)
	ws, _ := m["warnings"].([]any)
	w0, _ := ws[0].(map[string]any)
	if d, _ := w0["detail"].(string); w0["code"] != "partial" || !strings.Contains(d, "termsRequired") {
		t.Errorf("warning %v", w0)
	}
	notes, _ := m["notes"].(map[string]any)
	if c, _ := notes["condition"].([]any); len(c) != 1 {
		t.Errorf("condition notes %v", notes)
	}
	q, _ := m["query"].(map[string]any)
	if q["freeMin"] != 2.0 {
		t.Errorf("query %v", q)
	}

	f.parking.Items = f.parking.Items[:1]
	m = meta(t, decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/parking-options?at=52.52,13.405", bearer)))
	ws, _ = m["warnings"].([]any)
	w0, _ = ws[0].(map[string]any)
	if d, _ := w0["detail"].(string); strings.Contains(d, "termsRequired") {
		t.Errorf("a partial answer with nothing withheld names termsRequired: %v", w0)
	}
}

func TestPlaceOverviewAnswer(t *testing.T) {
	req.Covers(t, "FR-BRIEF-008", "FR-BRIEF-002", "FR-BRIEF-004")
	f := everyAnswer()
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview?needs=parking,charging,parking", bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if f.gotOver.PlaceID != "pl-01-0001" || f.gotOver.RadiusM != 2000 || f.gotOver.PerPartLimit != 5 ||
		!slices.Equal(f.gotOver.Needs, []features.Part{features.PartParking, features.PartCharging}) {
		t.Errorf("asked %+v", f.gotOver)
	}
	body := decode(t, w)
	data, _ := body["data"].(map[string]any)
	pl, _ := data["place"].(map[string]any)
	if pl["id"] != "pl-01-0001" || pl["kind"] != "locality" || data["charging"] == nil || data["parking"] == nil {
		t.Errorf("data %v", data)
	}
	m := meta(t, body)
	parts, _ := m["parts"].([]any)
	p0, _ := parts[0].(map[string]any)
	if len(parts) != 2 || p0["status"] != "ok" || p0["problemType"] != nil {
		t.Errorf("parts %v", parts)
	}
	if fr, _ := m["freshness"].([]any); len(fr) != 3 || !slices.Contains(warningCodes(m), "partial") {
		t.Errorf("meta %v", m)
	}

	f.overview.Parking, f.overview.Partial = nil, false
	f.overview.Parts = []features.PartResult{
		{Part: features.PartCharging, Status: features.StatusOK},
		{Part: features.PartParking, Status: features.StatusTimeout, Err: context.DeadlineExceeded},
	}
	w = do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview?radius_m=900&per_part_limit=2", bearer)
	body = decode(t, w)
	data, _ = body["data"].(map[string]any)
	if _, ok := data["parking"]; ok || w.Code != http.StatusOK || f.gotOver.RadiusM != 900 || f.gotOver.PerPartLimit != 2 || len(f.gotOver.Needs) != 0 {
		t.Errorf("a part that is not ok is absent: %d %v, asked %+v", w.Code, data, f.gotOver)
	}
	parts, _ = meta(t, body)["parts"].([]any)
	p1, _ := parts[1].(map[string]any)
	if p1["status"] != "timeout" || !strings.HasSuffix(p1["problemType"].(string), "/problems/deadline-exhausted") {
		t.Errorf("parts %v", parts)
	}
}

func TestPartProblemTypes(t *testing.T) {
	req.Covers(t, "FR-BRIEF-005")
	for err, slug := range map[error]string{
		features.ErrScopeDenied:    "scope-denied",
		access.ErrTermsNotAccepted: "terms-not-accepted",
		access.ErrNonePermitted:    "source-not-permitted",
	} {
		f := everyAnswer()
		f.overview.Parts[1] = features.PartResult{Part: features.PartParking, Status: features.StatusNotPermitted, Err: err}
		parts, _ := meta(t, decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview", bearer)))["parts"].([]any)
		p1, _ := parts[1].(map[string]any)
		if p1["status"] != "not_permitted" || !strings.HasSuffix(p1["problemType"].(string), "/problems/"+slug) {
			t.Errorf("%v: %v", err, p1)
		}
	}
	f := everyAnswer()
	f.overview.Parts = []features.PartResult{
		{Part: features.PartCharging, Status: features.StatusUnavailable, Err: errors.New("down")},
		{Part: features.PartParking, Status: features.StatusNotRequested},
	}
	parts, _ := meta(t, decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview", bearer)))["parts"].([]any)
	p0, _ := parts[0].(map[string]any)
	p1, _ := parts[1].(map[string]any)
	if !strings.HasSuffix(p0["problemType"].(string), "/problems/upstream-unavailable") || p1["problemType"] != nil {
		t.Errorf("parts %v", parts)
	}
}

func TestNoPartIsAProblem(t *testing.T) {
	req.Covers(t, "FR-BRIEF-005", "FR-BRIEF-003")
	part := func(p features.Part, s features.Status) features.PartResult {
		return features.PartResult{Part: p, Status: s}
	}
	cases := []struct {
		parts  []features.PartResult
		status int
		slug   string
	}{
		{[]features.PartResult{part(features.PartCharging, features.StatusNotPermitted), part(features.PartParking, features.StatusNotRequested)}, 403, "source-not-permitted"},
		{[]features.PartResult{part(features.PartCharging, features.StatusTimeout), part(features.PartParking, features.StatusNotPermitted)}, 503, "deadline-exhausted"},
		{[]features.PartResult{part(features.PartCharging, features.StatusUnavailable), part(features.PartParking, features.StatusNotPermitted)}, 503, "upstream-unavailable"},
	}
	for _, c := range cases {
		f := &fakeFeatures{overview: features.Overview{Parts: c.parts}, err: features.ErrNoPart}
		assertProblem(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview", bearer), c.status, c.slug)
	}
}

func TestFeatureRefusals(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-033", "ERR-001")
	targets := []string{"/v1/charging-options?at=52.52,13.405", "/v1/parking-options?at=52.52,13.405", "/v1/places/pl-01-0001/overview"}
	cases := []struct {
		err    error
		status int
		slug   string
	}{
		{access.ErrTermsNotAccepted, 403, "terms-not-accepted"},
		{access.ErrNonePermitted, 403, "source-not-permitted"},
		{store.ErrDeadlineExhausted, 503, "deadline-exhausted"},
		{store.ErrUnavailable, 503, "upstream-unavailable"},
	}
	// One handler per case, shared by the three targets: building a handler
	// registers the whole contract and is the cost of this test (TST-012's 2 s
	// bound was crossed under -race and coverage with fifteen builds).
	for _, c := range cases {
		h := featureHandler(t, config.Defaults(), &fakeFeatures{err: c.err}, nil)
		for _, target := range targets {
			assertProblem(t, do(t, h, http.MethodGet, target, bearer), c.status, c.slug)
		}
	}
	disabled := featureHandler(t, config.Defaults(), nil, nil)
	for _, target := range targets {
		assertProblem(t, do(t, disabled, http.MethodGet, target, bearer), 404, "feature-disabled")
	}
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: places.ErrNotFound}, nil), http.MethodGet, targets[2], bearer), 404, "not-found")
}

func TestFeaturePostCheckAbortsTheAnswer(t *testing.T) {
	req.Covers(t, "SEC-033")
	var log bytes.Buffer
	before := httpapi.GateViolations()
	w := do(t, featureHandler(t, config.Defaults(), &fakeFeatures{err: features.ErrGateViolation}, &log), http.MethodGet, "/v1/charging-options?at=52.52,13.405", bearer)
	assertProblem(t, w, 500, "internal")
	if httpapi.GateViolations() != before+1 || !strings.Contains(log.String(), "licence gate post-check aborted an answer") {
		t.Errorf("violations %d, log %s", httpapi.GateViolations()-before, log.String())
	}
}

func TestFeatureInputsAreChecked(t *testing.T) {
	req.Covers(t, "NFR-SEC-002", "ERR-005")
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, target := range []string{
		"/v1/charging-options?at=91,0", "/v1/charging-options?at=0,181", "/v1/parking-options?at=999,0",
	} {
		p := assertProblem(t, do(t, h, http.MethodGet, target, bearer), 400, "invalid-parameter")
		errs, _ := p["errors"].([]any)
		e, _ := errs[0].(map[string]any)
		if e["parameter"] != "query.at" {
			t.Errorf("%s: %v", target, p)
		}
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/places/pl-01-0001/overview?needs=fuel", bearer), 400, "invalid-parameter")
}

func TestFeatureDefaultsComeFromTheConfiguration(t *testing.T) {
	req.Covers(t, "FR-GEN-002")
	cfg := config.Defaults()
	cfg.Geo.DefaultRadiusM, cfg.Pagination.DefaultLimit, cfg.Compose.PerPartLimit = 750, 7, 3
	f := everyAnswer()
	h := featureHandler(t, cfg, f, nil)
	do(t, h, http.MethodGet, "/v1/charging-options?at=1,2", bearer)
	do(t, h, http.MethodGet, "/v1/parking-options?at=1,2", bearer)
	do(t, h, http.MethodGet, "/v1/places/pl-1/overview", bearer)
	if f.gotCharge.RadiusM != 750 || f.gotCharge.Limit != 7 || f.gotPark.RadiusM != 750 || f.gotPark.Limit != 7 || f.gotOver.RadiusM != 750 || f.gotOver.PerPartLimit != 3 {
		t.Errorf("charging %+v, parking %+v, overview %+v", f.gotCharge, f.gotPark, f.gotOver)
	}
}

func TestFeatureOperationsNameTheirMCPCounterpart(t *testing.T) {
	req.Covers(t, "TEST-009")
	var d struct {
		Paths map[string]map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(contractBytes(t), &d); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"/v1/charging-options": "charging station search", "/v1/parking-options": "parking search"} {
		if got := d.Paths[path]["get"]["x-mcp-counterpart"]; got != want {
			t.Errorf("%s: counterpart %v", path, got)
		}
	}
}

func TestChargingSummaryJudgesEachReportByItsOwnFamily(t *testing.T) {
	req.Covers(t, "ENV-041", "FR-ENERGY-004")
	// A site of one family with two reports of the same age, each from a
	// family with its own threshold: only the report within its own
	// family's threshold counts as available now.
	slow, quick := fresh, terms
	slow.Source.Family, slow.StaleAfter = "src-alpha-energy", time.Hour
	quick.Source.Family, quick.StaleAfter = "src-beta-energy", 10*time.Minute
	report := func(fam string) *energy.Availability {
		return &energy.Availability{State: energy.StateAvailable, ObservedAt: fixedNow.Add(-20 * time.Minute), Family: fam}
	}
	f := everyAnswer()
	f.charging.Items[0].Family = "src-gamma-energy"
	f.charging.Items[0].Points = []energy.ChargePoint{{ID: "cp-1", Availability: report("src-alpha-energy")}, {ID: "cp-2", Availability: report("src-beta-energy")}}
	f.charging.Families = []provenance.Family{slow, quick}
	data, _ := decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/charging-options?at=52.52,13.405", bearer))["data"].([]any)
	site, _ := data[0].(map[string]any)
	if sum, _ := site["summary"].(map[string]any); sum["availableNow"] != 1.0 {
		t.Errorf("summary %v: each report must use its own family's threshold", sum)
	}
}

func TestOverviewWithAMissingPartIsPartial(t *testing.T) {
	req.Covers(t, "FR-BRIEF-005", "NFR-RES-003")
	f := everyAnswer()
	f.overview.Parking, f.overview.Partial, f.overview.Charging.Partial = nil, false, false
	f.overview.Parts = []features.PartResult{
		{Part: features.PartCharging, Status: features.StatusOK},
		{Part: features.PartParking, Status: features.StatusTimeout, Err: context.DeadlineExceeded},
	}
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview", bearer)
	m := meta(t, decode(t, w))
	ws, _ := m["warnings"].([]any)
	w0, _ := ws[0].(map[string]any)
	if w.Code != http.StatusOK || !slices.Contains(warningCodes(m), "partial") || !strings.Contains(w0["detail"].(string), "meta.parts") {
		t.Errorf("status %d, warnings %v", w.Code, ws)
	}
	// A part that was not asked for is no reason to warn.
	f.overview.Parts[1] = features.PartResult{Part: features.PartParking, Status: features.StatusNotRequested}
	m = meta(t, decode(t, do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview?needs=charging", bearer)))
	if slices.Contains(warningCodes(m), "partial") {
		t.Errorf("warnings %v for an answer with every asked part ok", warningCodes(m))
	}
}

func TestFeatureOperationsListTheirRefusals(t *testing.T) {
	req.Covers(t, "ERR-001")
	var d struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]any `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(contractBytes(t), &d); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/charging-options", "/v1/parking-options", "/v1/places/{placeId}/overview"} {
		for _, status := range []string{"403", "404", "503"} {
			if _, ok := d.Paths[path]["get"].Responses[status].Content["application/problem+json"]; !ok {
				t.Errorf("%s does not list its %s problem", path, status)
			}
		}
	}
}
