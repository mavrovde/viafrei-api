package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// raw sends method target to h and returns the answer unchecked by the
// document, which declares no HEAD.
func raw(h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	for k, vs := range header {
		r.Header[k] = vs
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// HEAD is answered as GET without a body: the status and the headers are
// the GET's (#153, RFC 9110 9.3.2). A path with no GET answers HEAD 405,
// and a path the router does not hold 404.
func TestHeadIsAnsweredAsGetWithoutABody(t *testing.T) {
	req.Covers(t, "ERR-001")
	h := newHandler(t, config.Defaults(), nil)
	for _, target := range []string{"/v1/meta", "/healthz", "/v1/nothing"} {
		get, head := raw(h, http.MethodGet, target, nil), raw(h, http.MethodHead, target, nil)
		if head.Code != get.Code {
			t.Errorf("HEAD %s: %d, GET %d", target, head.Code, get.Code)
		}
		if head.Body.Len() != 0 || get.Body.Len() == 0 {
			t.Errorf("HEAD %s: %d body bytes, GET %d", target, head.Body.Len(), get.Body.Len())
		}
		for _, k := range []string{"Content-Type", "Cache-Control", "Etag"} {
			if head.Header().Get(k) != get.Header().Get(k) {
				t.Errorf("HEAD %s: %s %q, GET %q", target, k, head.Header().Get(k), get.Header().Get(k))
			}
		}
	}
	if w := raw(h, http.MethodHead, "/v1/refreshes", nil); w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
		t.Errorf("HEAD of a POST-only path: %d, Allow %q", w.Code, w.Header().Get("Allow"))
	}
}

// Every 405 names the methods its path serves in Allow, HEAD wherever GET
// is (#153, RFC 9110 15.5.6).
func TestAMethodNotAllowedNamesTheAllowedMethods(t *testing.T) {
	req.Covers(t, "ERR-001")
	h := newHandler(t, config.Defaults(), nil)
	for _, tc := range []struct{ method, target, allow string }{
		{http.MethodPut, "/v1/germany", "GET, HEAD"},
		{http.MethodDelete, "/v1/meta", "GET, HEAD"},
		{http.MethodPatch, "/v1/places/a%2Fb/overview", "GET, HEAD"},
		{http.MethodGet, "/v1/refreshes", "POST"},
	} {
		w := do(t, h, tc.method, tc.target, nil)
		assertProblem(t, w, http.StatusMethodNotAllowed, "method-not-allowed")
		if got := w.Header().Get("Allow"); got != tc.allow {
			t.Errorf("%s %s: Allow %q, want %q", tc.method, tc.target, got, tc.allow)
		}
	}
}

// An empty value of a declared query parameter is refused, naming it,
// never read as the parameter's default (#153).
func TestAnEmptyQueryValueIsRefused(t *testing.T) {
	req.Covers(t, "ERR-005")
	h := newHandler(t, config.Defaults(), nil)
	for target, name := range map[string]string{
		"/v1/charging-options?at=52.52,13.405&radius_m=": "radius_m",
		"/v1/fuel-options?radius_m=&at=52.52,13.405":     "radius_m",
		"/v1/fuel-options?at=":                           "at",
	} {
		w := do(t, h, http.MethodGet, target, nil)
		body := assertProblem(t, w, http.StatusBadRequest, "invalid-parameter")
		if s := w.Body.String(); !strings.Contains(s, `"query.`+name+`"`) || !strings.Contains(s, "empty value") {
			t.Errorf("%s: %v", target, body)
		}
	}
}

// verdictGate answers every request with its verdict and counts them.
type verdictGate struct {
	v       ratelimit.Verdict
	charged int
}

func (g *verdictGate) Admit(context.Context, string, ratelimit.Plan) ratelimit.Verdict {
	g.charged++
	return g.v
}

// A valid key presented where no key is checked (an unknown path, a method
// the path does not serve) counts against the key, never the address: its
// answer carries the key's RateLimit fields, and a spent key is refused as
// the key, never "without a key" (#151).
func TestAValidKeyOnAnUnknownPathCountsAgainstTheKey(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	cfg := config.Defaults()
	cfg.RateLimit.Headers = true
	known := principal("consumer")
	known.Key.Plan.Rate, known.Key.Plan.Burst = 10, 10
	for _, tc := range []struct {
		name   string
		v      ratelimit.Verdict
		status int
		slug   string
	}{
		{"within budget, unknown path", ratelimit.Verdict{}, http.StatusNotFound, "not-found"},
		{"rate spent", ratelimit.Verdict{Refused: ratelimit.RateLimited, RetryAfter: time.Second}, http.StatusTooManyRequests, "rate-limited"},
		{"quota spent", ratelimit.Verdict{Refused: ratelimit.QuotaExceeded, RetryAfter: time.Hour}, http.StatusTooManyRequests, "quota-exceeded"},
	} {
		gate, addresses := &verdictGate{v: tc.v}, &addressCounter{}
		h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, Build: buildinfo.Info{Version: "t", Commit: "c"},
			ContractVersion: "0.1", Now: func() time.Time { return fixedNow }, Auth: &fakeAuth{p: known}, Keys: gate, Addresses: addresses})
		if err != nil {
			t.Fatal(err)
		}
		w := raw(h, http.MethodGet, "/v1/nothing", bearer)
		assertProblem(t, w, tc.status, tc.slug)
		if strings.Contains(w.Body.String(), "without a key") {
			t.Errorf("%s: %s", tc.name, w.Body)
		}
		if gate.charged != 1 || addresses.charged != 0 {
			t.Errorf("%s: key charged %d, address %d", tc.name, gate.charged, addresses.charged)
		}
		if w.Header().Get("RateLimit") == "" || w.Header().Get("RateLimit-Policy") == "" {
			t.Errorf("%s: no RateLimit fields: %v", tc.name, w.Header())
		}
		if raw(h, http.MethodPut, "/v1/germany", bearer).Code == http.StatusTooManyRequests && tc.v.Refused == "" {
			t.Errorf("%s: a 405 with a valid key refused", tc.name)
		}
	}
}

// getOperations reads the served document: every GET operation's path,
// its path parameters filled with "x", and its query parameter names.
func getOperations(t *testing.T, h http.Handler) map[string][]string {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct{ Name, In string } `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw(h, http.MethodGet, "/v1/openapi.json", nil).Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for path, ops := range doc.Paths {
		op, ok := ops["get"]
		if !ok {
			continue
		}
		target := path
		query := []string{}
		for _, p := range op.Parameters {
			switch p.In {
			case "path":
				target = strings.ReplaceAll(target, "{"+p.Name+"}", "x")
			case "query":
				query = append(query, p.Name)
			}
		}
		out[target] = query
	}
	if len(out) < 20 {
		t.Fatalf("the document holds %d GET operations", len(out))
	}
	return out
}

// Over the contract: HEAD answers every GET operation with the GET's
// status and no body, and an empty value of each of its query parameters
// is refused 400, naming the parameter (#153).
func TestEveryGetOperationAnswersHeadAndRefusesEmptyValues(t *testing.T) {
	req.Covers(t, "ERR-001", "ERR-005")
	h := newHandler(t, config.Defaults(), nil)
	for target, query := range getOperations(t, h) {
		get, head := raw(h, http.MethodGet, target, bearer), raw(h, http.MethodHead, target, bearer)
		if head.Code != get.Code || head.Code == http.StatusMethodNotAllowed || head.Body.Len() != 0 {
			t.Errorf("HEAD %s: %d with %d body bytes, GET %d", target, head.Code, head.Body.Len(), get.Code)
		}
		for _, name := range query {
			w := raw(h, http.MethodGet, target+"?"+name+"=", nil)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"query.`+name+`"`) {
				t.Errorf("GET %s?%s=: %d %s", target, name, w.Code, w.Body)
			}
		}
	}
}

// An overloaded refusal is told apart from the key's own budget (#156): its
// problem type is overloaded with Retry-After, at admission.shed_status,
// and it carries the key's RateLimit-Policy but no RateLimit remaining
// value; a refusal by the key's quota keeps both fields.
func TestAnOverloadedRefusalCarriesNoRemainingValue(t *testing.T) {
	req.Covers(t, "RATE-006")
	known := principal("consumer")
	known.Key.Plan.Rate, known.Key.Plan.Burst = 10, 10
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		cfg := config.Defaults()
		cfg.RateLimit.Headers = true
		cfg.Admission.ShedStatus = status
		for _, tc := range []struct {
			v         ratelimit.Verdict
			sources   *fakeSources
			status    int
			slug      string
			remaining bool
		}{
			{ratelimit.Verdict{}, &fakeSources{regErr: &admission.Refusal{RetryAfter: time.Second}}, status, "overloaded", false},
			{ratelimit.Verdict{Refused: ratelimit.QuotaExceeded, RetryAfter: time.Hour}, &fakeSources{register: fixtureRegister}, http.StatusTooManyRequests, "quota-exceeded", true},
		} {
			h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, Build: buildinfo.Info{Version: "t", Commit: "c"},
				ContractVersion: "0.1", Now: func() time.Time { return fixedNow }, Auth: &fakeAuth{p: known}, Keys: &verdictGate{v: tc.v}, Sources: tc.sources})
			if err != nil {
				t.Fatal(err)
			}
			w := do(t, h, http.MethodGet, "/v1/key", bearer)
			assertProblem(t, w, tc.status, tc.slug)
			if w.Header().Get("Retry-After") == "" || w.Header().Get("RateLimit-Policy") == "" {
				t.Errorf("%s at %d: %v", tc.slug, status, w.Header())
			}
			if got := w.Header().Get("RateLimit") != ""; got != tc.remaining {
				t.Errorf("%s at %d: RateLimit %q", tc.slug, status, w.Header().Get("RateLimit"))
			}
		}
	}
}
