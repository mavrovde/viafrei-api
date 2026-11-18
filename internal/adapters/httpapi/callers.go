package httpapi

import (
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// callerGate is the public listener's allow-list (#92): a caller outside
// callers.allow_cidr gets 403 with an empty body and no header of the
// service, on every route, before anything else runs, so a refusal says
// nothing about which paths exist. An empty list refuses every caller.
type callerGate struct {
	allow   []netip.Prefix
	header  string
	proxies []netip.Prefix
	log     *slog.Logger
}

func newCallerGate(c config.Callers, log *slog.Logger) callerGate {
	return callerGate{
		allow:   TrustedProxies(c.AllowCIDR),
		header:  c.TrustedProxyHeader,
		proxies: TrustedProxies(c.TrustedProxyCIDR),
		log:     log,
	}
}

func (g callerGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a, ok := g.caller(r); ok && inPrefixes(a, g.allow) {
			next.ServeHTTP(w, r)
			return
		}
		// The address is not logged: the line says that a caller was
		// refused, not who.
		g.log.Info("caller refused", "method", r.Method, "path", r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
	})
}

// caller is the address the allow-list judges: the socket peer, or, when
// the peer is inside callers.trusted_proxy_cidr and sends
// callers.trusted_proxy_header, the rightmost address in that header (the
// one the edge wrote; it overwrites or appends). ok is false when the peer
// is no address, or a trusted edge sent a value that is none: an
// unreadable answer is refused, never guessed.
func (g callerGate) caller(r *http.Request) (netip.Addr, bool) {
	p, err := netip.ParseAddr(peer(r.RemoteAddr))
	if err != nil {
		return netip.Addr{}, false
	}
	p = p.Unmap()
	if g.header == "" || !inPrefixes(p, g.proxies) {
		return p, true
	}
	values := r.Header.Values(g.header)
	if len(values) == 0 {
		return p, true
	}
	list := strings.Split(values[len(values)-1], ",")
	a, err := netip.ParseAddr(strings.TrimSpace(list[len(list)-1]))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
