package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Authenticator is the port keyed operations call.
type Authenticator interface {
	Authenticate(ctx context.Context, presented, client string) (auth.Principal, error)
}

// Security scheme names of the OpenAPI document (SEC-001).
const (
	schemeBearer = "bearerKey"
	schemeHeader = "headerKey"
	// APIKeyHeader is the alternative header a key may be presented in.
	APIKeyHeader = "X-API-Key" // #nosec G101 -- a header name, not a credential
	// scopeExtension carries an operation's required scope in the
	// document (SEC-020).
	scopeExtension = "x-required-scope"
)

// queryKeyNames are parameter names that only ever carry a credential.
var queryKeyNames = []string{"api_key", "apikey", "access_token", "key"}

// refuseKeyInQuery answers 400 key-in-query when the query string carries
// something shaped like a key, or a parameter named for one; the key is never
// checked (SEC-002). It runs before the operation's parameter checks.
func refuseKeyInQuery(p access.Prefixes) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for name, vs := range r.URL.Query() {
				hit := p.Carries(name)
				for _, n := range queryKeyNames {
					hit = hit || strings.EqualFold(name, n)
				}
				for _, v := range vs {
					hit = hit || p.Carries(v)
				}
				if hit {
					writeProblem(w, newProblem(r.Context(), SlugKeyInQuery, "A key must never be sent in the query string; send it in the Authorization header. Treat this key as exposed and rotate it.", 0))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

type principalKey struct{}

// principalFrom returns the authenticated key of a keyed operation.
func principalFrom(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(principalKey{}).(auth.Principal)
	return p
}

// presented returns the key of the request: the bearer token, else the
// X-API-Key header.
func presented(ctx huma.Context) string {
	if h := ctx.Header("Authorization"); h != "" {
		scheme, token, _ := strings.Cut(h, " ")
		if strings.EqualFold(scheme, "Bearer") {
			return strings.TrimSpace(token)
		}
		return ""
	}
	return strings.TrimSpace(ctx.Header(APIKeyHeader))
}

func clientAddress(ctx huma.Context) string {
	host, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		return ctx.RemoteAddr()
	}
	return host
}

// challenge is the WWW-Authenticate value of a 401 answer.
func challenge(errCode string) http.Header {
	v := `Bearer realm="viafrei-api"`
	if errCode != "" {
		v += `, error="` + errCode + `"`
	}
	return http.Header{"Www-Authenticate": {v}}
}

// authProblem maps a refusal of the authenticator to its problem.
func authProblem(ctx context.Context, err error, retryAfter int) *Problem {
	var p *Problem
	switch {
	case errors.Is(err, access.ErrMissing):
		p = newProblem(ctx, SlugMissingKey, "This operation needs an API key in the Authorization header.", 0)
		p.headers = challenge("")
	case errors.Is(err, access.ErrExpired):
		p = newProblem(ctx, SlugKeyExpired, "The key has expired; ask for a new one.", 0)
		p.headers = challenge("invalid_token")
	case errors.Is(err, access.ErrRevoked):
		p = newProblem(ctx, SlugKeyRevoked, "The key has been revoked.", 0)
		p.headers = challenge("invalid_token")
	case errors.Is(err, auth.ErrLimited):
		p = newProblem(ctx, SlugRateLimited, "Too many failed authentication attempts from this address.", retryAfter)
	case errors.Is(err, auth.ErrUnavailable):
		p = newProblem(ctx, SlugUpstreamUnavailable, "Key checks are unavailable.", 1)
	default:
		p = newProblem(ctx, SlugInvalidKey, "The key is not valid.", 0)
		p.headers = challenge("invalid_token")
	}
	return p
}

// authenticate runs before every keyed operation: an operation that declares
// a security requirement needs a valid key, and the scope it declares
// (SEC-001, SEC-020). Without an access store, keyed operations are off.
func (s *server) authenticate(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if len(op.Security) == 0 {
			next(ctx)
			return
		}
		var p *Problem
		if s.auth == nil {
			p = newProblem(ctx.Context(), SlugFeatureDisabled, "Keyed operations are switched off on this instance.", 0)
		} else {
			principal, err := s.auth.Authenticate(ctx.Context(), presented(ctx), clientAddress(ctx))
			scope, _ := op.Metadata[scopeExtension].(access.Scope)
			switch {
			case err != nil:
				p = authProblem(ctx.Context(), err, int(s.cfg.Auth.FailureWindow.Seconds()))
			case scope != "" && !principal.Key.Plan.Has(scope):
				p = newProblem(ctx.Context(), SlugScopeDenied, "The key's plan does not include the scope "+string(scope)+".", 0)
			default:
				next(huma.WithValue(ctx, principalKey{}, principal))
				return
			}
		}
		for k, vs := range p.headers {
			for _, v := range vs {
				ctx.AppendHeader(k, v)
			}
		}
		_ = huma.WriteErr(api, ctx, p.Status, p.Detail, p)
	}
}

// gateViolations counts answers aborted by the licence gate's post-check. It
// must stay zero; any increase is a defect to alert on (SEC-032).
var gateViolations atomic.Int64

// GateViolations returns the number of answers the post-check aborted.
func GateViolations() int64 { return gateViolations.Load() }

// keyedResponses documents the refusals every keyed operation may answer
// besides its own: a 401 carries the WWW-Authenticate challenge (RFC 6750).
func keyedResponses(api huma.API) map[string]*huma.Response {
	reg := api.OpenAPI().Components.Schemas
	problem := map[string]*huma.MediaType{ProblemContentType: {Schema: reg.Schema(reflect.TypeFor[Problem](), true, "Problem")}}
	return map[string]*huma.Response{
		"401": {
			Description: "No key, or a key that is invalid, expired or revoked.",
			Headers:     map[string]*huma.Param{"WWW-Authenticate": {Schema: &huma.Schema{Type: huma.TypeString}}},
			Content:     problem,
		},
		"default": {Description: "Error", Content: problem},
	}
}
