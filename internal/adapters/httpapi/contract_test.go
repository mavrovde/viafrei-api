package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/oascheck"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The committed document is the contract every handler test is held to
// (TEST-003): do() validates each response it receives against it.
var (
	docOnce sync.Once
	doc     *oascheck.Document
	docErr  error
)

func contract(t *testing.T) *oascheck.Document {
	t.Helper()
	docOnce.Do(func() {
		var raw []byte
		if raw, docErr = os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.json")); docErr == nil {
			doc, docErr = oascheck.Load(raw)
		}
	})
	if docErr != nil {
		t.Fatal(docErr)
	}
	return doc
}

// documentRoutes serve the document itself and are not operations of it.
var documentRoutes = []string{"/v1/openapi.json", "/openapi.json"}

// checkContract validates one response of a handler test.
func checkContract(t *testing.T, method, target string, w *httptest.ResponseRecorder) {
	t.Helper()
	path, _, _ := strings.Cut(target, "?")
	if slices.Contains(documentRoutes, path) {
		return
	}
	if err := contract(t).Check(method, target, w.Code, w.Header(), w.Body.Bytes()); err != nil {
		t.Errorf("contract (TEST-003): %v", err)
	}
}

// TestMain prints the operation × status coverage of the handler tests.
func TestMain(m *testing.M) {
	code := m.Run()
	if doc != nil {
		table, _ := doc.Coverage()
		fmt.Println("OpenAPI conformance coverage (handler tests):")
		fmt.Print(table)
	}
	os.Exit(code)
}

// TestContractInBothDirections sends every request the document declares
// invalid and every documented example request to the service (TST-015).
func TestContractInBothDirections(t *testing.T) {
	req.Covers(t, "TST-015", "NFR-SEC-002", "ERR-005", "TEST-003")
	d := contract(t)
	for name, ready := range map[string]health.Result{"ready": {Ready: true}, "not ready": {}} {
		t.Run(name, func(t *testing.T) {
			rep, err := d.Exercise(contractHandler(t, &fakeReadiness{result: ready}))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range rep.Findings {
				t.Error(f)
			}
			if rep.Invalid < len(d.Operations()) || rep.Examples != len(d.Operations()) {
				t.Fatalf("generated %d invalid and %d example requests for %d operations", rep.Invalid, rep.Examples, len(d.Operations()))
			}
			t.Log(rep)
		})
	}
}

// contractHandler admits every request with a key of every feature scope, so
// that each keyed operation reaches its parameter checks and its examples are
// answered by a feature fake.
func contractHandler(t *testing.T, ready httpapi.Readiness) http.Handler {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: config.Defaults(), Readiness: ready, Build: buildinfo.Info{Version: "0.2.0-test", Commit: "abc1234"},
		ContractVersion: "0.1", Now: func() time.Time { return fixedNow },
		Auth: &fakeAuth{p: featureScopes()}, Sources: &fakeSources{register: fixtureRegister}, Features: everyAnswer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func contractBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestUnknownAndRepeatedParametersAreListed: every violation is listed, not
// only the first (ERR-005).
func TestUnknownAndRepeatedParametersAreListed(t *testing.T) {
	req.Covers(t, "ERR-005", "NFR-SEC-002")
	w := do(t, newHandler(t, config.Defaults(), nil), http.MethodGet, "/v1/meta?b=1&a=2&a=3", nil)
	p := assertProblem(t, w, http.StatusBadRequest, "invalid-parameter")
	errs, _ := p["errors"].([]any)
	if len(errs) != 2 {
		t.Fatalf("errors %v, want one per unknown parameter", p["errors"])
	}
	for i, want := range []string{"query.a", "query.b"} {
		if e, _ := errs[i].(map[string]any); e["parameter"] != want || e["detail"] != "unknown parameter" {
			t.Errorf("errors[%d] = %v, want %s unknown", i, errs[i], want)
		}
	}
}

// TestContractCheckCanSayNo: on a real answer of the service, a removed
// member, an undocumented status and an undocumented header each fail the
// check that the handler tests run (TEST-003).
func TestContractCheckCanSayNo(t *testing.T) {
	req.Covers(t, "TEST-003", "ENV-001", "ENV-002")
	d := contract(t)
	w := do(t, newHandler(t, config.Defaults(), nil), http.MethodGet, "/v1/meta", nil)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	meta, _ := body["meta"].(map[string]any)
	delete(meta, "requestId")
	removed, _ := json.Marshal(body)
	extra := w.Header().Clone()
	extra.Set("X-Internal-Node", "n1")
	for name, c := range map[string]struct {
		status int
		h      http.Header
		body   []byte
	}{
		"a removed member":        {http.StatusOK, w.Header(), removed},
		"an undocumented status":  {http.StatusCreated, w.Header(), w.Body.Bytes()},
		"a success under default": {http.StatusAccepted, w.Header(), w.Body.Bytes()},
		"an undocumented header":  {http.StatusOK, extra, w.Body.Bytes()},
	} {
		err := d.Check(http.MethodGet, "/v1/meta", c.status, c.h, c.body)
		if err == nil {
			t.Errorf("%s passed the contract check", name)
		} else if c.status != http.StatusOK && !strings.Contains(err.Error(), "is not documented for getMeta") {
			t.Errorf("%s failed for another reason: %v", name, err)
		}
	}
}
