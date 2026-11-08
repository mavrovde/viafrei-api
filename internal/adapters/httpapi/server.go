// Package httpapi is the HTTP adapter: the router, the operations, the
// envelope, problem details and the OpenAPI document generated from code.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/invariant"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// Title and InterfaceVersion describe the OpenAPI document. The interface
// version is the version of the v1 description, not of the build: it stays
// fixed across builds so the committed document is byte-stable (FR-SVC-005).
const (
	Title            = "ViaFrei API"
	InterfaceVersion = "1.0.0-draft"
	OpenAPIMediaType = "application/vnd.oai.openapi+json;version=3.1"
)

// Readiness is the port the readiness operation calls.
type Readiness interface {
	Check(ctx context.Context) health.Result
}

// Deps are the collaborators of the HTTP adapter.
type Deps struct {
	Config          *config.Config
	Readiness       Readiness
	Logger          *slog.Logger
	Build           buildinfo.Info
	ContractVersion string
	Now             func() time.Time
}

type server struct {
	cfg             *config.Config
	ready           Readiness
	log             *slog.Logger
	build           buildinfo.Info
	contractVersion string
	now             func() time.Time
}

// operation is one registered API operation.
type operation struct {
	id       string
	method   string
	path     string
	register func(api huma.API, s *server, op huma.Operation)
}

func operations() []operation {
	return []operation{
		{id: "getHealth", method: http.MethodGet, path: "/healthz", register: registerHealth},
		{id: "getReadiness", method: http.MethodGet, path: "/readyz", register: registerReadiness},
		{id: "getMeta", method: http.MethodGet, path: "/v1/meta", register: registerMeta},
	}
}

// OperationIDs lists the ids accepted by endpoints.disabled.
func OperationIDs() []string {
	ops := operations()
	ids := make([]string, len(ops))
	for i, op := range ops {
		ids[i] = op.id
	}
	return ids
}

// NewHandler builds the complete HTTP handler.
func NewHandler(d Deps) (http.Handler, error) {
	h, _, err := build(d)
	return h, err
}

// OpenAPI returns the OpenAPI document the handler built from cfg would
// serve. `viafrei-api openapi` writes it to api/openapi.json.
func OpenAPI(cfg *config.Config) ([]byte, error) {
	_, doc, err := build(Deps{Config: cfg})
	return doc, err
}

// NewServer builds the http.Server with every server.* limit applied.
func NewServer(d Deps) (*http.Server, error) {
	h, err := NewHandler(d)
	if err != nil {
		return nil, err
	}
	sc := d.Config.Server
	return &http.Server{
		Addr:              sc.ListenAddr,
		Handler:           h,
		ReadHeaderTimeout: sc.ReadHeaderTimeout,
		ReadTimeout:       sc.ReadTimeout,
		WriteTimeout:      sc.WriteTimeout,
		IdleTimeout:       sc.IdleTimeout,
		MaxHeaderBytes:    sc.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger(d).Handler(), slog.LevelWarn),
	}, nil
}

func logger(d Deps) *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func build(d Deps) (http.Handler, []byte, error) {
	if d.Config == nil {
		return nil, nil, errors.New("httpapi: no configuration")
	}
	hookHumaErrors()
	s := &server{
		cfg:             d.Config,
		ready:           d.Readiness,
		log:             logger(d),
		build:           d.Build,
		contractVersion: d.ContractVersion,
		now:             d.Now,
	}
	if s.now == nil {
		s.now = time.Now
	}
	ops := operations()
	disabled := map[string]bool{}
	for _, id := range d.Config.Endpoints.Disabled {
		if !slices.Contains(OperationIDs(), id) {
			return nil, nil, fmt.Errorf("endpoints.disabled: %q is not an operation id (known: %s)", id, strings.Join(OperationIDs(), ", "))
		}
		disabled[id] = true
	}

	base := strings.TrimRight(d.Config.Server.PublicBaseURL, "/")
	router := chi.NewRouter()
	// recoverPanics runs inside withRequest, so a panic's problem and log
	// line carry the request id, the instance and the absolute type, and
	// inside accessLog, so the 500 it writes is logged like any answer.
	router.Use(
		securityHeaders(d.Config.HTTP.HSTSMaxAge),
		withRequest(base),
		accessLog(s.log, s.now),
		recoverPanics(s.log),
		limitURI(d.Config.Server.MaxURIBytes),
		withDeadline(d.Config.Deadline.Request),
	)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, newProblem(r.Context(), SlugNotFound, "Nothing exists at this path.", 0))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, newProblem(r.Context(), SlugMethodNotAllowed, "This method is not supported on this path.", 0))
	})

	hc := huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:       Title,
				Version:     InterfaceVersion,
				Description: "Keyed, read-only access to live German mobility context. This document is generated from the code.",
				License:     &huma.License{Name: "Terms of use", URL: "https://viafrei.de/"},
				Contact:     &huma.Contact{Name: "ViaFrei API maintainers", URL: "https://github.com/mavrovde/viafrei-api/issues"},
			},
			Servers:    []*huma.Server{{URL: base}},
			Components: &huma.Components{Schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)},
			Tags:       []*huma.Tag{{Name: "service", Description: "Health, readiness and service metadata."}},
		},
		Formats:       huma.DefaultFormats,
		DefaultFormat: "application/json",
	}
	api := humachi.New(router, hc)
	api.UseMiddleware(strictQuery(api))

	for _, op := range ops {
		if disabled[op.id] {
			router.Method(op.method, op.path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeProblem(w, newProblem(r.Context(), SlugFeatureDisabled, "This operation is switched off.", 0))
			}))
			continue
		}
		op.register(api, s, huma.Operation{
			OperationID: op.id,
			Method:      op.method,
			Path:        op.path,
			Tags:        []string{"service"},
		})
	}

	// The document is a value of huma's own fixed types: marshalling it
	// cannot fail for any configuration.
	raw := invariant.Must(json.Marshal(api.OpenAPI()))
	var doc bytes.Buffer
	_ = json.Indent(&doc, raw, "", "  ") // raw is the output of json.Marshal
	doc.WriteByte('\n')
	served := doc.Bytes()

	if d.Config.Docs.Enabled {
		router.Get("/v1/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", OpenAPIMediaType)
			_, _ = w.Write(served)
		})
		router.Get("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/v1/openapi.json", http.StatusFound)
		})
	}
	return router, served, nil
}
