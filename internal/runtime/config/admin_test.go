package config_test

import (
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestTheConsoleIsOffByDefaultAndOnTheLoopback(t *testing.T) {
	req.Covers(t, "CFG-001", "THR-018")
	c, err := config.Load(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a := c.Admin
	if a.Enabled || a.ListenAddr != "127.0.0.1:8081" || a.Origin != "http://localhost:8081" || a.RPID != "localhost" ||
		a.SessionTTL.String() != "15m0s" || a.ChallengeTTL.String() != "1m0s" || a.ConfirmTTL.String() != "2m0s" ||
		a.EnrolmentTTL.String() != "30m0s" || a.SignInLimit != 10 || a.SignInWindow.String() != "5m0s" ||
		a.MaxPending != 32 || a.MaxSessions != 4 || a.AuditPage != 100 || a.RPName == "" {
		t.Fatalf("defaults %+v", a)
	}
}

func TestTheConsoleRules(t *testing.T) {
	req.Covers(t, "CFG-004", "THR-018")
	pepper := "VFAPI_AUTH_PEPPER=" + strings.Repeat("p", 32)
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_ADMIN_ENABLED=true",
		"VFAPI_ADMIN_LISTEN_ADDR=0.0.0.0:8081",
		"VFAPI_ADMIN_ORIGIN=http://console.example.org:8081/path?q=1",
		"VFAPI_ADMIN_RP_ID=example.com",
		"VFAPI_ADMIN_CONFIRM_TTL=10m",
		"VFAPI_ADMIN_SESSION_TTL=5m",
	}})
	assertRefused(t, err,
		"admin.enabled: needs access.url",
		"admin.listen_addr: 0.0.0.0 is not a loopback IP address",
		"admin.origin: http is accepted for localhost and loopback addresses only",
		"admin.origin: scheme, host and port only",
		`admin.rp_id: "example.com" is neither the host of admin.origin nor a parent domain of it`,
		"admin.confirm_ttl: 10m0s is longer than admin.session_ttl 5m0s",
	)
	for addr, want := range map[string]string{
		"192.0.2.10:8081": "admin.listen_addr: 192.0.2.10 is not a loopback IP address",
		"[::]:8081":       "admin.listen_addr: :: is not a loopback IP address",
		"localhost:8081":  `admin.listen_addr: "localhost:8081" is not a loopback IP address and port`,
	} {
		_, err := config.Load(config.Options{Environ: []string{"VFAPI_ADMIN_LISTEN_ADDR=" + addr}})
		assertRefused(t, err, want)
	}
	_, err = config.Load(config.Options{Environ: []string{"VFAPI_ADMIN_ORIGIN=ftp://localhost:8081"}})
	assertRefused(t, err, "admin.origin: the scheme must be https, or http for a loopback host")
	_, err = config.Load(config.Options{Environ: []string{"VFAPI_ADMIN_ORIGIN=not a url"}})
	assertRefused(t, err, "admin.origin: fails the url rule")
	for _, ok := range [][]string{
		{"VFAPI_ADMIN_ENABLED=true", "VFAPI_ACCESS_URL=postgres://a@b/c", pepper},
		{"VFAPI_ADMIN_LISTEN_ADDR=[::1]:9000", "VFAPI_ADMIN_ORIGIN=http://localhost:9000/"},
		{"VFAPI_ADMIN_LISTEN_ADDR=[::1]:9000", "VFAPI_ADMIN_ORIGIN=https://console.example.org", "VFAPI_ADMIN_RP_ID=example.org"},
	} {
		if _, err := config.Load(config.Options{Environ: ok}); err != nil {
			t.Errorf("%v: %v", ok, err)
		}
	}
}

func TestLoopbackHost(t *testing.T) {
	req.Covers(t, "THR-018")
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "192.0.2.1": false, "example.org": false} {
		if config.LoopbackHost(host) != want {
			t.Errorf("%s: want %v", host, want)
		}
	}
}
