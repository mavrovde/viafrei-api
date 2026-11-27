package httpapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
)

// headAsGet answers HEAD as GET without a body (RFC 9110 9.3.2): the route,
// the gates, the headers and the status are the GET's, the body is dropped.
// A path with no GET answers HEAD as it answers GET: 404, or 405 with Allow.
func headAsGet(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		g := r.Clone(r.Context())
		g.Method = http.MethodGet
		next.ServeHTTP(bodiless{w}, g)
	})
}

// bodiless writes the status and the headers of an answer, never its body.
type bodiless struct{ http.ResponseWriter }

func (b bodiless) Write(p []byte) (int, error) { return len(p), nil }

// methods are the methods an Allow field may name, in its order.
var methods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// allowed is the Allow value of the path on the router (RFC 9110 10.2.1):
// every method a route serves there, HEAD wherever GET is.
func allowed(router chi.Routes, path string) string {
	var out []string
	for _, m := range methods {
		if m != http.MethodHead && router.Match(chi.NewRouteContext(), m, path) {
			out = append(out, m)
		}
	}
	if slices.Contains(out, http.MethodGet) {
		out = slices.Insert(out, 1, http.MethodHead)
	}
	return strings.Join(out, ", ")
}
