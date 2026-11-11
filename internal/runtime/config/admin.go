package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Admin holds the owner console: a second listener, on the loopback only,
// where the owner signs in with a passkey and runs the key lifecycle
// (THR-018). It is off unless admin.enabled is set.
type Admin struct {
	Enabled      bool          `koanf:"enabled" default:"false" desc:"Run the owner console on admin.listen_addr. Needs access.url. Off by default."`
	ListenAddr   string        `koanf:"listen_addr" default:"127.0.0.1:8081" validate:"required" desc:"Address of the console listener: a loopback IP address (127.0.0.0/8 or ::1) and a port. Any other address is refused at boot; reach the console through a tunnel to the host."`
	Origin       string        `koanf:"origin" default:"http://localhost:8081" validate:"required,url" desc:"The console's origin as the browser sees it: scheme, host and port, no path. Every request must name its host, every change must come from it, and passkeys are bound to it. http is accepted for localhost and loopback addresses only."`
	RPID         string        `koanf:"rp_id" default:"localhost" validate:"required,hostname_rfc1123,max=253" desc:"WebAuthn relying-party id: the host of admin.origin or a parent domain of it. A passkey works only under the id it was enrolled with."`
	RPName       string        `koanf:"rp_name" default:"ViaFrei API console" validate:"required,max=64" desc:"Name of the relying party the authenticator shows when a passkey is enrolled."`
	SessionTTL   time.Duration `koanf:"session_ttl" default:"15m" validate:"min=1m,max=1h" desc:"Lifetime of a console session from sign-in; it is not extended by use. Sessions are held in memory only, so a restart signs everyone out."`
	ChallengeTTL time.Duration `koanf:"challenge_ttl" default:"1m" validate:"min=10s,max=5m" desc:"Lifetime of a sign-in or enrolment challenge. A challenge is single-use: it is gone after its first answer, right or wrong."`
	ConfirmTTL   time.Duration `koanf:"confirm_ttl" default:"2m" validate:"min=10s,max=10m" desc:"Time between asking for a change and confirming it. The confirmation is single-use and bound to the session that asked. At most admin.session_ttl."`
	EnrolmentTTL time.Duration `koanf:"enrolment_ttl" default:"30m" validate:"min=5m,max=24h" desc:"Lifetime of an enrolment code printed by console enrol. The code is single-use and stored only as its MAC."`
	SignInLimit  int           `koanf:"signin_limit" default:"10" validate:"min=1,max=100" desc:"Sign-in and enrolment attempts (begun or failed) the console accepts per admin.signin_window, from all browsers together; one more is answered 429."`
	SignInWindow time.Duration `koanf:"signin_window" default:"5m" validate:"min=1m,max=1h" desc:"The window admin.signin_limit counts in."`
	MaxPending   int           `koanf:"max_pending" default:"32" validate:"min=4,max=256" desc:"Challenges and pending confirmations held at once; when full, expired ones are dropped first and a new one is refused."`
	MaxSessions  int           `koanf:"max_sessions" default:"4" validate:"min=1,max=16" desc:"Console sessions held at once; a new sign-in beyond it ends the oldest session."`
	AuditPage    int           `koanf:"audit_page" default:"100" validate:"min=10,max=1000" desc:"Audit records the console's audit view shows, newest first."`
}

// crossFieldAdmin holds the console to the loopback and its origin to the
// relying-party id (THR-018).
func crossFieldAdmin(c *Config) Errors {
	var out Errors
	a := c.Admin
	if a.Enabled && !c.Access.URL.IsSet() {
		out = append(out, "admin.enabled: needs access.url; the console runs the key lifecycle against the access store")
	}
	if ap, err := netip.ParseAddrPort(a.ListenAddr); err != nil {
		out = append(out, fmt.Sprintf("admin.listen_addr: %q is not a loopback IP address and port; the console answers on the loopback only", a.ListenAddr))
	} else if !ap.Addr().IsLoopback() {
		out = append(out, fmt.Sprintf("admin.listen_addr: %s is not a loopback IP address; the console answers on the loopback only", ap.Addr()))
	}
	u, err := url.Parse(a.Origin)
	if err != nil || u.Host == "" {
		// The url rule has already named a value that is no URL.
		return out
	}
	switch {
	case u.Scheme != "https" && u.Scheme != "http":
		out = append(out, "admin.origin: the scheme must be https, or http for a loopback host")
	case u.Scheme == "http" && !LoopbackHost(u.Hostname()):
		out = append(out, "admin.origin: http is accepted for localhost and loopback addresses only")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		out = append(out, "admin.origin: scheme, host and port only; no path, query, fragment or user")
	}
	if h := u.Hostname(); h != a.RPID && !strings.HasSuffix(h, "."+a.RPID) {
		out = append(out, fmt.Sprintf("admin.rp_id: %q is neither the host of admin.origin nor a parent domain of it", a.RPID))
	}
	if a.ConfirmTTL > a.SessionTTL {
		out = append(out, fmt.Sprintf("admin.confirm_ttl: %s is longer than admin.session_ttl %s", a.ConfirmTTL, a.SessionTTL))
	}
	return out
}

// LoopbackHost reports whether host is localhost or a loopback address.
func LoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
