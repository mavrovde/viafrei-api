package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// RequestIDHeader carries the request id in both directions.
const RequestIDHeader = "X-Request-Id"

type ctxKey struct{}

// requestContext is what every answer needs to know about its request.
type requestContext struct {
	id      string
	path    string
	baseURL string
}

func requestFrom(ctx context.Context) requestContext {
	rc, _ := ctx.Value(ctxKey{}).(requestContext)
	return rc
}

// RequestIDFrom returns the request id carried by ctx.
func RequestIDFrom(ctx context.Context) string { return requestFrom(ctx).id }

var clientRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// withRequest assigns the request id (keeping a well-formed client value),
// records the path and base URL, and echoes the id on the answer.
func withRequest(baseURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !clientRequestID.MatchString(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), ctxKey{}, requestContext{id: id, path: r.URL.Path, baseURL: baseURL})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func newRequestID() string { return uuid.Must(uuid.NewV7()).String() }

// securityHeaders sets the headers every answer carries (NFR-SEC-001). It
// runs first, so a problem written by any later middleware carries them too.
// The service answers JSON only and serves no cross-origin requests yet, so
// the CSP and CORP values apply to every answer.
func securityHeaders(hstsMaxAge time.Duration) func(http.Handler) http.Handler {
	hsts := "max-age=" + strconv.FormatInt(int64(hstsMaxAge/time.Second), 10) + "; includeSubDomains"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Strict-Transport-Security", hsts)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Resource-Policy", "same-site")
			next.ServeHTTP(w, r)
		})
	}
}

// limitURI answers 414 uri-too-long above server.max_uri_bytes (PROT-006).
func limitURI(maxBytes int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.RequestURI) > maxBytes {
				writeProblem(w, newProblem(r.Context(), SlugURITooLong, "The request URI is longer than the service accepts.", 0))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// withDeadline bounds every request by deadline.request (NFR-PERF-001). The
// store derives the statement timeout from this deadline and cancels the
// statement on the server when it passes.
func withDeadline(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// recoverPanics turns a panic into 500 internal; the details go to the log
// only (ERR-008).
func recoverPanics(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { //nolint:contextcheck // false positive on a deferred recover; r.Context() is passed below
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
						panic(v)
					}
					log.ErrorContext(r.Context(), "panic", "requestId", RequestIDFrom(r.Context()), "panic", v)
					writeProblem(w, newProblem(r.Context(), SlugInternal, "An unexpected fault occurred; quote the request id.", 0))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// accessLog writes one structured line per request. The query string is not
// logged: it may carry a key presented by mistake.
func accessLog(log *slog.Logger, now func() time.Time) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			log.Info("request",
				"requestId", RequestIDFrom(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"durationMs", now().Sub(start).Milliseconds(),
			)
		})
	}
}
