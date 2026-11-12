package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type stubAuth struct {
	p      auth.Principal
	client string
}

func (s *stubAuth) Authenticate(_ context.Context, _, client string) (auth.Principal, error) {
	s.client = client
	return s.p, nil
}

type scopedOutput struct {
	Body struct {
		Plan string `json:"plan"`
	}
}

// scoped is an operation that needs the roads scope; the catalogue has no
// scoped operation yet, so the test registers one.
var scoped = operation{id: "getScoped", method: http.MethodGet, path: "/v1/scoped", tag: "access", keyed: true, scope: "roads",
	register: func(api huma.API, _ *server, op huma.Operation) {
		huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*scopedOutput, error) {
			out := &scopedOutput{}
			out.Body.Plan = principalFrom(ctx).Key.Plan.Name
			return out, nil
		})
	}}

func TestScopeIsEnforcedPerOperation(t *testing.T) {
	req.Covers(t, "SEC-020")
	for _, c := range []struct {
		scopes []access.Scope
		status int
	}{
		{[]access.Scope{"energy"}, http.StatusForbidden},
		{nil, http.StatusForbidden},
		{[]access.Scope{"energy", "roads"}, http.StatusOK},
	} {
		a := &stubAuth{p: auth.Principal{Key: access.Record{Plan: access.Plan{Name: "pro", Scopes: c.scopes}}}}
		h, doc, err := build(Deps{Config: config.Defaults(), Auth: a}, []operation{scoped})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(doc()), `"x-required-scope": "roads"`) {
			t.Fatal("the scope is not in the document")
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/scoped", nil)
		r.RemoteAddr = "198.51.100.7"
		h.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Fatalf("scopes %v: %d %s", c.scopes, w.Code, w.Body)
		}
		if c.status == http.StatusForbidden && !strings.Contains(w.Body.String(), "/problems/scope-denied") {
			t.Fatalf("scopes %v: %s", c.scopes, w.Body)
		}
		if c.status == http.StatusOK && !strings.Contains(w.Body.String(), `"plan":"pro"`) {
			t.Fatalf("the operation did not see its key: %s", w.Body)
		}
		if a.client != "198.51.100.7" {
			t.Fatalf("an address without a port became %q", a.client)
		}
	}
}

func TestEveryKeyedOperationDeclaresAtMostOneScope(t *testing.T) {
	req.Covers(t, "SEC-020")
	for _, op := range operations() {
		if op.scope != "" && !op.keyed {
			t.Errorf("%s declares a scope but needs no key", op.id)
		}
	}
}
