package config

import (
	"fmt"
	"net"
	"regexp"
)

// Callers is the caller allow-list of the public listener: the owner-only
// preview admits the maintainer's own network and refuses every other
// caller (#92). An empty allow-list refuses everything: the listener fails
// closed. Opening the API to everyone is an explicit act, the list
// 0.0.0.0/0,::/0.
type Callers struct {
	AllowCIDR          []string `koanf:"allow_cidr" default:"" validate:"dive,cidr" desc:"Networks (CIDR) whose callers the public listener answers; every other caller gets 403 with an empty body, on every route, the OpenAPI document included. Empty refuses every caller (fail closed); 0.0.0.0/0,::/0 admits everyone. Comma-separated in the environment."`
	TrustedProxyHeader string   `koanf:"trusted_proxy_header" default:"" validate:"omitempty,header_name" desc:"Request header in which the edge in front of the service names the caller's address, such as X-Real-IP. Believed only from a peer inside callers.trusted_proxy_cidr; the rightmost address in it is the caller, and a value that is no address is refused. Empty: the socket peer is the caller. Set together with callers.trusted_proxy_cidr."`
	TrustedProxyCIDR   []string `koanf:"trusted_proxy_cidr" default:"" validate:"dive,cidr" desc:"Networks (CIDR) of the edge whose callers.trusted_proxy_header is believed. A request from any other peer is judged by its socket address, whatever the header says. Comma-separated in the environment."`
}

// headerNamePattern is an HTTP field name (an RFC 9110 token), held to
// letters, digits and hyphens.
var headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

// crossFieldCallers keeps the proxy pair whole and the health listener off
// the public port.
func crossFieldCallers(c *Config) Errors {
	var out Errors
	k := c.Callers
	switch {
	case k.TrustedProxyHeader != "" && len(k.TrustedProxyCIDR) == 0:
		out = append(out, "callers.trusted_proxy_header: set without callers.trusted_proxy_cidr; no peer would ever be believed")
	case k.TrustedProxyHeader == "" && len(k.TrustedProxyCIDR) > 0:
		out = append(out, "callers.trusted_proxy_cidr: set without callers.trusted_proxy_header; there is no header to believe")
	}
	// Both values have passed hostname_port, so each splits.
	_, public, _ := net.SplitHostPort(c.Server.ListenAddr)
	_, health, _ := net.SplitHostPort(c.Health.ListenAddr)
	if public == health {
		out = append(out, fmt.Sprintf("health.listen_addr: port %s is the public listener's; health is served on its own port, which the edge never routes", health))
	}
	return out
}
