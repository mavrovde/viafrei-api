package console_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	consoleweb "github.com/mavrovde/viafrei-api/internal/adapters/console"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/testkit/consolemem"
	"github.com/mavrovde/viafrei-api/internal/testkit/passkey"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

const (
	origin = "http://localhost:8081"
	host   = "localhost:8081"
	// peer is the planted client address: it may appear in no page, log
	// line or audit record.
	peer = "[::1]:40404"
	// plantedPepper and plantedKey are planted.
	plantedPepper = "pepper-PLANTED-0123456789abcdefghij"
	plantedKey    = "vf_test_" + "PLANTEDKEY" + "abcdefghijklmnop"
)

// lockedBuffer is a buffer a server goroutine writes while a test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// otherLoopback is 127.b.c.d:port, an address of 127.0.0.0/8 other than
// 127.0.0.1, built from its bytes.
func otherLoopback(b, c, d byte, port uint16) string {
	return netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, b, c, d}), port).String()
}

// switchReader is a random source a test can make fail.
type switchReader struct{ fail atomic.Bool }

func (r *switchReader) Read(b []byte) (int, error) {
	if r.fail.Load() {
		return 0, errors.New("no entropy")
	}
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i % 8))
	}
	return len(b), nil
}

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// lifecycle is a fake key lifecycle recording each call.
type lifecycle struct {
	mu    sync.Mutex
	calls []string
	err   error
	// pruneErr fails PruneUsage only.
	pruneErr error
	listErr  [3]error
}

func (l *lifecycle) record(call string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
	return l.err
}

func (l *lifecycle) PutPlan(_ context.Context, p access.Plan, actor string) error {
	return l.record(fmt.Sprintf("plan %s %s %s %v %v %d %d %d %d %s", p.Name, p.UseClass, p.Tier, p.Scopes, p.Sources, p.Rate, p.Burst, p.QuotaDay, p.QuotaMonth, actor))
}

func (l *lifecycle) Issue(_ context.Context, plan, label, contact string, expiry time.Duration, actor string) (access.Key, error) {
	err := l.record(fmt.Sprintf("issue %s %s %s %s %s", plan, label, contact, expiry, actor))
	if err != nil {
		return access.Key{}, err
	}
	return access.Key{ID: "keyid00001", Secret: "s", Full: plantedKey}, nil
}

func (l *lifecycle) Rotate(_ context.Context, id, actor string) (access.Key, error) {
	err := l.record("rotate " + id + " " + actor)
	return access.Key{ID: id, Full: plantedKey}, err
}

func (l *lifecycle) Revoke(_ context.Context, id, reason, actor string) error {
	return l.record("revoke " + id + " " + reason + " " + actor)
}

func (l *lifecycle) Move(_ context.Context, id, plan, actor string) error {
	return l.record("move " + id + " " + plan + " " + actor)
}

func (l *lifecycle) Accept(_ context.Context, id, family, version, actor string) error {
	return l.record("accept " + id + " " + family + " " + version + " " + actor)
}

func (l *lifecycle) Prune(_ context.Context, actor string) (int64, error) {
	return 3, l.record("prune " + actor)
}

func (l *lifecycle) PruneUsage(_ context.Context, actor string) (int64, error) {
	_ = l.record("prune_usage " + actor)
	return 4, l.pruneErr
}

func (l *lifecycle) KeyList(context.Context) ([]auth.KeyView, error) {
	revoked := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	return []auth.KeyView{
		{ID: "keyid00001", Environment: access.Test, Plan: "basic", Label: "lab", Contact: "c@example.org", IssuedAt: revoked, ExpiresAt: revoked},
		{ID: "keyid00002", Plan: "basic", RevokedAt: &revoked, RevokeReason: "lost"},
	}, l.listErr[0]
}

func (l *lifecycle) PlanList(context.Context) ([]access.Plan, error) {
	return []access.Plan{{Name: "basic", UseClass: "consumer", Tier: "standard", Scopes: []access.Scope{"read:places"}, Sources: []string{"x"}, Rate: 1, Burst: 2}}, l.listErr[1]
}

func (l *lifecycle) AuditList(_ context.Context, limit int) ([]auth.AuditEntry, error) {
	return []auth.AuditEntry{{Change: auth.Change{Action: "issue", KeyID: "keyid00001", Actor: "console:alice", At: time.Now()}, PlanAfter: "basic"}}, l.listErr[2]
}

func (l *lifecycle) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.calls) == 0 {
		return ""
	}
	return l.calls[len(l.calls)-1]
}

// failingStore fails the named calls only.
type failingStore struct {
	*consolemem.Store
	signedIn, add, passkeys error
}

func (f failingStore) Passkeys(ctx context.Context) ([]console.Passkey, error) {
	if f.passkeys != nil {
		return nil, f.passkeys
	}
	return f.Store.Passkeys(ctx)
}

func (f failingStore) SignedIn(ctx context.Context, c auth.Change, id []byte, n uint32, fl byte) error {
	if f.signedIn != nil {
		return f.signedIn
	}
	return f.Store.SignedIn(ctx, c, id, n, fl)
}

func (f failingStore) AddPasskey(ctx context.Context, c auth.Change, code []byte, p console.Passkey) error {
	if f.add != nil {
		return f.add
	}
	return f.Store.AddPasskey(ctx, c, code, p)
}

type rig struct {
	t      *testing.T
	h      http.Handler
	clock  *clock
	guard  *console.Guard
	owners *console.Owners
	store  *consolemem.Store
	fail   *failingStore
	keys   *lifecycle
	random *switchReader
	grand  *switchReader
	logs   *bytes.Buffer
	// seen collects every page body the rig received.
	seen bytes.Buffer
}

type option func(*console.GuardSettings)

func newRig(t *testing.T, opts ...option) *rig {
	t.Helper()
	r := &rig{t: t, clock: &clock{t: time.Now()}, store: consolemem.New(), keys: &lifecycle{}, random: &switchReader{}, grand: &switchReader{}, logs: &bytes.Buffer{}}
	r.fail = &failingStore{Store: r.store}
	gs := console.GuardSettings{
		SessionTTL: 15 * time.Minute, ChallengeTTL: time.Minute, ConfirmTTL: 2 * time.Minute,
		SignInLimit: 100, SignInWindow: 5 * time.Minute, MaxPending: 16, MaxSessions: 4,
	}
	for _, o := range opts {
		o(&gs)
	}
	r.guard = console.NewGuard(gs, r.grand, r.clock.now)
	r.owners = console.NewOwners(console.OwnerSettings{Pepper: []byte(plantedPepper), EnrolmentTTL: 30 * time.Minute}, r.fail, nil, r.clock.now)
	h, err := consoleweb.New(consoleweb.Deps{
		Settings: consoleweb.Settings{Origin: origin, RPID: "localhost", RPName: "test console", ChallengeTTL: time.Minute, SessionTTL: 15 * time.Minute, AuditPage: 50},
		Guard:    r.guard, Owners: r.owners, Keys: r.keys, Random: r.random,
		Logger: slog.New(slog.NewJSONHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.h = h
	return r
}

// request is one request to the console.
type request struct {
	method, path, body, contentType, cookie string
	headers                                 map[string]string
	remote                                  string
	noOrigin                                bool
}

func (r *rig) do(q request) *httptest.ResponseRecorder {
	r.t.Helper()
	hr := httptest.NewRequest(q.method, "http://"+host+q.path, strings.NewReader(q.body))
	hr.RemoteAddr = peer
	if q.remote != "" {
		hr.RemoteAddr = q.remote
	}
	if q.method == http.MethodPost && !q.noOrigin {
		hr.Header.Set("Origin", origin)
	}
	if q.contentType != "" {
		hr.Header.Set("Content-Type", q.contentType)
	}
	if q.cookie != "" {
		hr.AddCookie(&http.Cookie{Name: consoleweb.CookieName, Value: q.cookie})
	}
	for k, v := range q.headers {
		if k == "Host" {
			hr.Host = v
			continue
		}
		hr.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, hr)
	r.seen.Write(w.Body.Bytes())
	return w
}

func (r *rig) postJSON(path string, v any) *httptest.ResponseRecorder {
	r.t.Helper()
	b, _ := json.Marshal(v)
	return r.do(request{method: http.MethodPost, path: path, body: string(b), contentType: "application/json"})
}

// begun is a ceremony's first answer.
type begun struct {
	ID      string          `json:"id"`
	Options json.RawMessage `json:"options"`
}

func (r *rig) begin(path string, v any) begun {
	r.t.Helper()
	w := r.postJSON(path, v)
	if w.Code != http.StatusOK {
		r.t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var b begun
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		r.t.Fatal(err)
	}
	return b
}

// enrol enrols a new authenticator for owner and returns it.
func (r *rig) enrol(owner string) *passkey.Authenticator {
	r.t.Helper()
	code, err := r.owners.Offer(context.Background(), owner, "ops")
	if err != nil {
		r.t.Fatal(err)
	}
	a := passkey.New(origin, "localhost")
	b := r.begin("/enrol/begin", map[string]string{"code": code})
	w := r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": a.Create(b.Options)})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"/signin"`) {
		r.t.Fatalf("enrol finish: %d %s", w.Code, w.Body)
	}
	return a
}

// signIn signs in with a and returns the session cookie and CSRF token.
func (r *rig) signIn(a *passkey.Authenticator) (string, string) {
	r.t.Helper()
	b := r.begin("/signin/begin", struct{}{})
	w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": a.Get(b.Options)})
	if w.Code != http.StatusOK {
		r.t.Fatalf("sign-in finish: %d %s", w.Code, w.Body)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == consoleweb.CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 900 {
		r.t.Fatalf("cookie %+v", cookie)
	}
	home := r.do(request{method: http.MethodGet, path: "/", cookie: cookie.Value})
	m := csrfPattern.FindStringSubmatch(home.Body.String())
	if m == nil {
		r.t.Fatalf("no CSRF token on the home page: %s", home.Body)
	}
	return cookie.Value, m[1]
}

var (
	csrfPattern    = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	confirmPattern = regexp.MustCompile(`name="id" value="([^"]+)"`)
)

func form(csrf string, kv ...string) string {
	v := url.Values{}
	if csrf != "" {
		v.Set("csrf", csrf)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

const formType = "application/x-www-form-urlencoded"

// ask asks for a change and returns the confirmation id.
func (r *rig) ask(cookie, csrf, action string, kv ...string) string {
	r.t.Helper()
	w := r.do(request{method: http.MethodPost, path: "/act/" + action, body: form(csrf, kv...), contentType: formType, cookie: cookie})
	m := confirmPattern.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		r.t.Fatalf("ask %s: %d %s", action, w.Code, w.Body)
	}
	return m[1]
}

func (r *rig) confirm(cookie, csrf, id string) *httptest.ResponseRecorder {
	return r.do(request{method: http.MethodPost, path: "/confirm", body: form(csrf, "id", id), contentType: formType, cookie: cookie})
}

func TestEveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	for _, path := range []string{"/signin", "/enrol", "/console.js", "/console.css", "/"} {
		w := r.do(request{method: http.MethodGet, path: path})
		h := w.Header()
		if !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") || !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
			h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" ||
			h.Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s: headers %v", path, h)
		}
	}
	if w := r.do(request{method: http.MethodGet, path: "/console.js"}); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("script: %d %s", w.Code, w.Header())
	}
	if w := r.do(request{method: http.MethodGet, path: "/signin"}); w.Code != 200 || !strings.Contains(w.Body.String(), `<script src="/console.js">`) {
		t.Fatalf("sign-in page: %d", w.Code)
	}
	if w := r.do(request{method: http.MethodGet, path: "/"}); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/signin" {
		t.Fatalf("no session: %d", w.Code)
	}
	if w := r.do(request{method: http.MethodGet, path: "/audit", cookie: "forged"}); w.Code != http.StatusSeeOther {
		t.Fatalf("a forged session: %d", w.Code)
	}
}

func TestTheConsoleAnswersTheLoopbackAndItsOwnHostOnly(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	for _, tc := range []struct {
		name string
		q    request
		code int
	}{
		{"another network", request{method: http.MethodGet, path: "/signin", remote: "192.0.2.10:5000"}, http.StatusForbidden},
		{"no address", request{method: http.MethodGet, path: "/signin", remote: "pipe"}, http.StatusForbidden},
		{"another host (DNS rebinding)", request{method: http.MethodGet, path: "/signin", headers: map[string]string{"Host": "attacker.example:8081"}}, http.StatusMisdirectedRequest},
		{"the bare host", request{method: http.MethodGet, path: "/signin", headers: map[string]string{"Host": "localhost"}}, http.StatusMisdirectedRequest},
		{"a POST without Origin", request{method: http.MethodPost, path: "/signin/begin", body: "{}", contentType: "application/json", noOrigin: true}, http.StatusForbidden},
		{"a POST from a wrong origin", request{method: http.MethodPost, path: "/signin/begin", body: "{}", contentType: "application/json", headers: map[string]string{"Origin": "http://attacker.example"}}, http.StatusForbidden},
		{"a POST from a wrong port", request{method: http.MethodPost, path: "/signin/begin", body: "{}", contentType: "application/json", headers: map[string]string{"Origin": "http://localhost:9999"}}, http.StatusForbidden},
		{"a cross-site fetch", request{method: http.MethodPost, path: "/signin/begin", body: "{}", contentType: "application/json", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}}, http.StatusForbidden},
	} {
		if w := r.do(tc.q); w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
		}
	}
	// The refusal is JSON for a JSON request.
	w := r.do(request{method: http.MethodPost, path: "/signin/begin", body: "{}", contentType: "application/json", noOrigin: true})
	if w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), "own origin") {
		t.Fatalf("json refusal: %s %s", w.Header(), w.Body)
	}
	if w := r.do(request{method: http.MethodPost, path: "/signin/begin", body: "{}", contentType: "application/json", headers: map[string]string{"Sec-Fetch-Site": "same-origin"}}); w.Code != http.StatusOK {
		t.Fatalf("same-origin: %d", w.Code)
	}
	for _, ok := range []string{"127.0.0.1:1", otherLoopback(8, 9, 10, 1), "[::1]:1", "[::ffff:127.0.0.1]:1"} {
		if !consoleweb.Loopback(ok) {
			t.Fatalf("%s is the loopback", ok)
		}
	}
	for _, no := range []string{"192.0.2.1:1", "[::]:1", "0.0.0.0:1", "localhost:1", ""} {
		if consoleweb.Loopback(no) {
			t.Fatalf("%s is not the loopback", no)
		}
	}
}

func TestEnrolSignInAndRunEveryChange(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-044", "SEC-012")
	r := newRig(t)
	a := r.enrol("alice")
	cookie, csrf := r.signIn(a)

	home := r.do(request{method: http.MethodGet, path: "/", cookie: cookie}).Body.String()
	for _, want := range []string{"keyid00001", "lost", "basic", "read:places", "alice", console.Fingerprint(a.ID), "Sign out alice", "/act/prune"} {
		if !strings.Contains(home, want) {
			t.Fatalf("the home page lacks %q", want)
		}
	}
	if w := r.do(request{method: http.MethodGet, path: "/audit", cookie: cookie}); w.Code != 200 || !strings.Contains(w.Body.String(), "console:alice") || !strings.Contains(w.Body.String(), "newest 50") {
		t.Fatalf("audit: %d", w.Code)
	}

	for _, tc := range []struct {
		action string
		kv     []string
		call   string
		shows  string
	}{
		{"plan", []string{"name", "basic", "use_class", "consumer", "tier", "standard", "scopes", "read:places, read:fuel", "sources", "x", "rate", "5", "burst", "10", "quota_day", "100", "quota_month", ""},
			"plan basic consumer standard [read:places read:fuel] [x] 5 10 100 0 console:alice", "Plan basic saved."},
		{"issue", []string{"plan", "basic", "label", "lab", "contact", "c@example.org", "expires", "720h"}, "issue basic lab c@example.org 720h0m0s console:alice", "Key issued."},
		{"issue", []string{"plan", "basic", "label", "lab", "contact", "c@example.org"}, "issue basic lab c@example.org 0s console:alice", "Key issued."},
		{"rotate", []string{"id", "keyid00001"}, "rotate keyid00001 console:alice", "Key rotated"},
		{"revoke", []string{"id", "keyid00001", "reason", "lost laptop"}, "revoke keyid00001 lost laptop console:alice", "Key keyid00001 revoked."},
		{"move", []string{"id", "keyid00001", "plan", "pro"}, "move keyid00001 pro console:alice", "moved to pro"},
		{"accept", []string{"id", "keyid00001", "source", "fam", "version", "v1"}, "accept keyid00001 fam v1 console:alice", "Acceptance recorded."},
		{"prune", nil, "prune_usage console:alice", "3 audit records and 4 usage records removed."},
	} {
		id := r.ask(cookie, csrf, tc.action, tc.kv...)
		if r.keys.last() == tc.call {
			t.Fatalf("%s: made before it was confirmed", tc.action)
		}
		w := r.confirm(cookie, csrf, id)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.shows) || r.keys.last() != tc.call {
			t.Fatalf("%s: %d %q %s", tc.action, w.Code, r.keys.last(), w.Body)
		}
		shown := strings.Contains(w.Body.String(), plantedKey)
		if shown != (tc.action == "issue" || tc.action == "rotate") {
			t.Fatalf("%s: the key shown = %v", tc.action, shown)
		}
		// A confirmation is used once.
		if w := r.confirm(cookie, csrf, id); w.Code != http.StatusConflict {
			t.Fatalf("%s reused: %d", tc.action, w.Code)
		}
	}
	if w := r.do(request{method: http.MethodPost, path: "/signout", body: form(csrf), contentType: formType, cookie: cookie}); w.Code != 200 {
		t.Fatalf("sign out: %d", w.Code)
	}
	if w := r.do(request{method: http.MethodGet, path: "/", cookie: cookie}); w.Code != http.StatusSeeOther {
		t.Fatalf("after sign-out: %d", w.Code)
	}
	ch := r.store.Changes()
	if len(ch) != 3 || ch[1].Action != console.ActionPasskeyAdded || ch[2].Action != console.ActionSignIn || ch[2].Actor != "console:alice" {
		t.Fatalf("audit %+v", ch)
	}
	// A second owner's passkey lists beside the first.
	r.enrol("bob")
	if list, _ := r.owners.Passkeys(context.Background()); len(list) != 2 {
		t.Fatalf("passkeys %d", len(list))
	}
}

func TestEveryChangeNeedsASessionACSRFTokenAndAConfirmation(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	cookie, csrf := r.signIn(r.enrol("alice"))
	other, otherCSRF := r.signIn(r.enrol("bob"))
	revoke := []string{"id", "keyid00001", "reason", "r"}
	for _, tc := range []struct {
		name string
		q    request
		code int
	}{
		{"no session", request{method: http.MethodPost, path: "/act/revoke", body: form(csrf, revoke...), contentType: formType}, http.StatusUnauthorized},
		{"a forged session", request{method: http.MethodPost, path: "/act/revoke", body: form(csrf, revoke...), contentType: formType, cookie: "forged"}, http.StatusUnauthorized},
		{"no CSRF token", request{method: http.MethodPost, path: "/act/revoke", body: form("", revoke...), contentType: formType, cookie: cookie}, http.StatusForbidden},
		{"another session's CSRF token", request{method: http.MethodPost, path: "/act/revoke", body: form(otherCSRF, revoke...), contentType: formType, cookie: cookie}, http.StatusForbidden},
		{"the CSRF token in the query", request{method: http.MethodPost, path: "/act/revoke?csrf=" + csrf, body: form("", revoke...), contentType: formType, cookie: cookie}, http.StatusForbidden},
		{"an unreadable form", request{method: http.MethodPost, path: "/act/revoke", body: "csrf=%zz", contentType: formType, cookie: cookie}, http.StatusBadRequest},
		{"no CSRF token on confirm", request{method: http.MethodPost, path: "/confirm", body: form("", "id", "x"), contentType: formType, cookie: cookie}, http.StatusForbidden},
		{"no CSRF token on sign-out", request{method: http.MethodPost, path: "/signout", body: "", contentType: formType, cookie: cookie}, http.StatusForbidden},
		{"an unknown change", request{method: http.MethodPost, path: "/act/drop", body: form(csrf), contentType: formType, cookie: cookie}, http.StatusNotFound},
		{"a missing value", request{method: http.MethodPost, path: "/act/revoke", body: form(csrf, "id", "keyid00001"), contentType: formType, cookie: cookie}, http.StatusBadRequest},
		{"an unknown confirmation", request{method: http.MethodPost, path: "/confirm", body: form(csrf, "id", "unknown"), contentType: formType, cookie: cookie}, http.StatusConflict},
	} {
		if w := r.do(tc.q); w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
		}
	}
	if len(r.keys.calls) != 0 {
		t.Fatalf("a refused request reached the lifecycle: %v", r.keys.calls)
	}
	// A confirmation asked for by one session is no use to another.
	id := r.ask(cookie, csrf, "revoke", revoke...)
	if w := r.confirm(other, otherCSRF, id); w.Code != http.StatusConflict {
		t.Fatalf("another session's confirmation: %d", w.Code)
	}
	// ... and was spent by the attempt.
	if w := r.confirm(cookie, csrf, id); w.Code != http.StatusConflict {
		t.Fatalf("spent: %d", w.Code)
	}
	// An expired confirmation.
	id = r.ask(cookie, csrf, "revoke", revoke...)
	r.clock.add(2 * time.Minute)
	if w := r.confirm(cookie, csrf, id); w.Code != http.StatusGone {
		t.Fatalf("expired: %d", w.Code)
	}
	if len(r.keys.calls) != 0 {
		t.Fatalf("an unconfirmed change was made: %v", r.keys.calls)
	}
	// An expired session.
	r.clock.add(15 * time.Minute)
	if w := r.do(request{method: http.MethodPost, path: "/act/revoke", body: form(csrf, revoke...), contentType: formType, cookie: cookie}); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: %d", w.Code)
	}
}

func TestAChangeWithBadValuesIsRefusedBeforeItIsHeld(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	cookie, csrf := r.signIn(r.enrol("alice"))
	plan := func(field, value string) []string {
		kv := map[string]string{"name": "p", "use_class": "consumer", "tier": "standard", "rate": "1", "burst": "1", "quota_day": "0", "quota_month": "0"}
		kv[field] = value
		var out []string
		for k, v := range kv {
			out = append(out, k, v)
		}
		return out
	}
	for _, tc := range []struct {
		action string
		kv     []string
		want   string
	}{
		{"plan", plan("rate", "fast"), "rate"},
		{"plan", plan("burst", "1.5"), "burst"},
		{"plan", plan("quota_day", "x"), "quota_day"},
		{"plan", plan("quota_month", "x"), "quota_month"},
		{"issue", []string{"plan", "p", "label", "l", "contact", "c", "expires", "a year"}, "not a duration"},
	} {
		w := r.do(request{method: http.MethodPost, path: "/act/" + tc.action, body: form(csrf, tc.kv...), contentType: formType, cookie: cookie})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s %s: %d %s", tc.action, tc.want, w.Code, w.Body)
		}
	}
}

func TestTheLifecyclesRefusalsAreShownWithoutTheStoresDetail(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	cookie, csrf := r.signIn(r.enrol("alice"))
	for _, tc := range []struct {
		err  error
		code int
		want string
	}{
		{fmt.Errorf("%w: reason must be 1 to 200 characters", auth.ErrInput), http.StatusBadRequest, "reason must be"},
		{auth.ErrUnknown, http.StatusConflict, "no such key"},
		{errors.New("pq: detail PLANTED-DETAIL"), http.StatusConflict, "see the server log"},
	} {
		r.keys.err = tc.err
		id := r.ask(cookie, csrf, "revoke", "id", "keyid00001", "reason", "r")
		w := r.confirm(cookie, csrf, id)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), "PLANTED-DETAIL") {
			t.Fatalf("%v: %d %s", tc.err, w.Code, w.Body)
		}
	}
	r.keys.err = errors.New("down")
	if w := r.confirm(cookie, csrf, r.ask(cookie, csrf, "prune")); w.Code != http.StatusConflict {
		t.Fatalf("prune: %d", w.Code)
	}
	r.keys.err, r.keys.pruneErr = nil, errors.New("down")
	if w := r.confirm(cookie, csrf, r.ask(cookie, csrf, "prune")); w.Code != http.StatusConflict {
		t.Fatalf("prune usage: %d", w.Code)
	}
	for i := range 3 {
		r.keys.listErr = [3]error{}
		r.keys.listErr[i] = errors.New("down")
		path := "/"
		if i == 2 {
			path = "/audit"
		}
		if w := r.do(request{method: http.MethodGet, path: path, cookie: cookie}); w.Code != http.StatusInternalServerError {
			t.Fatalf("list %d: %d", i, w.Code)
		}
	}
	r.keys.listErr = [3]error{}
	r.fail.passkeys = errors.New("down")
	if w := r.do(request{method: http.MethodGet, path: "/", cookie: cookie}); w.Code != http.StatusInternalServerError {
		t.Fatalf("passkeys: %d", w.Code)
	}
	r.fail.passkeys = nil
	// A store that cannot say whether the session's passkey still exists
	// admits nothing, and ends no session.
	r.store.Err = errors.New("down")
	if w := r.do(request{method: http.MethodGet, path: "/", cookie: cookie}); w.Code != http.StatusInternalServerError {
		t.Fatalf("page, store down: %d", w.Code)
	}
	if w := r.do(request{method: http.MethodPost, path: "/act/prune", body: form(csrf), contentType: formType, cookie: cookie}); w.Code != http.StatusInternalServerError {
		t.Fatalf("change, store down: %d", w.Code)
	}
	r.store.Err = nil
	if w := r.do(request{method: http.MethodGet, path: "/", cookie: cookie}); w.Code != http.StatusOK {
		t.Fatalf("session kept: %d", w.Code)
	}
}

func TestSignInRefusals(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	a := r.enrol("alice")
	signIn := func(change func(*passkey.Authenticator), before func(b begun)) *httptest.ResponseRecorder {
		t.Helper()
		k := *a
		if change != nil {
			change(&k)
		}
		b := r.begin("/signin/begin", struct{}{})
		if before != nil {
			before(b)
		}
		return r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": k.Get(b.Options)})
	}
	for _, tc := range []struct {
		name   string
		change func(*passkey.Authenticator)
		before func(b begun)
		want   string
	}{
		{"user verification missing", func(k *passkey.Authenticator) { k.SkipUV = true }, nil, "not accepted"},
		{"a wrong origin in the client data", func(k *passkey.Authenticator) { k.Origin = "http://attacker.example" }, nil, "not accepted"},
		{"a wrong relying party", func(k *passkey.Authenticator) { k.RPID = "attacker.example" }, nil, "not accepted"},
		{"another user handle", func(k *passkey.Authenticator) { k.Handle = []byte("someone else") }, nil, "not accepted"},
		{"an unknown passkey", func(k *passkey.Authenticator) { *k = *passkey.New(origin, "localhost"); k.Handle = a.Handle }, nil, "not accepted"},
		{"a counter that went backwards", func(k *passkey.Authenticator) { k.Counter = 0 }, nil, "not accepted"},
		{"an expired challenge", nil, func(begun) { r.clock.add(time.Minute) }, "expired"},
	} {
		if w := signIn(tc.change, tc.before); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), tc.want) || len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
		}
	}
	// A reused challenge: the first answer spends it, right or wrong.
	b := r.begin("/signin/begin", struct{}{})
	cred := a.Get(b.Options)
	if w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": cred}); w.Code != 200 {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	if w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": cred}); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "already used") {
		t.Fatalf("reused: %d %s", w.Code, w.Body)
	}
	// A challenge of the enrolment kind is no sign-in challenge.
	code, _ := r.owners.Offer(context.Background(), "alice", "ops")
	eb := r.begin("/enrol/begin", map[string]string{"code": code})
	if w := r.postJSON("/signin/finish", map[string]any{"id": eb.ID, "credential": a.Get(eb.Options)}); w.Code != http.StatusUnauthorized {
		t.Fatalf("an enrolment challenge: %d", w.Code)
	}
	for name, q := range map[string]request{
		"not JSON":       {method: http.MethodPost, path: "/signin/finish", body: "{}", contentType: formType},
		"broken JSON":    {method: http.MethodPost, path: "/signin/finish", body: "{", contentType: "application/json"},
		"an empty body":  {method: http.MethodPost, path: "/signin/finish", body: `{"id":"x"}`, contentType: "application/json"},
		"too large":      {method: http.MethodPost, path: "/signin/finish", body: `{"id":"` + strings.Repeat("x", 70<<10) + `"}`, contentType: "application/json"},
		"begin not JSON": {method: http.MethodPost, path: "/signin/begin", body: "x", contentType: formType},
	} {
		if w := r.do(q); w.Code != http.StatusUnauthorized && w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	b = r.begin("/signin/begin", struct{}{})
	if w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": map[string]string{"id": "garbage"}}); w.Code != http.StatusUnauthorized {
		t.Fatalf("garbage credential: %d", w.Code)
	}
}

func TestSignInIsRateLimited(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t, func(g *console.GuardSettings) { g.SignInLimit = 3 })
	a := r.enrol("alice") // one attempt
	b := r.begin("/signin/begin", struct{}{})
	k := *a
	k.SkipUV = true
	// A failed finish counts as an attempt too.
	if w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": k.Get(b.Options)}); w.Code != http.StatusUnauthorized {
		t.Fatalf("failed: %d", w.Code)
	}
	w := r.postJSON("/signin/begin", struct{}{})
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("limited: %d %v", w.Code, w.Header())
	}
	if w := r.postJSON("/enrol/begin", map[string]string{"code": "x"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("enrolment shares the limit: %d", w.Code)
	}
}

func TestPendingCeremoniesAreBounded(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t, func(g *console.GuardSettings) { g.MaxPending = 4 })
	cookie, csrf := r.signIn(r.enrol("alice"))
	for range 4 {
		r.begin("/signin/begin", struct{}{})
	}
	w := r.postJSON("/signin/begin", struct{}{})
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("full: %d %v", w.Code, w.Header())
	}
	if w := r.do(request{method: http.MethodPost, path: "/act/prune", body: form(csrf), contentType: formType, cookie: cookie}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a confirmation when full: %d", w.Code)
	}
}

func TestEnrolmentRefusals(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-010")
	r := newRig(t)
	ctx := context.Background()
	if w := r.postJSON("/enrol/begin", map[string]string{"code": "unknown"}); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "enrolment code") {
		t.Fatalf("unknown code: %d %s", w.Code, w.Body)
	}
	if w := r.do(request{method: http.MethodPost, path: "/enrol/begin", body: "code=x", contentType: formType}); w.Code != http.StatusUnauthorized {
		t.Fatalf("not JSON: %d", w.Code)
	}
	// A wrong origin in the client data.
	code, _ := r.owners.Offer(ctx, "alice", "ops")
	b := r.begin("/enrol/begin", map[string]string{"code": code})
	evil := passkey.New("http://attacker.example", "localhost")
	if w := r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": evil.Create(b.Options)}); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong origin: %d", w.Code)
	}
	// User verification missing.
	b = r.begin("/enrol/begin", map[string]string{"code": code})
	noUV := passkey.New(origin, "localhost")
	noUV.SkipUV = true
	if w := r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": noUV.Create(b.Options)}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no UV: %d", w.Code)
	}
	// An expired challenge.
	b = r.begin("/enrol/begin", map[string]string{"code": code})
	r.clock.add(time.Minute)
	if w := r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": passkey.New(origin, "localhost").Create(b.Options)}); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("expired: %d %s", w.Code, w.Body)
	}
	// Two ceremonies on one code: the second finish finds it used.
	first := r.begin("/enrol/begin", map[string]string{"code": code})
	second := r.begin("/enrol/begin", map[string]string{"code": code})
	if w := r.postJSON("/enrol/finish", map[string]any{"id": first.ID, "credential": passkey.New(origin, "localhost").Create(first.Options)}); w.Code != 200 {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	w := r.postJSON("/enrol/finish", map[string]any{"id": second.ID, "credential": passkey.New(origin, "localhost").Create(second.Options)})
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "enrolment code") {
		t.Fatalf("second: %d %s", w.Code, w.Body)
	}
	// A reused challenge, broken JSON and a garbage credential.
	if w := r.postJSON("/enrol/finish", map[string]any{"id": first.ID, "credential": json.RawMessage(`{}`)}); w.Code != http.StatusUnauthorized {
		t.Fatalf("reused: %d", w.Code)
	}
	if w := r.do(request{method: http.MethodPost, path: "/enrol/finish", body: "{", contentType: "application/json"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("broken JSON: %d", w.Code)
	}
	code, _ = r.owners.Offer(ctx, "alice", "ops")
	b = r.begin("/enrol/begin", map[string]string{"code": code})
	if w := r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": map[string]string{"id": "garbage"}}); w.Code != http.StatusUnauthorized {
		t.Fatalf("garbage: %d", w.Code)
	}
}

func TestInternalFailuresAnswer500WithoutDetail(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	ctx := context.Background()
	a := r.enrol("alice")

	// The store fails to record the sign-in.
	r.fail.signedIn = errors.New("PLANTED-DETAIL")
	b := r.begin("/signin/begin", struct{}{})
	if w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": a.Get(b.Options)}); w.Code != 500 || strings.Contains(w.Body.String(), "PLANTED") {
		t.Fatalf("signed in: %d %s", w.Code, w.Body)
	}
	// The store refuses the counter (a sign-in that raced this one, or a
	// counter that did not advance): a refused sign-in, not a failure.
	r.fail.signedIn = console.ErrCounter
	b = r.begin("/signin/begin", struct{}{})
	if w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": a.Get(b.Options)}); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatalf("counter refused: %d %s", w.Code, w.Body)
	}
	r.fail.signedIn = nil
	// No entropy for the session.
	b = r.begin("/signin/begin", struct{}{})
	r.grand.fail.Store(true)
	if w := r.postJSON("/signin/finish", map[string]any{"id": b.ID, "credential": a.Get(b.Options)}); w.Code != 500 {
		t.Fatalf("session: %d", w.Code)
	}
	// No entropy for a challenge.
	if w := r.postJSON("/signin/begin", struct{}{}); w.Code != 500 {
		t.Fatalf("challenge: %d", w.Code)
	}
	r.grand.fail.Store(false)
	// No entropy for a user handle.
	code, _ := r.owners.Offer(ctx, "bob", "ops")
	r.random.fail.Store(true)
	if w := r.postJSON("/enrol/begin", map[string]string{"code": code}); w.Code != 500 {
		t.Fatalf("handle: %d", w.Code)
	}
	r.random.fail.Store(false)
	// The store fails to add the passkey.
	b = r.begin("/enrol/begin", map[string]string{"code": code})
	r.fail.add = errors.New("PLANTED-DETAIL")
	if w := r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": passkey.New(origin, "localhost").Create(b.Options)}); w.Code != 500 || strings.Contains(w.Body.String(), "PLANTED") {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	r.fail.add = nil
	// No entropy for a confirmation.
	cookie, csrf := r.signIn(a)
	r.grand.fail.Store(true)
	if w := r.do(request{method: http.MethodPost, path: "/act/prune", body: form(csrf), contentType: formType, cookie: cookie}); w.Code != 500 {
		t.Fatalf("confirmation: %d", w.Code)
	}
	r.grand.fail.Store(false)
	// The owner who already has a passkey enrols another under the same
	// handle, with the passkeys the owner holds excluded; bob's is not.
	code, _ = r.owners.Offer(ctx, "alice", "ops")
	r.fail.passkeys = errors.New("PLANTED-DETAIL")
	if w := r.postJSON("/enrol/begin", map[string]string{"code": code}); w.Code != 500 || strings.Contains(w.Body.String(), "PLANTED") {
		t.Fatalf("passkeys for the exclusions: %d %s", w.Code, w.Body)
	}
	r.fail.passkeys = nil
	bob := r.enrol("bob")
	second := passkey.New(origin, "localhost")
	b = r.begin("/enrol/begin", map[string]string{"code": code})
	var opts struct {
		PublicKey struct {
			Exclude []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"excludeCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(b.Options, &opts); err != nil {
		t.Fatal(err)
	}
	ex := opts.PublicKey.Exclude
	if len(ex) != 1 || ex[0].Type != "public-key" || ex[0].ID != base64.RawURLEncoding.EncodeToString(a.ID) || ex[0].ID == base64.RawURLEncoding.EncodeToString(bob.ID) {
		t.Fatalf("excluded %+v", ex)
	}
	if w := r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": second.Create(b.Options)}); w.Code != 200 || !bytes.Equal(second.Handle, a.Handle) {
		t.Fatalf("second passkey: %d", w.Code)
	}
}

// TestRemovingAPasskeyEndsItsSessions: a session lasts no longer than the
// passkey it was signed in with; another passkey's session is untouched.
func TestRemovingAPasskeyEndsItsSessions(t *testing.T) {
	req.Covers(t, "THR-018")
	r := newRig(t)
	ctx := context.Background()
	a := r.enrol("alice")
	b := r.enrol("bob")
	cookie, csrf := r.signIn(a)
	other, otherCSRF := r.signIn(b)
	if err := r.owners.Remove(ctx, base64.RawURLEncoding.EncodeToString(a.ID), "ops"); err != nil {
		t.Fatal(err)
	}
	if w := r.do(request{method: http.MethodPost, path: "/act/prune", body: form(csrf), contentType: formType, cookie: cookie}); w.Code != http.StatusUnauthorized {
		t.Fatalf("change after removal: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(r.logs.String(), "its passkey was removed") {
		t.Fatal("the ended session was not logged")
	}
	// The session is gone, not merely refused: a page sends to sign-in.
	if w := r.do(request{method: http.MethodGet, path: "/", cookie: cookie}); w.Code != http.StatusSeeOther {
		t.Fatalf("page after removal: %d", w.Code)
	}
	if _, err := r.guard.Session(cookie); !errors.Is(err, console.ErrNoSession) {
		t.Fatalf("session kept: %v", err)
	}
	if id := r.ask(other, otherCSRF, "prune"); id == "" {
		t.Fatal("another passkey's session ended")
	}
	// A page is refused the same way once its passkey goes.
	if err := r.owners.Remove(ctx, base64.RawURLEncoding.EncodeToString(b.ID), "ops"); err != nil {
		t.Fatal(err)
	}
	if w := r.do(request{method: http.MethodGet, path: "/audit", cookie: other}); w.Code != http.StatusSeeOther {
		t.Fatalf("page after removal: %d", w.Code)
	}
}

// TestNoLogLineCarriesTheClientsPathOrMethod: a path with CR/LF and a forged
// log fragment, and a method the console does not route, are logged as the
// registered route (or "unmatched") and a known method (or "other"), never
// as the client sent them.
func TestNoLogLineCarriesTheClientsPathOrMethod(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-012")
	const forged = "\r\nlevel=ERROR msg=FORGED-ENTRY"
	r := newRig(t)
	plant := func(method, path, remote string, sendOrigin bool) *httptest.ResponseRecorder {
		t.Helper()
		hr := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
		hr.Method = method
		hr.URL.Path = path
		hr.RequestURI = path
		hr.RemoteAddr = remote
		if sendOrigin {
			hr.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		r.h.ServeHTTP(w, hr)
		return w
	}
	cases := []struct {
		method, path, remote string
		sendOrigin           bool
		status               int
		wantMethod, route    string
	}{
		// Refused by the guard, before the mux: the route is resolved.
		{http.MethodGet, "/audit" + forged, "192.0.2.10:1", false, http.StatusForbidden, http.MethodGet, "unmatched"},
		{http.MethodHead, "/signin", "192.0.2.10:1", false, http.StatusForbidden, http.MethodHead, "GET /signin"},
		{"FORGE" + forged, "/x" + forged, "192.0.2.10:1", false, http.StatusForbidden, "other", "unmatched"},
		{http.MethodPost, "/confirm" + forged, peer, false, http.StatusForbidden, http.MethodPost, "unmatched"},
		// Refused inside a route: the mux's own pattern.
		{http.MethodPost, "/act/x" + forged, peer, true, http.StatusUnauthorized, http.MethodPost, "POST /act/{action}"},
	}
	for _, c := range cases {
		r.logs.Reset()
		if w := plant(c.method, c.path, c.remote, c.sendOrigin); w.Code != c.status {
			t.Fatalf("%q %q: status %d, want %d", c.method, c.path, w.Code, c.status)
		}
		out := r.logs.String()
		if strings.Contains(out, "FORGE") || strings.Contains(out, `\r`) {
			t.Fatalf("a log line carries the client's path or method: %s", out)
		}
		var line struct{ Method, Route string }
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &line); err != nil {
			t.Fatalf("not one JSON log line: %v: %s", err, out)
		}
		if line.Method != c.wantMethod || line.Route != c.route {
			t.Fatalf("%q %q: logged method %q route %q, want %q %q", c.method, c.path, line.Method, line.Route, c.wantMethod, c.route)
		}
	}
}

// TestTheServerErrorLogCarriesNoAddress: net/http's own error lines (a
// panic, a TLS handshake error) name the peer; the console's error log
// takes every address out.
func TestTheServerErrorLogCarriesNoAddress(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-012")
	var buf bytes.Buffer
	el := consoleweb.ErrorLog(slog.New(slog.NewJSONHandler(&buf, nil)))
	el.Printf("http: TLS handshake error from 192.0.2.77:51234: EOF")
	el.Printf("http: panic serving [::1]:40404: boom")
	el.Printf("bare 2001:db8::7 and fe80::1%%lo0 and 198.51.100.9")
	got := buf.String()
	for _, needle := range []string{"192.0.2.77", "51234", "::1", "40404", "2001:db8", "fe80", "198.51.100.9"} {
		if strings.Contains(got, needle) {
			t.Fatalf("%q in %s", needle, got)
		}
	}
	if strings.Count(got, `"msg":"console http server"`) != 3 || !strings.Contains(got, "TLS handshake error from [address]: EOF") || !strings.Contains(got, "boom") {
		t.Fatalf("log %s", got)
	}
	// A real panic, served over TCP: the client address is the loopback.
	var served lockedBuffer
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("PLANTED-PANIC") }))
	srv.Config.ErrorLog = consoleweb.ErrorLog(slog.New(slog.NewJSONHandler(&served, nil)))
	srv.Start()
	hr, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if resp, err := http.DefaultClient.Do(hr); err == nil {
		_ = resp.Body.Close()
	}
	srv.Close()
	got = served.String()
	if !strings.Contains(got, "PLANTED-PANIC") || strings.Contains(got, "127.0.0.1") {
		t.Fatalf("panic log %s", got)
	}
}

// TestNoSecretMACOrAddressLeaves is the planted-secret sweep of the console
// (SEC-010): the pepper, an enrolment code, a code MAC, the session and CSRF
// tokens and the client address appear in no page, log line or audit
// record; the full key appears only on the page that answers its issue or
// rotation.
func TestNoSecretMACOrAddressLeaves(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-010", "SEC-012")
	r := newRig(t)
	ctx := context.Background()
	code, _ := r.owners.Offer(ctx, "alice", "ops")
	a := passkey.New(origin, "localhost")
	b := r.begin("/enrol/begin", map[string]string{"code": code})
	r.postJSON("/enrol/finish", map[string]any{"id": b.ID, "credential": a.Create(b.Options)})
	cookie, csrf := r.signIn(a)
	issued := r.confirm(cookie, csrf, r.ask(cookie, csrf, "issue", "plan", "basic", "label", "l", "contact", "c"))
	if !strings.Contains(issued.Body.String(), plantedKey) {
		t.Fatal("the issued key is not shown")
	}
	pages := strings.ReplaceAll(r.seen.String(), issued.Body.String(), "")
	for _, path := range []string{"/", "/audit", "/signin", "/enrol"} {
		pages += r.do(request{method: http.MethodGet, path: path, cookie: cookie}).Body.String()
	}
	// Refusals too.
	pages += r.do(request{method: http.MethodPost, path: "/act/revoke", body: form("wrong", "id", "x"), contentType: formType, cookie: cookie}).Body.String()
	pages += r.postJSON("/enrol/begin", map[string]string{"code": code}).Body.String()
	pages += r.do(request{method: http.MethodGet, path: "/", remote: "192.0.2.77:1"}).Body.String()

	var audit strings.Builder
	for _, c := range r.store.Changes() {
		fmt.Fprintf(&audit, "%+v\n", c)
	}
	mac := string(access.MAC([]byte(plantedPepper), "console-enrolment", code))
	planted := map[string]string{
		"pepper": plantedPepper, "enrolment code": code, "code MAC": mac, "key": plantedKey,
		"session token": cookie, "address": "[::1]", "foreign address": "192.0.2.77",
	}
	for where, text := range map[string]string{"pages": pages, "logs": r.logs.String(), "audit": audit.String()} {
		if text == "" {
			t.Fatalf("the sweep read no %s", where)
		}
		for name, v := range planted {
			if strings.Contains(text, v) {
				t.Errorf("the %s carry the %s", where, name)
			}
		}
	}
	if strings.Contains(r.logs.String(), csrf) || strings.Contains(audit.String(), csrf) {
		t.Error("a CSRF token reached the logs or the audit records")
	}
}

func TestNewRefusesABadConfiguration(t *testing.T) {
	req.Covers(t, "THR-018")
	deps := consoleweb.Deps{Settings: consoleweb.Settings{Origin: "::not a url", RPID: "localhost", RPName: "x", ChallengeTTL: time.Minute}}
	if _, err := consoleweb.New(deps); err == nil {
		t.Fatal("a bad origin")
	}
	deps.Settings.Origin = "localhost"
	if _, err := consoleweb.New(deps); err == nil {
		t.Fatal("an origin without a host")
	}
	deps.Settings.Origin, deps.Settings.RPID = origin, "not a domain!"
	if _, err := consoleweb.New(deps); err == nil {
		t.Fatal("a relying-party id that is no domain")
	}
	deps.Settings.RPID = "localhost"
	if _, err := consoleweb.New(deps); err != nil {
		t.Fatalf("defaults for the random source: %v", err)
	}
}

func TestListenRefusesAnyAddressButTheLoopback(t *testing.T) {
	req.Covers(t, "THR-018")
	ctx := context.Background()
	l, err := consoleweb.Listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	// The whole of 127.0.0.0/8 is the loopback. Linux answers on all of it;
	// macOS configures 127.0.0.1 alone unless an alias is added, and there
	// the bind itself is refused by the system, before Listen's own check.
	other := otherLoopback(0, 0, 2, 0)
	l, err = consoleweb.Listen(ctx, other)
	switch {
	case err == nil:
		_ = l.Close()
	case runtime.GOOS == "darwin" && errors.Is(err, syscall.EADDRNOTAVAIL):
		t.Logf("%s: no such address on this macOS host; proven where the range is configured", other)
	default:
		t.Fatalf("%s: %v", other, err)
	}
	if _, err := consoleweb.Listen(ctx, "0.0.0.0:0"); !errors.Is(err, consoleweb.ErrNotLoopback) {
		t.Fatalf("the wildcard: %v", err)
	}
	if _, err := consoleweb.Listen(ctx, "not an address"); err == nil {
		t.Fatal("a bad address")
	}
}
