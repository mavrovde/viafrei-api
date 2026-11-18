package httpapi_test

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// nonFinite is every spelling of NaN and the infinities strconv.ParseFloat
// reads as a number; signed NaN it refuses itself (an invalid float).
var nonFinite = []string{"NaN", "+NaN", "-NaN", "nan", "Inf", "+Inf", "-Inf", "Infinity", "-infinity"}

// numberParameters lists, per operation path, the number query parameters
// the published document declares.
func numberParameters(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := httpapi.OpenAPI(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					Type string `json:"type"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			if method != "get" {
				continue
			}
			for _, p := range op.Parameters {
				if p.In == "query" && p.Schema.Type == "number" {
					out[path] = append(out[path], p.Name)
				}
			}
		}
	}
	return out
}

// TestEveryNumberParameterRefusesNaNAndInfinity (#149, #150): every number
// query parameter of the contract refuses NaN and the infinities with a 400
// that names it and is not retryable; NaN passes a minimum and a maximum, so
// the schema bounds alone would let it reach the handler.
func TestEveryNumberParameterRefusesNaNAndInfinity(t *testing.T) {
	req.Covers(t, "ERR-005", "NFR-SEC-002")
	params := numberParameters(t)
	walked := 0
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for path, names := range params {
		target := strings.NewReplacer("{", "", "}", "").Replace(path)
		for _, name := range names {
			walked++
			for _, v := range nonFinite {
				w := do(t, h, http.MethodGet, target+"?"+url.Values{name: {v}}.Encode(), bearer)
				p := assertProblem(t, w, http.StatusBadRequest, "invalid-parameter")
				detail := `"detail":"expected a finite number"`
				if strings.HasSuffix(v, "NaN") && v != "NaN" {
					detail = `"detail":"invalid float"`
				}
				if p["retryable"] != false || !strings.Contains(w.Body.String(), `"parameter":"query.`+name+`","code":"invalid",`+detail) {
					t.Errorf("%s %s=%s: %s", path, name, v, w.Body)
				}
			}
		}
	}
	// charging-options min_kw and max_kw, watch hours at least.
	if walked < 3 {
		t.Fatalf("only %d number parameters walked: %v", walked, params)
	}
	// A finite value that is no float at all is still the parameter check's.
	w := do(t, h, http.MethodGet, "/v1/watch?kind=road&target=A7&hours=abc", bearer)
	if assertProblem(t, w, http.StatusBadRequest, "invalid-parameter"); strings.Contains(w.Body.String(), "finite") {
		t.Errorf("hours=abc: %s", w.Body)
	}
}

// TestWatchHoursOutsideTheWindowAreRefused (#150): hours refuses NaN, the
// infinities and a number over the 24-hour window; until refuses an instant
// not after now, as the platform's watch; no answer's until is before its
// from.
func TestWatchHoursOutsideTheWindowAreRefused(t *testing.T) {
	h := featureHandler(t, config.Defaults(), watchAnswer(), nil)
	for _, v := range []string{"NaN", "Infinity", "-Infinity", "1e30", "1e999", "0.1", "-1"} {
		w := do(t, h, http.MethodGet, "/v1/watch?"+url.Values{"kind": {"road"}, "target": {"A100"}, "hours": {v}}.Encode(), bearer)
		if assertProblem(t, w, http.StatusBadRequest, "invalid-parameter"); !strings.Contains(w.Body.String(), `"parameter":"query.hours"`) {
			t.Errorf("hours=%s: %s", v, w.Body)
		}
	}
	for _, at := range []time.Time{fixedNow.Add(-time.Hour), fixedNow} {
		w := do(t, h, http.MethodGet, "/v1/watch?"+url.Values{"kind": {"road"}, "target": {"A100"}, "until": {at.Format(time.RFC3339)}}.Encode(), bearer)
		if assertProblem(t, w, http.StatusBadRequest, "invalid-parameter"); !strings.Contains(w.Body.String(), `"parameter":"query.until"`) {
			t.Errorf("until=%s: %s", at, w.Body)
		}
	}
	for _, v := range []string{"0.25", "1", "3.5", "24"} {
		code, d := watch(t, h, url.Values{"kind": {"road"}, "target": {"A100"}, "hours": {v}})
		if code != http.StatusOK {
			t.Fatalf("hours=%s: %d", v, code)
		}
		from, _ := time.Parse(time.RFC3339, d["from"].(string))
		until, _ := time.Parse(time.RFC3339, d["until"].(string))
		if until.Before(from) || d["ended"] != false {
			t.Errorf("hours=%s: from %v until %v ended %v", v, d["from"], d["until"], d["ended"])
		}
	}
}

// TestANonFiniteAnswerIsNeverA200 (#149): were a non-finite number ever to
// reach an answer, the encoder refuses it and the request answers a 500
// problem, never a 200 carrying NaN.
func TestANonFiniteAnswerIsNeverA200(t *testing.T) {
	req.Covers(t, "ERR-001")
	f := everyAnswer()
	nan := math.NaN()
	f.charging.Items[0].Points[0].MaxPowerKw = &nan
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/charging-options?at=52.52,13.405", bearer)
	if assertProblem(t, w, http.StatusInternalServerError, "internal"); strings.Contains(w.Body.String(), "NaN") {
		t.Errorf("the answer carries NaN: %s", w.Body)
	}
}
