package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/logging"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type fakeAuth struct {
	p   auth.Principal
	err error
	got []string
}

func (f *fakeAuth) Authenticate(_ context.Context, presented, client string) (auth.Principal, error) {
	f.got = append(f.got, presented+"|"+client)
	return f.p, f.err
}

// fakeSources is a source register; leak plants a row of a family the gate
// did not allow, as a broken storage read would return it.
type fakeSources struct {
	register []access.Terms
	regErr   error
	srcErr   error
	leak     string
	asked    [][]string
}

func (f *fakeSources) Register(context.Context) ([]access.Terms, error) { return f.register, f.regErr }

func (f *fakeSources) Sources(_ context.Context, allowed []string) ([]access.Source, error) {
	f.asked = append(f.asked, allowed)
	if f.srcErr != nil {
		return nil, f.srcErr
	}
	var out []access.Source
	for _, a := range append(slices.Clone(allowed), f.leak) {
		if a != "" {
			out = append(out, access.Source{Family: a, Domain: "energy", Publisher: "P", AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/" + a})
		}
	}
	return out, nil
}

var fixtureRegister = []access.Terms{
	{Family: "src-alpha-energy", UseClasses: []access.UseClass{"consumer", "non_commercial", "commercial"}, Version: "2026-03-01", Reviewed: true},
	{Family: "src-gamma-energy", UseClasses: []access.UseClass{"consumer"}, Version: "2026-03-01", Reviewed: true},
	{Family: "src-delta-parking", UseClasses: []access.UseClass{"consumer", "commercial"}, AcceptanceRequired: true, Version: "2026-03-01", Reviewed: true},
	{Family: "src-epsilon-roads", Version: "2026-03-01"},
}

func principal(use access.UseClass) auth.Principal {
	return auth.Principal{Key: access.Record{
		ID: "abcdefghij", Environment: access.Live, ExpiresAt: fixedNow.Add(time.Hour),
		Plan: access.Plan{Name: "pro", UseClass: use, Tier: "standard", Scopes: []access.Scope{"energy"},
			Sources: []string{"src-alpha-energy", "src-gamma-energy", "src-delta-parking", "src-epsilon-roads"}},
	}}
}

func keyedHandler(t *testing.T, cfg *config.Config, a httpapi.Authenticator, s httpapi.SourceReader, log *bytes.Buffer) http.Handler {
	t.Helper()
	d := httpapi.Deps{Config: cfg, Build: buildinfo.Info{Version: "t", Commit: "c"}, ContractVersion: "0.1", Now: func() time.Time { return fixedNow }}
	if a != nil {
		d.Auth = a
	}
	if s != nil {
		d.Sources = s
	}
	if log != nil {
		d.Logger = logging.New(config.Log{Level: "debug", Format: "json"}, log)
	}
	h, err := httpapi.NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

var bearer = http.Header{"Authorization": {"Bearer x"}}

func TestKeyedOperationIsOffWithoutAnAccessStore(t *testing.T) {
	req.Covers(t, "SEC-030")
	w := do(t, keyedHandler(t, config.Defaults(), nil, nil, nil), http.MethodGet, "/v1/key", bearer)
	assertProblem(t, w, http.StatusNotFound, "feature-disabled")
}

func TestEveryAuthenticationRefusal(t *testing.T) {
	req.Covers(t, "SEC-001", "SEC-003", "SEC-042", "SEC-052")
	cases := []struct {
		err       error
		status    int
		slug      string
		challenge string
	}{
		{access.ErrMissing, 401, "missing-key", `Bearer realm="viafrei-api"`},
		{access.ErrInvalid, 401, "invalid-key", `Bearer realm="viafrei-api", error="invalid_token"`},
		{access.ErrExpired, 401, "key-expired", `Bearer realm="viafrei-api", error="invalid_token"`},
		{access.ErrRevoked, 401, "key-revoked", `Bearer realm="viafrei-api", error="invalid_token"`},
		{auth.ErrLimited, 429, "rate-limited", ""},
		{errors.Join(auth.ErrUnavailable, errors.New("down")), 503, "upstream-unavailable", ""},
	}
	for _, c := range cases {
		h := keyedHandler(t, config.Defaults(), &fakeAuth{err: c.err}, &fakeSources{}, nil)
		w := do(t, h, http.MethodGet, "/v1/key", bearer)
		assertProblem(t, w, c.status, c.slug)
		if got := w.Header().Get("WWW-Authenticate"); got != c.challenge {
			t.Errorf("%s: WWW-Authenticate %q", c.slug, got)
		}
		if c.slug == "rate-limited" && w.Header().Get("Retry-After") != "60" {
			t.Errorf("rate-limited without Retry-After: %v", w.Header())
		}
	}
}

func TestKeyIsReadFromBearerOrHeader(t *testing.T) {
	req.Covers(t, "SEC-001")
	f := &fakeAuth{p: principal("consumer")}
	h := keyedHandler(t, config.Defaults(), f, &fakeSources{register: fixtureRegister}, nil)
	for _, hdr := range []http.Header{
		{"Authorization": {"Bearer  k1 "}},
		{"Authorization": {"bearer k2"}},
		{"X-Api-Key": {"k3"}},
		{"Authorization": {"Basic k4"}},
		{},
	} {
		do(t, h, http.MethodGet, "/v1/key", hdr)
	}
	want := []string{"k1|192.0.2.1", "k2|192.0.2.1", "k3|192.0.2.1", "|192.0.2.1", "|192.0.2.1"}
	if !slices.Equal(f.got, want) {
		t.Fatalf("presented %v, want %v", f.got, want)
	}
}

func TestKeyInQueryIsRefusedUnchecked(t *testing.T) {
	req.Covers(t, "SEC-002")
	f := &fakeAuth{p: principal("consumer")}
	h := keyedHandler(t, config.Defaults(), f, &fakeSources{}, nil)
	k, _ := access.Generate(rand.Reader, access.Live, access.Prefixes{Live: "vf_live_", Test: "vf_test_"})
	for _, q := range []string{"?x=" + k.Full, "?api_key=abc", "?KEY=abc", "?vf_test_x=1"} {
		w := do(t, h, http.MethodGet, "/v1/key"+q, nil)
		assertProblem(t, w, http.StatusBadRequest, "key-in-query")
		w = do(t, h, http.MethodGet, "/v1/meta"+q, nil)
		assertProblem(t, w, http.StatusBadRequest, "key-in-query")
	}
	if len(f.got) != 0 {
		t.Fatalf("a key in the query string was checked: %v", f.got)
	}
}

func TestKeyAnswer(t *testing.T) {
	req.Covers(t, "SEC-012", "SEC-043")
	p := principal("consumer")
	p.Expiring = true
	h := keyedHandler(t, config.Defaults(), &fakeAuth{p: p}, &fakeSources{register: fixtureRegister}, nil)
	w := do(t, h, http.MethodGet, "/v1/key", bearer)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := decode(t, w)
	data := body["data"].(map[string]any)
	if data["keyId"] != "abcdefghij" || data["environment"] != "live" || data["plan"].(map[string]any)["useClass"] != "consumer" {
		t.Fatalf("data %v", data)
	}
	warnings := body["meta"].(map[string]any)["warnings"].([]any)
	if len(warnings) != 1 || warnings[0].(map[string]any)["code"] != "key_expiring" {
		t.Fatalf("warnings %v", warnings)
	}
	for _, field := range []string{"secret", "mac", "Secret", "MAC"} {
		if strings.Contains(w.Body.String(), `"`+field+`"`) {
			t.Fatalf("the answer carries %s", field)
		}
	}
	h = keyedHandler(t, config.Defaults(), &fakeAuth{p: principal("consumer")}, &fakeSources{register: fixtureRegister}, nil)
	body = decode(t, do(t, h, http.MethodGet, "/v1/key", bearer))
	if w := body["meta"].(map[string]any)["warnings"].([]any); len(w) != 0 {
		t.Fatalf("a warning without an approaching expiry: %v", w)
	}
}

func families(t *testing.T, h http.Handler) []string {
	t.Helper()
	w := do(t, h, http.MethodGet, "/v1/key", bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out []string
	for _, s := range decode(t, w)["data"].(map[string]any)["sources"].([]any) {
		out = append(out, s.(map[string]any)["family"].(string))
	}
	return out
}

// One test per restricted family of the fixture set: none is ever returned
// to a key whose plan its terms do not permit.

func TestRestrictedPurposeFamilyNeverReachesACommercialKey(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-031")
	src := &fakeSources{register: fixtureRegister}
	got := families(t, keyedHandler(t, config.Defaults(), &fakeAuth{p: principal("commercial")}, src, nil))
	if slices.Contains(got, "src-gamma-energy") {
		t.Fatalf("restricted-purpose family served to a commercial key: %v", got)
	}
	if slices.Contains(src.asked[0], "src-gamma-energy") {
		t.Fatal("the forbidden family was asked of storage")
	}
	got = families(t, keyedHandler(t, config.Defaults(), &fakeAuth{p: principal("consumer")}, &fakeSources{register: fixtureRegister}, nil))
	if !slices.Contains(got, "src-gamma-energy") {
		t.Fatalf("restricted-purpose family withheld from a consumer key: %v", got)
	}
}

func TestTermsFamilyNeedsAcceptanceOfTheCurrentVersion(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-034")
	for _, c := range []struct {
		accepted []access.Acceptance
		served   bool
	}{
		{nil, false},
		{[]access.Acceptance{{Source: "src-delta-parking", Version: "2025-01-01"}}, false},
		{[]access.Acceptance{{Source: "src-delta-parking", Version: "2026-03-01"}}, true},
	} {
		p := principal("commercial")
		p.Key.Accepted = c.accepted
		got := families(t, keyedHandler(t, config.Defaults(), &fakeAuth{p: p}, &fakeSources{register: fixtureRegister}, nil))
		if slices.Contains(got, "src-delta-parking") != c.served {
			t.Errorf("accepted %v: served %v", c.accepted, got)
		}
	}
}

func TestUnreviewedFamilyIsNeverServed(t *testing.T) {
	req.Covers(t, "SEC-030")
	for _, use := range access.UseClasses {
		got := families(t, keyedHandler(t, config.Defaults(), &fakeAuth{p: principal(use)}, &fakeSources{register: fixtureRegister}, nil))
		if slices.Contains(got, "src-epsilon-roads") {
			t.Fatalf("unreviewed family served to %s", use)
		}
	}
}

func TestDisabledFamilyIsNeverServed(t *testing.T) {
	req.Covers(t, "SEC-030")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_SOURCES_DISABLED=src-alpha-energy"}})
	if err != nil {
		t.Fatal(err)
	}
	got := families(t, keyedHandler(t, cfg, &fakeAuth{p: principal("consumer")}, &fakeSources{register: fixtureRegister}, nil))
	if slices.Contains(got, "src-alpha-energy") || len(got) != 1 {
		t.Fatalf("disabled family served: %v", got)
	}
}

func TestPostCheckAbortsAForbiddenItem(t *testing.T) {
	req.Covers(t, "SEC-032")
	var log bytes.Buffer
	before := httpapi.GateViolations()
	src := &fakeSources{register: fixtureRegister, leak: "src-epsilon-roads"}
	w := do(t, keyedHandler(t, config.Defaults(), &fakeAuth{p: principal("consumer")}, src, &log), http.MethodGet, "/v1/key", bearer)
	assertProblem(t, w, http.StatusInternalServerError, "internal")
	if strings.Contains(w.Body.String(), "epsilon") {
		t.Fatal("the forbidden item reached the answer")
	}
	if httpapi.GateViolations() != before+1 || !strings.Contains(log.String(), "licence gate post-check") {
		t.Fatalf("no alert: %d %s", httpapi.GateViolations()-before, log.String())
	}
}

func TestKeyAnswerStorageFaults(t *testing.T) {
	for name, c := range map[string]struct {
		src  httpapi.SourceReader
		slug string
	}{
		"no store":      {nil, "upstream-unavailable"},
		"register down": {&fakeSources{regErr: store.ErrUnavailable}, "upstream-unavailable"},
		"sources down":  {&fakeSources{register: fixtureRegister, srcErr: fmt.Errorf("%w: x", store.ErrDeadlineExhausted)}, "deadline-exhausted"},
	} {
		w := do(t, keyedHandler(t, config.Defaults(), &fakeAuth{p: principal("consumer")}, c.src, nil), http.MethodGet, "/v1/key", bearer)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), c.slug) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestNoKeyReachesTheLog(t *testing.T) {
	req.Covers(t, "SEC-010")
	k, _ := access.Generate(rand.Reader, access.Live, access.Prefixes{Live: "vf_live_", Test: "vf_test_"})
	var log bytes.Buffer
	f := &fakeAuth{err: access.ErrInvalid}
	h := keyedHandler(t, config.Defaults(), f, &fakeSources{}, &log)
	do(t, h, http.MethodGet, "/v1/key", http.Header{"Authorization": {"Bearer " + k.Full}})
	do(t, h, http.MethodGet, "/v1/key", http.Header{"X-Api-Key": {k.Full}})
	do(t, h, http.MethodGet, "/v1/key?q="+k.Full, nil)
	do(t, h, http.MethodGet, "/v1/"+k.Full, nil)
	if strings.Contains(log.String(), k.Secret) || strings.Contains(log.String(), k.ID) {
		t.Fatalf("a key reached the log: %s", log.String())
	}
	if strings.Count(log.String(), `"msg":"request"`) != 4 {
		t.Fatalf("the requests were not logged: %s", log.String())
	}
}
