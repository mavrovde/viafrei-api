package config_test

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestTheCallerAllowListFailsClosed (#92, criterion 1): by default the
// allow-list is empty, no proxy is believed, and health has a port of its
// own.
func TestTheCallerAllowListFailsClosed(t *testing.T) {
	req.Covers(t, "CFG-001")
	c, err := config.Load(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	k := c.Callers
	if len(k.AllowCIDR) != 0 || k.TrustedProxyHeader != "" || len(k.TrustedProxyCIDR) != 0 || c.Health.ListenAddr != "localhost:8082" {
		t.Fatalf("defaults %+v, health %s", k, c.Health.ListenAddr)
	}
	c, err = config.Load(config.Options{Environ: []string{
		"VFAPI_CALLERS_ALLOW_CIDR=198.51.100.7/32,2001:db8::/32",
		"VFAPI_CALLERS_TRUSTED_PROXY_HEADER=X-Real-IP",
		"VFAPI_CALLERS_TRUSTED_PROXY_CIDR=10.0.0.0/8",
		"VFAPI_HEALTH_LISTEN_ADDR=:8082",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if k := c.Callers; !slices.Equal(k.AllowCIDR, []string{"198.51.100.7/32", "2001:db8::/32"}) || k.TrustedProxyHeader != "X-Real-IP" ||
		!slices.Equal(k.TrustedProxyCIDR, []string{"10.0.0.0/8"}) || c.Health.ListenAddr != ":8082" {
		t.Fatalf("loaded %+v, health %s", k, c.Health.ListenAddr)
	}
}

// TestTheCallerRules: a range that is no CIDR, a header that is no field
// name, half of the proxy pair, and a health port equal to the public one
// each refuse the boot.
func TestTheCallerRules(t *testing.T) {
	req.Covers(t, "CFG-004")
	for env, want := range map[string]string{
		"VFAPI_CALLERS_ALLOW_CIDR=198.51.100.7":        "callers.allow_cidr",
		"VFAPI_CALLERS_TRUSTED_PROXY_CIDR=10.0.0.0/33": "callers.trusted_proxy_cidr",
		"VFAPI_HEALTH_LISTEN_ADDR=nohostport":          "health.listen_addr",
	} {
		_, err := config.Load(config.Options{Environ: []string{env}})
		assertRefused(t, err, want)
	}
	for _, header := range []string{"X Real IP", "-X-Real-IP", "X-Real-IP:", "Ä"} {
		_, err := config.Load(config.Options{Environ: []string{"VFAPI_CALLERS_TRUSTED_PROXY_HEADER=" + header, "VFAPI_CALLERS_TRUSTED_PROXY_CIDR=10.0.0.0/8"}})
		assertRefused(t, err, "callers.trusted_proxy_header: fails the header_name rule")
	}
	_, err := config.Load(config.Options{Environ: []string{"VFAPI_CALLERS_TRUSTED_PROXY_HEADER=X-Real-IP"}})
	assertRefused(t, err, "callers.trusted_proxy_header: set without callers.trusted_proxy_cidr")
	_, err = config.Load(config.Options{Environ: []string{"VFAPI_CALLERS_TRUSTED_PROXY_CIDR=10.0.0.0/8"}})
	assertRefused(t, err, "callers.trusted_proxy_cidr: set without callers.trusted_proxy_header")
	_, err = config.Load(config.Options{Environ: []string{"VFAPI_SERVER_LISTEN_ADDR=:9000", "VFAPI_HEALTH_LISTEN_ADDR=localhost:9000"}})
	assertRefused(t, err, "health.listen_addr: port 9000 is the public listener's")
	if _, err := config.Load(config.Options{Environ: []string{"VFAPI_SERVER_LISTEN_ADDR=:9000", "VFAPI_HEALTH_LISTEN_ADDR=:9001"}}); err != nil {
		t.Fatalf("two ports: %v", err)
	}
}
