package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestRouteNamesOnlyARegisteredPattern: for a CONNECT request, the mux
// answers a /tree -> /tree/ redirect with the request's own path as the
// pattern. No console route has such a tree today (every pattern carries a
// method), so this plants one; route still logs "unmatched", because only a
// pattern the console registered is ever logged.
func TestRouteNamesOnlyARegisteredPattern(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-012")
	s := &server{mux: http.NewServeMux(), routes: map[string]string{}}
	const tree = "/tree/{x}/"
	s.mux.HandleFunc(tree, func(http.ResponseWriter, *http.Request) {})
	s.routes[tree] = tree

	r := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	r.Method = http.MethodConnect
	r.URL.Path = "/tree/FORGED-ENTRY"
	if _, pattern := s.mux.Handler(r); pattern != "/tree/FORGED-ENTRY/" {
		t.Fatalf("the mux no longer answers a CONNECT redirect with the path (%q); revisit this test", pattern)
	}
	if got := s.route(r); got != "unmatched" {
		t.Fatalf("route logged %q, want unmatched", got)
	}
	if got := method(r); got != "other" {
		t.Fatalf("method logged %q, want other", got)
	}
}
