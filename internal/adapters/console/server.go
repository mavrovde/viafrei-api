// Package console is the owner console's HTTP adapter: a small server-rendered
// site on the loopback where the owner signs in with a passkey and runs the
// key lifecycle (Spec: Threat Model, THR-018). Every request must come from
// the loopback and name the console's host; every change must come from the
// console's origin, carry the session's CSRF token and be confirmed.
package console

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// CookieName is the session cookie. The __Host- prefix makes the browser
// keep it to this host, path "/" and a secure context.
const CookieName = "__Host-vf_console"

// maxBody bounds every request body.
const maxBody = 64 << 10

// Settings are the console's own settings.
type Settings struct {
	// Origin is scheme://host[:port], as the browser sees the console.
	Origin       string
	RPID, RPName string
	ChallengeTTL time.Duration
	SessionTTL   time.Duration
	AuditPage    int
}

// Lifecycle is the key lifecycle the console runs: auth.Keys.
type Lifecycle interface {
	PutPlan(ctx context.Context, p access.Plan, actor string) error
	Issue(ctx context.Context, plan, label, contact string, expiry time.Duration, actor string) (access.Key, error)
	Rotate(ctx context.Context, id, actor string) (access.Key, error)
	Revoke(ctx context.Context, id, reason, actor string) error
	Move(ctx context.Context, id, plan, actor string) error
	Accept(ctx context.Context, id, family, version, actor string) error
	Prune(ctx context.Context, actor string) (int64, error)
	PruneUsage(ctx context.Context, actor string) (int64, error)
	auth.Directory
}

// Deps are the console's collaborators.
type Deps struct {
	Settings Settings
	Guard    *console.Guard
	Owners   *console.Owners
	Keys     Lifecycle
	Logger   *slog.Logger
	// Random is the source of new user handles; nil means crypto/rand.
	Random io.Reader
}

type server struct {
	Deps
	host     string
	webauthn *webauthn.WebAuthn
	mux      *http.ServeMux
	// routes maps each registered pattern to itself, so that a log line
	// names a route the console registered and never a client's path.
	routes map[string]string
}

// New returns the console's handler.
func New(d Deps) (http.Handler, error) {
	u, err := url.Parse(d.Settings.Origin)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("console: origin %q is not scheme://host[:port]", d.Settings.Origin)
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID: d.Settings.RPID, RPDisplayName: d.Settings.RPName, RPOrigins: []string{d.Settings.Origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: d.Settings.ChallengeTTL, TimeoutUVD: d.Settings.ChallengeTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: d.Settings.ChallengeTTL, TimeoutUVD: d.Settings.ChallengeTTL},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("console: %w", err)
	}
	if d.Random == nil {
		d.Random = rand.Reader
	}
	s := &server{Deps: d, host: u.Host, webauthn: wa, mux: http.NewServeMux(), routes: map[string]string{}}
	handle := func(pattern string, h http.HandlerFunc) {
		s.mux.HandleFunc(pattern, h)
		s.routes[pattern] = pattern
	}
	handle("GET /{$}", s.withSession(s.home))
	handle("GET /audit", s.withSession(s.audit))
	handle("GET /signin", s.page("signin", "Sign in"))
	handle("GET /enrol", s.page("enrol", "Enrol a passkey"))
	handle("GET /console.js", s.asset("assets/console.js", "text/javascript; charset=utf-8"))
	handle("GET /console.css", s.asset("assets/console.css", "text/css; charset=utf-8"))
	handle("POST /signin/begin", s.signInBegin)
	handle("POST /signin/finish", s.signInFinish)
	handle("POST /enrol/begin", s.enrolBegin)
	handle("POST /enrol/finish", s.enrolFinish)
	handle("POST /signout", s.withChange(s.signOut))
	handle("POST /act/{action}", s.withChange(s.ask))
	handle("POST /confirm", s.withChange(s.confirm))
	return s.guard(s.mux), nil
}

// route names the request in a log line by the pattern the console
// registered for it, or "unmatched"; the client's path never reaches a log.
// The guard refuses before the mux runs, so an empty r.Pattern is resolved
// against the mux without serving.
func (s *server) route(r *http.Request) string {
	pattern := r.Pattern
	if pattern == "" {
		_, pattern = s.mux.Handler(r)
	}
	if route, ok := s.routes[pattern]; ok {
		return route
	}
	return "unmatched"
}

// method names the request's method in a log line: one the console's
// routes accept, or "other".
func method(r *http.Request) string {
	switch r.Method {
	case http.MethodGet:
		return http.MethodGet
	case http.MethodHead:
		return http.MethodHead
	case http.MethodPost:
		return http.MethodPost
	default:
		return "other"
	}
}

//go:embed assets
var assets embed.FS

func (s *server) asset(name, contentType string) http.HandlerFunc {
	body := invariant.Must(assets.ReadFile(name))
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}
}

// Loopback reports whether a request's peer address is a loopback address.
func Loopback(remoteAddr string) bool {
	ap, err := netip.ParseAddrPort(remoteAddr)
	return err == nil && ap.Addr().Unmap().IsLoopback()
}

// guard answers only loopback peers that name the console's host, sets the
// security headers on every answer, and admits a POST only from the
// console's own origin (THR-018).
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		switch {
		case !Loopback(r.RemoteAddr):
			s.refuse(w, r, http.StatusForbidden, "the console answers on the loopback only")
		case r.Host != s.host:
			// A request naming another host is a DNS-rebinding page or a
			// misrouted tunnel; neither may reach the console.
			s.refuse(w, r, http.StatusMisdirectedRequest, "this request names another host")
		case r.Method == http.MethodPost && !s.sameOrigin(r):
			s.refuse(w, r, http.StatusForbidden, "a change must come from the console's own origin")
		default:
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
			next.ServeHTTP(w, r)
		}
	})
}

// sameOrigin requires the Origin header to be the console's origin, and a
// Sec-Fetch-Site header, when sent, to say same-origin.
func (s *server) sameOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	return r.Header.Get("Origin") == s.Settings.Origin && (site == "" || site == "same-origin")
}

// refuse answers a refusal and logs its reason: never the peer's address,
// a cookie or a token.
func (s *server) refuse(w http.ResponseWriter, r *http.Request, status int, reason string) {
	s.Logger.Warn("console request refused", "method", method(r), "route", s.route(r), "status", status, "reason", reason)
	if r.Header.Get("Content-Type") == "application/json" {
		writeJSON(w, status, map[string]string{"error": reason})
		return
	}
	s.render(w, status, "message", view{Title: "Refused", Message: reason})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// session returns the request's live session. A session lasts no longer
// than the passkey it was signed in with: once that passkey is removed, the
// session is ended and answers ErrNoSession.
func (s *server) session(r *http.Request) (console.Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return console.Session{}, console.ErrNoSession
	}
	sess, err := s.Guard.Session(c.Value)
	if err != nil {
		return console.Session{}, err
	}
	if _, err := s.Owners.Passkey(r.Context(), sess.Passkey); err != nil {
		if errors.Is(err, console.ErrUnknownPasskey) {
			s.Guard.EndSession(sess.Token)
			s.Logger.Info("console session ended: its passkey was removed", "owner", sess.Owner, "passkey", console.Fingerprint(sess.Passkey))
			return console.Session{}, console.ErrNoSession
		}
		return console.Session{}, err
	}
	return sess, nil
}

type sessionHandler func(http.ResponseWriter, *http.Request, console.Session)

// withSession serves a page to a signed-in owner and sends anyone else to
// the sign-in page.
func (s *server) withSession(next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.session(r)
		if errors.Is(err, console.ErrNoSession) {
			http.Redirect(w, r, "/signin", http.StatusSeeOther)
			return
		}
		if err != nil {
			s.unavailable(w, r, err)
			return
		}
		next(w, r, sess)
	}
}

// withChange admits a change only with a live session and its CSRF token.
func (s *server) withChange(next sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.session(r)
		if errors.Is(err, console.ErrNoSession) {
			s.refuse(w, r, http.StatusUnauthorized, "sign in first")
			return
		}
		if err != nil {
			s.unavailable(w, r, err)
			return
		}
		if err := r.ParseForm(); err != nil {
			s.refuse(w, r, http.StatusBadRequest, "the form could not be read")
			return
		}
		if err := console.CheckCSRF(sess, r.PostForm.Get("csrf")); err != nil {
			s.refuse(w, r, http.StatusForbidden, "missing or wrong CSRF token")
			return
		}
		next(w, r, sess)
	}
}

func (s *server) page(name, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { s.render(w, http.StatusOK, name, view{Title: title}) }
}

func (s *server) signOut(w http.ResponseWriter, _ *http.Request, sess console.Session) {
	s.Guard.EndSession(sess.Token)
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	s.render(w, http.StatusOK, "message", view{Title: "Signed out", Message: "You are signed out."})
}

// retryAfter writes the Retry-After header in whole seconds, at least one.
func retryAfter(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int((d+time.Second-1)/time.Second))))
}

// address matches a network address with or without its port: IPv4, an IPv6
// address in brackets, and a bare IPv6 address.
var address = regexp.MustCompile(`\[[^\]\s]*\](:[0-9]+)?|[0-9]{1,3}(\.[0-9]{1,3}){3}(:[0-9]+)?|[0-9A-Fa-f]*(:[0-9A-Fa-f]*){2,}(%[0-9A-Za-z]+)?`)

// ErrorLog is the console server's net/http error log ("http: panic
// serving <peer>", "TLS handshake error from <peer>", …): each line goes to
// logger with every network address taken out, so no client address
// reaches a log line (THR-018).
func ErrorLog(logger *slog.Logger) *log.Logger {
	return log.New(addressFree{logger}, "", 0)
}

type addressFree struct{ logger *slog.Logger }

func (a addressFree) Write(p []byte) (int, error) {
	a.logger.Error("console http server", "message", address.ReplaceAllString(strings.TrimSpace(string(p)), "[address]"))
	return len(p), nil
}

// isLoopbackListener reports whether addr is a loopback address.
func isLoopbackListener(addr net.Addr) bool { return Loopback(addr.String()) }

// ErrNotLoopback is a console listener that is not on the loopback.
var ErrNotLoopback = errors.New("console: the listener is not on the loopback")

// Listen opens the console's listener on addr and refuses any address that is
// not a loopback address, whatever the configuration said.
func Listen(ctx context.Context, addr string) (net.Listener, error) {
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if !isLoopbackListener(l.Addr()) {
		_ = l.Close()
		return nil, ErrNotLoopback
	}
	return l, nil
}
