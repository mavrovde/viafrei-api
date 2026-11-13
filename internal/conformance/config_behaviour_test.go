package conformance_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/logging"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// behaviour is one key's proof: setting the key to value must change what
// observe reports, compared with the defaults. A key that is declared but
// read by nothing observes the same thing twice and fails.
type behaviour struct {
	value   string
	observe func(t *testing.T, cfg *config.Config) string
}

const baseURL = "postgres://reader:pw@db.invalid:6000/x"

func get(t *testing.T, cfg *config.Config, target string) *httptest.ResponseRecorder {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: cfg, ContractVersion: "0.1"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	return w
}

func server(t *testing.T, cfg *config.Config) *http.Server {
	t.Helper()
	s, err := httpapi.NewServer(httpapi.Deps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func poolConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	pc, err := store.PoolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := pc.ConnConfig.BuildContextWatcherHandler(nil)
	return fmt.Sprintf("%s:%d max=%d min=%d connect=%s life=%s idle=%s app=%s watcher=%+v mode=%s statements=%d descriptions=%d",
		pc.ConnConfig.Host, pc.ConnConfig.Port, pc.MaxConns, pc.MinConns, pc.ConnConfig.ConnectTimeout,
		pc.MaxConnLifetime, pc.MaxConnIdleTime, pc.ConnConfig.RuntimeParams["application_name"], h,
		pc.ConnConfig.DefaultQueryExecMode, pc.ConnConfig.StatementCacheCapacity, pc.ConnConfig.DescriptionCacheCapacity)
}

type slowReader struct{ delay time.Duration }

func (r slowReader) ContractVersion(ctx context.Context) (string, error) {
	select {
	case <-time.After(r.delay):
		return contract.Version(), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func behaviours() map[string]behaviour {
	return map[string]behaviour{
		"server.listen_addr": {"localhost:9099", func(t *testing.T, c *config.Config) string { return server(t, c).Addr }},
		"server.public_base_url": {"https://example.test", func(t *testing.T, c *config.Config) string {
			return get(t, c, "/v1/meta").Body.String()
		}},
		"server.read_header_timeout": {"3s", func(t *testing.T, c *config.Config) string { return server(t, c).ReadHeaderTimeout.String() }},
		"server.read_timeout":        {"7s", func(t *testing.T, c *config.Config) string { return server(t, c).ReadTimeout.String() }},
		"server.write_timeout":       {"7s", func(t *testing.T, c *config.Config) string { return server(t, c).WriteTimeout.String() }},
		"server.idle_timeout":        {"30s", func(t *testing.T, c *config.Config) string { return server(t, c).IdleTimeout.String() }},
		"server.max_header_bytes":    {"4096", func(t *testing.T, c *config.Config) string { return headerVerdict(t, c) }},
		"server.max_uri_bytes": {"2048", func(t *testing.T, c *config.Config) string {
			return fmt.Sprint(get(t, c, "/v1/meta?pad="+strings.Repeat("a", 3000)).Code)
		}},
		"server.shutdown_grace": {"3s", func(t *testing.T, c *config.Config) string {
			ctx, cancel := httpapi.ShutdownContext(c)
			defer cancel()
			d, _ := ctx.Deadline()
			return time.Until(d).Round(time.Second).String()
		}},
		"http.hsts_max_age": {"1h", func(t *testing.T, c *config.Config) string {
			return get(t, c, "/v1/meta").Header().Get("Strict-Transport-Security")
		}},
		"db.url":               {"postgres://reader:pw@other.invalid:6001/x", poolConfig},
		"db.pool_max":          {"9", poolConfig},
		"db.pool_min":          {"0", poolConfig},
		"db.connect_timeout":   {"500ms", poolConfig},
		"db.max_conn_lifetime": {"2h", poolConfig},
		"db.max_conn_idle":     {"1m", poolConfig},
		"db.cancel_grace":      {"400ms", poolConfig},
		"db.application_name":  {"another-name", poolConfig},
		"db.statement_timeout_floor": {"980ms", func(t *testing.T, c *config.Config) string {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, ok := store.New(nil, c).Budget(ctx)
			return fmt.Sprint(ok)
		}},
		"deadline.request": {"1750ms", func(t *testing.T, c *config.Config) string {
			b, _ := store.New(nil, c).Budget(context.Background())
			return b.Round(100 * time.Millisecond).String()
		}},
		"deadline.reserve": {"400ms", func(t *testing.T, c *config.Config) string {
			b, _ := store.New(nil, c).Budget(context.Background())
			return b.Round(100 * time.Millisecond).String()
		}},
		"contract.expected": {"0.7", func(t *testing.T, c *config.Config) string {
			v, err := health.ExpectedVersion(c.Contract.Expected, contract.Version())
			if err != nil {
				t.Fatal(err)
			}
			return v.String()
		}},
		"health.ready_timeout": {"1s", func(t *testing.T, c *config.Config) string {
			expected, _ := health.ExpectedVersion("", contract.Version())
			return fmt.Sprint(health.NewChecker(slowReader{600 * time.Millisecond}, expected, c.Health.ReadyTimeout).Check(context.Background()).Ready)
		}},
		"health.probe_timeout": {"100ms", probeVerdict},
		"log.level": {"debug", func(t *testing.T, c *config.Config) string {
			var buf bytes.Buffer
			logging.New(c.Log, &buf).Debug("probe")
			return buf.String()
		}},
		"log.format": {"text", func(t *testing.T, c *config.Config) string {
			var buf bytes.Buffer
			logging.New(c.Log, &buf).Info("probe")
			return buf.String()[:1]
		}},
		"docs.enabled": {"false", func(t *testing.T, c *config.Config) string {
			return fmt.Sprint(get(t, c, "/v1/openapi.json").Code)
		}},
		"endpoints.disabled": {"getMeta", func(t *testing.T, c *config.Config) string {
			return fmt.Sprint(get(t, c, "/v1/meta").Code)
		}},
		"access.url":                  {"postgres://access:pw@other.invalid:6002/x", accessPool},
		"access.pool_max":             {"7", accessPool},
		"auth.environment":            {"test", authSettings},
		"auth.key_prefix_live":        {"ab_live_", authSettings},
		"auth.key_prefix_test":        {"ab_test_", authSettings},
		"auth.pepper":                 {strings.Repeat("c", 32), authSettings},
		"auth.pepper_previous":        {strings.Repeat("d", 32), authSettings},
		"auth.key_cache_ttl":          {"30s", authSettings},
		"auth.negative_cache_ttl":     {"0s", authSettings},
		"auth.key_cache_size":         {"50", authSettings},
		"auth.rotation_overlap":       {"24h", authSettings},
		"auth.default_expiry":         {"720h", authSettings},
		"auth.revocation_propagation": {"10s", authSettings},
		"auth.expiry_warning_days":    {"30", authSettings},
		"auth.failure_limit":          {"3", authSettings},
		"auth.failure_window":         {"10s", authSettings},
		"auth.failure_track_size":     {"20", authSettings},
		"audit.retention_days":        {"90", authSettings},
		"admin.enabled":               {"true", consoleSettings},
		"admin.listen_addr":           {"[::1]:9091", consoleSettings},
		"admin.origin":                {"http://localhost:9090", consoleSettings},
		"admin.rp_id": {"example.test", func(t *testing.T, c *config.Config) string {
			return wire.ConsoleWeb(c).RPID
		}},
		"admin.rp_name":            {"Another console", consoleSettings},
		"admin.session_ttl":        {"30m", consoleSettings},
		"admin.challenge_ttl":      {"2m", consoleSettings},
		"admin.confirm_ttl":        {"5m", consoleSettings},
		"admin.enrolment_ttl":      {"1h", consoleSettings},
		"admin.signin_limit":       {"3", consoleSettings},
		"admin.signin_window":      {"10m", consoleSettings},
		"admin.max_pending":        {"8", consoleSettings},
		"admin.max_sessions":       {"2", consoleSettings},
		"admin.audit_page":         {"20", consoleSettings},
		"geo.default_radius_m":     {"750", featureAsk("/v1/charging-options?at=1,2")},
		"pagination.default_limit": {"7", featureAsk("/v1/parking-options?at=1,2")},
		"compose.per_part_limit":   {"3", featureAsk("/v1/places/pl-1/overview")},
		"charging.status_max_age":  {"5m", chargingState},
		"compose.part_deadline": {"300ms", func(t *testing.T, c *config.Config) string {
			return wire.FeatureSettings(c).PartDeadline.String()
		}},
		"roads.ended_retention_h": {"6", func(t *testing.T, c *config.Config) string {
			return wire.FeatureSettings(c).EndedRetention.String()
		}},
		"weather.max_warnings": {"7", func(t *testing.T, c *config.Config) string {
			return fmt.Sprint(wire.FeatureSettings(c).MaxWarnings)
		}},
		"sources.disabled": {"alpha", func(t *testing.T, c *config.Config) string {
			register := []access.Terms{{Family: "alpha", UseClasses: access.UseClasses, Reviewed: true}}
			return fmt.Sprint(access.NewGate(register, access.Plan{UseClass: "consumer", Sources: []string{"alpha"}}, nil, c.Sources.Disabled).Allowed())
		}},
	}
}

// headerVerdict serves the configured server on the loopback and sends a
// 10 KiB header block (net/http allows 4 KiB above the limit): accepted at the default, refused with 431 at 4096.
func headerVerdict(t *testing.T, c *config.Config) string {
	t.Helper()
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := server(t, c)
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/healthz", nil)
	r.Header.Set("X-Pad", strings.Repeat("b", 10*1024))
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return fmt.Sprint(resp.StatusCode)
}

// probeVerdict points the healthcheck at a loopback server that answers in
// 300ms: inside the default probe_timeout, outside 100ms.
func probeVerdict(t *testing.T, c *config.Config) string {
	t.Helper()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer slow.Close()
	_, port, _ := net.SplitHostPort(slow.Listener.Addr().String())
	probe := *c
	probe.Server.ListenAddr = "localhost:" + port
	return fmt.Sprint(httpapi.Probe(context.Background(), &probe) == nil)
}

func TestEveryKeyChangesBehaviour(t *testing.T) {
	req.Covers(t, "CFG-006")
	all := behaviours()
	for k, b := range limitBehaviours() {
		all[k] = b
	}
	for k, b := range perfBehaviours() {
		all[k] = b
	}
	for _, f := range config.Fields() {
		b, ok := all[f.Key]
		if !ok {
			t.Errorf("%s: no behaviour test; add one to behaviours()", f.Key)
			continue
		}
		delete(all, f.Key)
		t.Run(f.Key, func(t *testing.T) {
			t.Parallel()
			base := []string{"VFAPI_DB_URL=" + baseURL}
			def, err := config.Load(config.Options{Environ: base})
			if err != nil {
				t.Fatal(err)
			}
			env := base
			if f.Key == "db.url" {
				env = nil
			}
			if f.Key == "access.url" {
				env = append(env, "VFAPI_AUTH_PEPPER="+strings.Repeat("e", 32))
			}
			if f.Key == "admin.enabled" { // the console needs an access store
				env = append(env, "VFAPI_ACCESS_URL=postgres://access:pw@db.invalid:6002/x", "VFAPI_AUTH_PEPPER="+strings.Repeat("e", 32))
			}
			if f.Key == "admin.rp_id" { // the id must cover the origin's host
				env = append(env, "VFAPI_ADMIN_ORIGIN=https://console.example.test")
			}
			changed, err := config.Load(config.Options{Environ: append(env, f.Env+"="+b.value)})
			if err != nil {
				t.Fatal(err)
			}
			before, after := b.observe(t, def), b.observe(t, changed)
			if before == after {
				t.Fatalf("setting %s=%s changed nothing observable (%q)", f.Env, b.value, before)
			}
		})
	}
	for key := range all {
		t.Errorf("behaviour for %s, which is not a declared key", key)
	}
}

// authSettings is what the authenticator and the key lifecycle are built
// with; their own tests prove each setting drives their behaviour.
func authSettings(t *testing.T, c *config.Config) string {
	t.Helper()
	return fmt.Sprintf("%+v", wire.AuthSettings(c))
}

// consoleSettings is what the owner console is built with; the console's
// own tests prove each setting drives its behaviour. The pepper is left out.
func consoleSettings(t *testing.T, c *config.Config) string {
	t.Helper()
	addr, on := wire.ConsoleListener(c)
	return fmt.Sprintf("%s %v %+v %v %+v", addr, on, wire.ConsoleGuard(c), wire.ConsoleOwners(c).EnrolmentTTL, wire.ConsoleWeb(c))
}

func accessPool(t *testing.T, c *config.Config) string {
	t.Helper()
	pc, err := accessstore.PoolConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s:%d max=%d", pc.ConnConfig.Host, pc.ConnConfig.Port, pc.MaxConns)
}
