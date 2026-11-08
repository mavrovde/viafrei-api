package oascheck

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// sample is a small document with every constraint the generator knows.
const sample = `{
  "openapi": "3.1.0",
  "paths": {
    "/things/{id}": {
      "parameters": [],
      "get": {
        "operationId": "getThing",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}, "example": "t1"},
          {"name": "radius_m", "in": "query", "required": true, "schema": {"type": "integer", "minimum": 1, "maximum": 100}, "example": 5},
          {"name": "kind", "in": "query", "schema": {"type": "string", "enum": ["a", "b"]}},
          {"name": "code", "in": "query", "schema": {"type": "string", "pattern": "^[A-Z]{2}$", "examples": ["DE"]}},
          {"name": "label", "in": "query", "schema": {"type": "string", "pattern": "^[a-z]+$"}},
          {"name": "name", "in": "query", "schema": {"type": "string", "minLength": 2, "maxLength": 5}},
          {"name": "flag", "in": "query", "schema": {"type": "boolean"}},
          {"name": "ratio", "in": "query", "schema": {"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 1, "default": 0.5}},
          {"name": "page", "in": "query", "schema": {"type": "integer"}},
          {"name": "X-Trace", "in": "header", "schema": {"type": "string"}}
        ],
        "responses": {
          "200": {
            "headers": {"X-Count": {"schema": {"type": "integer"}}, "Cache-Control": {"schema": {"type": "string"}}},
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Thing"}}}
          },
          "400": {"content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}}}},
          "4XX": {"content": {"application/json": {"schema": {"type": "object"}}}},
          "default": {"content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}}}}
        }
      }
    },
    "/plain": {
      "get": {
        "operationId": "getPlain",
        "responses": {
          "204": {},
          "default": {"content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}}}}
        }
      }
    }
  },
  "components": {"schemas": {
    "Thing": {"type": "object", "additionalProperties": false, "required": ["id", "sizeM"],
      "properties": {"id": {"type": "string"}, "sizeM": {"type": "integer"}}},
    "Problem": {"type": "object", "required": ["type", "status"],
      "properties": {"type": {"type": "string"}, "status": {"type": "integer"},
        "errors": {"type": "array", "items": {"type": "object", "properties": {"parameter": {"type": "string"}}}}}}
  }}
}`

func load(t *testing.T, src string) *Document {
	t.Helper()
	d, err := Load([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

const thing = `{"id":"t1","sizeM":3}`

func TestLoadFailsClosed(t *testing.T) {
	req.Covers(t, "TST-011", "TST-015")
	for name, src := range map[string]string{
		"not JSON":         `{`,
		"not OpenAPI 3":    `{"openapi":"2.0"}`,
		"no Problem":       `{"openapi":"3.1.0","paths":{}}`,
		"broken operation": `{"openapi":"3.1.0","paths":{"/x":{"get":5}},"components":{"schemas":{"Problem":{}}}}`,
		"no operation id":  `{"openapi":"3.1.0","paths":{"/x":{"get":{"responses":{"200":{}}}}},"components":{"schemas":{"Problem":{}}}}`,
		"no response":      `{"openapi":"3.1.0","paths":{"/x":{"get":{"operationId":"x"}}},"components":{"schemas":{"Problem":{}}}}`,
		"no operation":     `{"openapi":"3.1.0","paths":{"/x":{"parameters":[]}},"components":{"schemas":{"Problem":{}}}}`,
		"bad pattern":      `{"openapi":"3.1.0","paths":{"/x":{"get":{"operationId":"x","parameters":[{"name":"q","in":"query","schema":{"type":"string","pattern":"("}}],"responses":{"200":{}}}}},"components":{"schemas":{"Problem":{}}}}`,
	} {
		if _, err := Load([]byte(src)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	d := load(t, sample)
	if ids := []string{d.Operations()[0].ID, d.Operations()[1].ID}; !slices.Equal(ids, []string{"getPlain", "getThing"}) {
		t.Fatalf("operations %v", ids)
	}
}

// TestCheckValidatesEveryPart: a removed member, an extra member, an
// undocumented status, an undocumented header and a wrong media type each
// fail (TEST-003).
func TestCheckValidatesEveryPart(t *testing.T) {
	req.Covers(t, "TEST-003", "TST-015", "ERR-001")
	d := load(t, sample)
	ok := hdr("Content-Type", "application/json; charset=utf-8", "X-Count", "2", "X-Request-Id", "r1", "Cache-Control", "no-store")
	if err := d.Check("GET", "/things/t1?radius_m=5", 200, ok, []byte(thing)); err != nil {
		t.Fatalf("a valid response failed: %v", err)
	}
	problem := hdr("Content-Type", ProblemMediaType, "Retry-After", "3", "Cache-Control", "no-store")
	for name, c := range map[string]struct {
		method, target string
		status         int
		h              http.Header
		body           string
		want           string
	}{
		"removed member":        {"GET", "/things/t1", 200, ok, `{"id":"t1"}`, "does not match"},
		"extra member":          {"GET", "/things/t1", 200, ok, `{"id":"t1","sizeM":3,"table_name":"x"}`, "does not match"},
		"undocumented header":   {"GET", "/things/t1", 200, hdr("Content-Type", "application/json", "X-Internal", "1"), thing, "header X-Internal"},
		"header of wrong type":  {"GET", "/things/t1", 200, hdr("Content-Type", "application/json", "X-Count", "many"), thing, "header X-Count"},
		"problem header on 200": {"GET", "/things/t1", 200, hdr("Content-Type", "application/json", "Retry-After", "1"), thing, "header Retry-After"},
		"undocumented media":    {"GET", "/things/t1", 200, hdr("Content-Type", "text/plain"), "x", "media type"},
		"body not JSON":         {"GET", "/things/t1", 200, hdr("Content-Type", "application/json"), "{", "not JSON"},
		"body where none":       {"GET", "/plain", 204, hdr(), "x", "documents no body"},
		"error not a problem":   {"GET", "/things/t1", 404, hdr("Content-Type", "application/json"), `{}`, "ERR-001"},
		"bad problem":           {"GET", "/plain", 500, problem, `{"type":"x"}`, "does not match"},
		"success off the map":   {"GET", "/elsewhere", 200, hdr(), "", "does not hold"},
		"empty path segment":    {"GET", "/things/", 200, hdr(), "", "does not hold"},
		"wrong method":          {"POST", "/plain", 204, hdr(), "", "does not hold"},
		"unparsable target":     {"GET", "%zz", 200, hdr(), "", "target"},
	} {
		err := d.Check(c.method, c.target, c.status, c.h, []byte(c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error containing %q", name, err, c.want)
		}
	}
	if err := d.Check("GET", "/plain", 503, problem, []byte(`{"type":"x","status":503}`)); err != nil {
		t.Errorf("a valid problem failed: %v", err)
	}
	if err := d.Check("GET", "/nowhere", 404, problem, []byte(`{"type":"x","status":404}`)); err != nil {
		t.Errorf("a problem off the map failed: %v", err)
	}
	if err := d.Check("GET", "/plain", 204, hdr(), nil); err != nil {
		t.Errorf("an empty documented answer failed: %v", err)
	}

	table, missing := d.Coverage()
	if missing != 3 || !strings.Contains(table, "| getThing | GET /things/{id} | 200 | yes |") || !strings.Contains(table, "| getThing | GET /things/{id} | 4XX | no |") ||
		!strings.Contains(table, "2 operations, 6 documented statuses, 3 covered, 3 not covered") {
		t.Fatalf("coverage (%d missing):\n%s", missing, table)
	}
}

func TestUndocumentedStatus(t *testing.T) {
	req.Covers(t, "TEST-003")
	d := load(t, `{"openapi":"3.1.0","paths":{"/x":{"get":{"operationId":"x","responses":{"200":{}}}}},"components":{"schemas":{"Problem":{}}}}`)
	if err := d.Check("GET", "/x", 500, hdr(), nil); err == nil || !strings.Contains(err.Error(), "status 500 is not documented for x") {
		t.Fatalf("an undocumented status: %v", err)
	}
}

func TestHeaderValue(t *testing.T) {
	req.Covers(t, "TEST-003")
	if headerValue(nil, "1") != "1" {
		t.Fatal("no schema: the raw string")
	}
}

func targets(rs []Request) map[string]Request {
	out := map[string]Request{}
	for _, r := range rs {
		out[r.Name] = r
	}
	return out
}

func TestInvalidRequestsOnePerConstraint(t *testing.T) {
	req.Covers(t, "TST-015", "NFR-SEC-002")
	got, err := load(t, sample).InvalidRequests()
	if err != nil {
		t.Fatal(err)
	}
	by := targets(got)
	want := map[string]string{
		"an unknown parameter":           "radius_m=5&zz_undeclared=1",
		"radius_m missing":               "",
		"radius_m repeated":              "radius_m=5&radius_m=5",
		"radius_m of the wrong type":     "radius_m=x",
		"radius_m below minimum":         "radius_m=0",
		"radius_m above maximum":         "radius_m=101",
		"kind outside the enumeration":   "radius_m=5&kind=zz_not_in_enum",
		"kind repeated":                  "radius_m=5&kind=a&kind=a",
		"code not matching the pattern":  "radius_m=5&code=~",
		"label not matching the pattern": "radius_m=5&label=~",
		"name too short":                 "radius_m=5&name=a",
		"name too long":                  "radius_m=5&name=aaaaaa",
		"name repeated":                  "radius_m=5&name=aa&name=aa",
		"flag of the wrong type":         "radius_m=5&flag=x",
		"flag repeated":                  "radius_m=5&flag=true&flag=true",
		"ratio at the exclusive minimum": "radius_m=5&ratio=0",
		"ratio at the exclusive maximum": "radius_m=5&ratio=1",
		"ratio of the wrong type":        "radius_m=5&ratio=x",
		"page repeated":                  "radius_m=5&page=1&page=1",
		"ratio repeated":                 "radius_m=5&ratio=0.5&ratio=0.5",
		"code repeated":                  "radius_m=5&code=DE&code=DE",
		"page of the wrong type":         "radius_m=5&page=x",
		"getPlain: an unknown parameter": "zz_undeclared=1",
	}
	for name, query := range want {
		lookup := name
		if strings.HasPrefix(name, "getPlain: ") {
			lookup = strings.TrimPrefix(name, "getPlain: ")
		}
		r, ok := by[lookup]
		if strings.HasPrefix(name, "getPlain") {
			r, ok = got[0], got[0].Operation.ID == "getPlain"
		}
		path := "/things/t1"
		if strings.HasPrefix(name, "getPlain") {
			path = "/plain"
		}
		u, _ := url.Parse(r.Target)
		if !ok || u.Path != path || u.RawQuery != query {
			t.Errorf("%s: %q, want %s?%s", name, r.Target, path, query)
		}
	}
	if _, ok := by["label repeated"]; ok {
		t.Error("a pattern-only parameter without an example cannot be repeated with a valid value")
	}
	if len(got) != len(want) {
		t.Errorf("%d requests, want %d", len(got), len(want))
	}
}

func TestGeneratorsFailClosed(t *testing.T) {
	req.Covers(t, "TST-011", "TST-015")
	wrap := func(params string) *Document {
		return load(t, `{"openapi":"3.1.0","paths":{"/x":{"get":{"operationId":"x","parameters":[`+params+`],"responses":{"200":{}}}}},"components":{"schemas":{"Problem":{}}}}`)
	}
	for name, params := range map[string]string{
		"required without a value": `{"name":"q","in":"query","required":true,"schema":{"type":"string","pattern":"^a$"}}`,
		"required without schema":  `{"name":"q","in":"query","required":true}`,
		"object without value":     `{"name":"q","in":"path","schema":{"type":"object"}}`,
	} {
		d := wrap(params)
		if _, err := d.InvalidRequests(); err == nil {
			t.Errorf("%s: invalid requests built", name)
		}
		if _, err := d.ExampleRequests(); err == nil {
			t.Errorf("%s: example requests built", name)
		}
		if _, err := d.Exercise(http.NotFoundHandler()); err == nil {
			t.Errorf("%s: exercised", name)
		}
	}
	for name, params := range map[string]string{
		"pattern admits all": `{"name":"q","in":"query","schema":{"type":"string","pattern":".*"}}`,
	} {
		if _, err := wrap(params).InvalidRequests(); err == nil {
			t.Errorf("%s: invalid requests built", name)
		}
	}
	d := wrap(`{"name":"q","in":"query","required":true,"schema":{"type":"integer"}}`)
	if _, err := d.ExampleRequests(); err == nil {
		t.Error("a required parameter without an example: example requests built")
	}
	if _, err := d.Exercise(http.NotFoundHandler()); err == nil {
		t.Error("a required parameter without an example: exercised")
	}
	if vs, err := violations(param{Name: "n"}); vs != nil || err != nil {
		t.Errorf("no schema: %v %v", vs, err)
	}
}

func TestExampleRequests(t *testing.T) {
	req.Covers(t, "TST-015")
	got, err := load(t, sample).ExampleRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Target != "/plain" || got[1].Target != "/things/t1?radius_m=5&code=DE" {
		t.Fatalf("examples %+v", got)
	}
}

// service answers the sample document: the documented example gets a Thing,
// /plain a 204, and anything else a problem listing every query parameter
// and radius_m. lenient also accepts radius_m out of range: the service
// change the negative suite must catch.
func service(lenient bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plain" && r.URL.RawQuery == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		q := r.URL.Query()
		if r.URL.RawQuery == "radius_m=5&code=DE" || (lenient && q.Get("radius_m") == "101" && len(q) == 1) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(thing))
			return
		}
		var errs []map[string]string
		for k := range q {
			errs = append(errs, map[string]string{"parameter": "query." + k})
		}
		errs = append(errs, map[string]string{"parameter": "radius_m"})
		w.Header().Set("Content-Type", ProblemMediaType)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://x.example/problems/invalid-parameter", "status": 400, "errors": errs})
	})
}

// TestExerciseCatchesALenientService: a service change that accepts an
// out-of-range value the document forbids is caught.
func TestExerciseCatchesALenientService(t *testing.T) {
	req.Covers(t, "TST-015", "NFR-SEC-002", "ERR-005")
	d := load(t, sample)
	rep, err := d.Exercise(service(false))
	if err != nil || len(rep.Findings) != 0 {
		t.Fatalf("a conforming service: %v %v", err, rep.Findings)
	}
	if s := rep.String(); s != "23 invalid requests refused as documented, 2 example requests accepted, 0 finding(s)" {
		t.Fatalf("summary %q", s)
	}
	rep, _ = d.Exercise(service(true))
	if len(rep.Findings) != 1 || !strings.Contains(rep.Findings[0].Error(), "radius_m above maximum") || !strings.HasPrefix(rep.String(), "22 invalid") {
		t.Fatalf("a lenient service: %v (%s)", rep.Findings, rep)
	}
}

func TestExerciseReportsExampleFailures(t *testing.T) {
	req.Covers(t, "TST-015")
	d := load(t, sample)
	refuseAll := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ProblemMediaType)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"x","status":400}`))
	})
	rep, err := d.Exercise(refuseAll)
	if err != nil {
		t.Fatal(err)
	}
	examples := slices.DeleteFunc(slices.Clone(rep.Findings), func(e error) bool { return !strings.HasPrefix(e.Error(), "example: ") })
	if len(examples) != 2 || !strings.Contains(examples[0].Error(), "answered 400") {
		t.Fatalf("example findings %v", examples)
	}
	broken := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rep, _ = d.Exercise(broken)
	if !strings.HasPrefix(rep.String(), "0 invalid requests refused as documented, 0 example") {
		t.Fatalf("an undocumented answer to everything: %s", rep)
	}
	if countPrefix([]error{errors.New("other")}, "invalid") != 0 {
		t.Fatal("countPrefix counted an unrelated error")
	}
}

func TestValidOfTakesTheMinimum(t *testing.T) {
	req.Covers(t, "TST-015")
	low := 3.0
	if v, ok := validOf(param{Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: &low}}); v != "3" || !ok {
		t.Fatalf("validOf = %q %v", v, ok)
	}
}
