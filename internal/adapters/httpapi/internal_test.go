package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestPanicBecomesInternalProblem(t *testing.T) {
	req.Covers(t, "ERR-001")
	var logs bytes.Buffer
	h := recoverPanics(slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret detail")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "secret detail") {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	if !strings.Contains(logs.String(), "secret detail") {
		t.Fatalf("the panic is not in the log: %s", logs.String())
	}
}

func TestAbortHandlerPanicPassesThrough(t *testing.T) {
	req.Covers(t, "ERR-001")
	h := recoverPanics(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // identity, as net/http compares it
			t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil))
	t.Fatal("the abort panic was swallowed")
}

// TestPanicAfterWriteDoesNotWriteAgain: once the handler has started its
// answer, a panic aborts the connection instead of appending a problem.
func TestPanicAfterWriteDoesNotWriteAgain(t *testing.T) {
	req.Covers(t, "ERR-001")
	for name, body := range map[string]string{"after WriteHeader": "", "after Write": `{"data":`} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			h := recoverPanics(slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body))
				panic("late fault")
			}))
			w := httptest.NewRecorder()
			func() {
				defer func() {
					if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // identity, as net/http compares it
						t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
					}
				}()
				h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil))
			}()
			if w.Code != http.StatusOK || w.Body.String() != body {
				t.Fatalf("a second answer was written: status %d, body %q", w.Code, w.Body)
			}
			if !strings.Contains(logs.String(), "late fault") || !strings.Contains(logs.String(), `"answerStarted":true`) {
				t.Fatalf("the late panic is not in the log: %s", logs.String())
			}
		})
	}
}

func TestWriteTrackerUnwraps(t *testing.T) {
	w := httptest.NewRecorder()
	if (&writeTracker{ResponseWriter: w}).Unwrap() != w {
		t.Fatal("Unwrap does not return the wrapped writer")
	}
}

func TestStatusRecorderUnwraps(t *testing.T) {
	w := httptest.NewRecorder()
	if (&statusRecorder{ResponseWriter: w}).Unwrap() != w {
		t.Fatal("Unwrap does not return the wrapped writer")
	}
}

func TestProblemImplementsHumaErrors(t *testing.T) {
	req.Covers(t, "ERR-001")
	p := newProblem(context.Background(), SlugUpstreamUnavailable, "down", 3)
	if p.Error() != "Upstream unavailable: down" || p.GetStatus() != http.StatusServiceUnavailable || p.GetHeaders().Get("Retry-After") != "3" {
		t.Fatalf("problem = %q %d %v", p.Error(), p.GetStatus(), p.GetHeaders())
	}
	w := httptest.NewRecorder()
	writeProblem(w, p)
	if w.Header().Get("Retry-After") != "3" || w.Header().Get("Content-Type") != ProblemContentType {
		t.Fatalf("headers = %v", w.Header())
	}
}

func TestUnknownSlugIsInternal(t *testing.T) {
	req.Covers(t, "ERR-001")
	if p := newProblem(context.Background(), "no-such-slug", "x", 0); p.Status != http.StatusInternalServerError || !strings.HasSuffix(p.Type, "/internal") {
		t.Fatalf("problem = %+v", p)
	}
}

func TestHumaErrorsBecomeProblems(t *testing.T) {
	req.Covers(t, "ERR-001")
	hookHumaErrors()
	cases := map[int]string{
		http.StatusBadRequest:            SlugInvalidParameter,
		http.StatusUnprocessableEntity:   SlugInvalidParameter,
		http.StatusNotFound:              SlugNotFound,
		http.StatusMethodNotAllowed:      SlugMethodNotAllowed,
		http.StatusNotAcceptable:         SlugNotAcceptable,
		http.StatusRequestEntityTooLarge: SlugPayloadTooLarge,
		http.StatusUnsupportedMediaType:  SlugUnsupportedMediaType,
		http.StatusServiceUnavailable:    SlugUpstreamUnavailable,
		http.StatusTeapot:                SlugInternal,
	}
	for status, slug := range cases {
		err := huma.NewErrorWithContext(nil, status, "internal message", &huma.ErrorDetail{Location: "query.limit", Message: "too big"}, errors.New("plain"))
		p, ok := err.(*Problem)
		if !ok || !strings.HasSuffix(p.Type, "/"+slug) || strings.Contains(p.Detail, "internal message") {
			t.Errorf("status %d: %+v", status, err)
		}
	}
	p, _ := huma.NewErrorWithContext(nil, http.StatusBadRequest, "m", &huma.ErrorDetail{Location: "query.limit", Message: "too big"}, errors.New("plain")).(*Problem)
	want := []ParamError{{Parameter: "query.limit", Code: "invalid", Detail: "too big"}, {Code: "invalid", Detail: "invalid value"}}
	got, _ := json.Marshal(p.Errors)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("errors = %s, want %s", got, exp)
	}
}

func TestHumaErrorCarriesTheRequest(t *testing.T) {
	req.Covers(t, "ERR-001")
	hookHumaErrors()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/meta", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, requestContext{id: "req-12345678", path: "/v1/meta", baseURL: "https://example.test"}))
	hctx := humachi.NewContext(&huma.Operation{}, r, httptest.NewRecorder())
	p, _ := huma.NewErrorWithContext(hctx, http.StatusNotFound, "m").(*Problem)
	if p == nil || p.RequestID != "req-12345678" || p.Type != "https://example.test/problems/not-found" {
		t.Fatalf("problem = %+v", p)
	}
}

func TestServerKeepsTheGivenLogger(t *testing.T) {
	var logs bytes.Buffer
	srv, err := NewServer(Deps{Config: config.Defaults(), Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	srv.ErrorLog.Print("from net/http")
	if !strings.Contains(logs.String(), "from net/http") {
		t.Fatalf("the server's error log does not reach the given logger: %q", logs.String())
	}
	ctx, cancel := ShutdownContext(config.Defaults())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("the shutdown context has no deadline")
	}
}

func TestBuildNeedsAConfiguration(t *testing.T) {
	if _, err := NewServer(Deps{}); err == nil {
		t.Fatal("a server without configuration was built")
	}
	if _, err := OpenAPI(nil); err == nil {
		t.Fatal("a document without configuration was built")
	}
}

func TestServerAppliesDefaultsWithoutALogger(t *testing.T) {
	srv, err := NewServer(Deps{Config: config.Defaults()})
	if err != nil || srv.ErrorLog == nil {
		t.Fatalf("server = %v, %v", srv, err)
	}
	doc, err := OpenAPI(config.Defaults())
	if err != nil || !json.Valid(doc) {
		t.Fatalf("OpenAPI = %d bytes, %v", len(doc), err)
	}
}

// TestStrictQueryRefusesARepeatedDeclaredParameter: a declared parameter
// given twice is a violation of its own (ERR-005, NFR-SEC-002).
func TestStrictQueryRefusesARepeatedDeclaredParameter(t *testing.T) {
	req.Covers(t, "ERR-005", "NFR-SEC-002")
	hookHumaErrors()
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("t", "1"))
	api.UseMiddleware(strictQuery(api))
	type in struct {
		Radius int `query:"radius_m"`
	}
	huma.Register(api, huma.Operation{OperationID: "t", Method: http.MethodGet, Path: "/t"}, func(_ context.Context, _ *in) (*struct{}, error) {
		return nil, nil
	})
	for target, want := range map[string]int{"/t?radius_m=1": http.StatusNoContent, "/t?radius_m=1&radius_m=2": http.StatusBadRequest} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
		if w.Code != want {
			t.Fatalf("%s: %d %s", target, w.Code, w.Body)
		}
		if want == http.StatusBadRequest && !strings.Contains(w.Body.String(), `"parameter":"query.radius_m","code":"invalid","detail":"repeated parameter"`) {
			t.Fatalf("%s: %s", target, w.Body)
		}
	}
}
