package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// ProblemContentType is the media type of every error answer (ERR-001).
const ProblemContentType = "application/problem+json"

// Problem is an RFC 9457 problem details object with the project's
// extension members (Spec: Errors).
type Problem struct {
	Type       string       `json:"type" doc:"Stable URI of the problem type; resolves to its documentation page."`
	Title      string       `json:"title" doc:"Short title, fixed per type."`
	Status     int          `json:"status" doc:"Equal to the HTTP status."`
	Detail     string       `json:"detail" doc:"What went wrong in this occurrence."`
	Instance   string       `json:"instance" doc:"The request path without the query string."`
	RequestID  string       `json:"requestId" doc:"Equal to the X-Request-Id header."`
	Retryable  bool         `json:"retryable" doc:"Whether repeating the same request may succeed."`
	RetryAfter *int         `json:"retryAfter,omitempty" doc:"Seconds, equal to Retry-After, where applicable."`
	Errors     []ParamError `json:"errors,omitempty" doc:"One entry per violation, for invalid-parameter."`

	headers http.Header
}

// ParamError is one violation inside an invalid-parameter problem.
type ParamError struct {
	Parameter string `json:"parameter,omitempty"`
	Pointer   string `json:"pointer,omitempty"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
}

// Error implements error.
func (p *Problem) Error() string { return p.Title + ": " + p.Detail }

// GetStatus implements huma.StatusError.
func (p *Problem) GetStatus() int { return p.Status }

// ContentType implements huma.ContentTypeFilter.
func (p *Problem) ContentType(string) string { return ProblemContentType }

// GetHeaders implements huma.HeadersError.
func (p *Problem) GetHeaders() http.Header { return p.headers }

// problemKind is one entry of the closed catalogue (ERR-003).
type problemKind struct {
	status    int
	title     string
	retryable bool
}

// Problem type slugs used by this release.
const (
	SlugInvalidParameter     = "invalid-parameter"
	SlugNotFound             = "not-found"
	SlugFeatureDisabled      = "feature-disabled"
	SlugMethodNotAllowed     = "method-not-allowed"
	SlugNotAcceptable        = "not-acceptable"
	SlugPayloadTooLarge      = "payload-too-large"
	SlugURITooLong           = "uri-too-long"
	SlugUnsupportedMediaType = "unsupported-media-type"
	SlugInternal             = "internal"
	SlugDeadlineExhausted    = "deadline-exhausted"
	SlugUpstreamUnavailable  = "upstream-unavailable"
	SlugNotReady             = "not-ready"
)

var catalogue = map[string]problemKind{
	SlugInvalidParameter:     {http.StatusBadRequest, "Invalid parameter", false},
	SlugNotFound:             {http.StatusNotFound, "Not found", false},
	SlugFeatureDisabled:      {http.StatusNotFound, "Feature disabled", false},
	SlugMethodNotAllowed:     {http.StatusMethodNotAllowed, "Method not allowed", false},
	SlugNotAcceptable:        {http.StatusNotAcceptable, "Not acceptable", false},
	SlugPayloadTooLarge:      {http.StatusRequestEntityTooLarge, "Payload too large", false},
	SlugURITooLong:           {http.StatusRequestURITooLong, "URI too long", false},
	SlugUnsupportedMediaType: {http.StatusUnsupportedMediaType, "Unsupported media type", false},
	SlugInternal:             {http.StatusInternalServerError, "Internal error", true},
	SlugDeadlineExhausted:    {http.StatusServiceUnavailable, "Deadline exhausted", true},
	SlugUpstreamUnavailable:  {http.StatusServiceUnavailable, "Upstream unavailable", true},
	SlugNotReady:             {http.StatusServiceUnavailable, "Not ready", true},
}

// newProblem builds a problem of a catalogued type for the request in ctx.
// retryAfter > 0 sets Retry-After and retryAfter together (ERR-007).
func newProblem(ctx context.Context, slug, detail string, retryAfter int) *Problem {
	kind, ok := catalogue[slug]
	if !ok {
		slug, kind = SlugInternal, catalogue[SlugInternal]
	}
	rc := requestFrom(ctx)
	p := &Problem{
		Type:      rc.baseURL + "/problems/" + slug,
		Title:     kind.title,
		Status:    kind.status,
		Detail:    detail,
		Instance:  rc.path,
		RequestID: rc.id,
		Retryable: kind.retryable,
	}
	if retryAfter > 0 {
		p.RetryAfter = &retryAfter
		p.headers = http.Header{"Retry-After": {strconv.Itoa(retryAfter)}}
	}
	return p
}

// writeProblem writes a problem outside huma (router-level answers).
func writeProblem(w http.ResponseWriter, p *Problem) {
	for k, vs := range p.headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// hookHumaErrors makes huma's own error answers (validation, negotiation,
// panics) project problems. huma keeps these hooks in package variables; the
// values set here depend only on the request context, so every server built in
// one process shares them safely.
func hookHumaErrors() {
	// A list in an answer is an empty array when there is nothing, never null.
	huma.DefaultArrayNullable = false
	huma.NewErrorWithContext = func(hctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		ctx := context.Background()
		if hctx != nil {
			ctx = hctx.Context()
		}
		var p *Problem
		switch status {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			p = newProblem(ctx, SlugInvalidParameter, "One or more parameters are invalid.", 0)
			for _, e := range errs {
				pe := ParamError{Code: "invalid", Detail: "invalid value"}
				if d, ok := e.(huma.ErrorDetailer); ok {
					det := d.ErrorDetail()
					pe.Parameter, pe.Detail = det.Location, det.Message
				}
				p.Errors = append(p.Errors, pe)
			}
		case http.StatusNotFound:
			p = newProblem(ctx, SlugNotFound, "Nothing exists at this path.", 0)
		case http.StatusMethodNotAllowed:
			p = newProblem(ctx, SlugMethodNotAllowed, "This method is not supported on this path.", 0)
		case http.StatusNotAcceptable:
			p = newProblem(ctx, SlugNotAcceptable, "No offered media type is acceptable.", 0)
		case http.StatusRequestEntityTooLarge:
			p = newProblem(ctx, SlugPayloadTooLarge, "The request body is too large.", 0)
		case http.StatusUnsupportedMediaType:
			p = newProblem(ctx, SlugUnsupportedMediaType, "The request body must be application/json.", 0)
		case http.StatusServiceUnavailable:
			p = newProblem(ctx, SlugUpstreamUnavailable, "Storage is unavailable.", 1)
		default:
			// Never echo msg or errs: they may carry internals (ERR-004, ERR-008).
			p = newProblem(ctx, SlugInternal, "An unexpected fault occurred; quote the request id.", 0)
		}
		return p
	}
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return huma.NewErrorWithContext(nil, status, msg, errs...)
	}
}
