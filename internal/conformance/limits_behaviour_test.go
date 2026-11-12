package conformance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
)

// anyKey accepts every presented key as one key of a plan with a large
// bucket and no quota.
type anyKey struct{}

func (anyKey) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{Key: access.Record{ID: "abcdefghij", Environment: access.Live, ExpiresAt: time.Now().Add(time.Hour),
		Plan: access.Plan{Name: "p", UseClass: "consumer", Tier: "standard", Rate: 100, Burst: 100}}}, nil
}

// shedding is a source register whose every read the admission sheds.
type shedding struct{}

func (shedding) Register(context.Context) ([]access.Terms, error) {
	return nil, &admission.Refusal{RetryAfter: time.Second}
}

func (shedding) Sources(context.Context, []string) ([]access.Source, error) { return nil, nil }

// anonymousCodes sends six keyless requests from one peer through the
// per-address gate built from c, each naming another forwarded address,
// and returns their status codes.
func anonymousCodes(t *testing.T, c *config.Config) string {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, Addresses: ratelimit.NewIPLimiter(wire.AddressSettings(c), nil)})
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for i := range 6 {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
		r.RemoteAddr = "127.0.0.1:4000"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", i+1))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		codes = append(codes, fmt.Sprint(w.Code))
	}
	return strings.Join(codes, ",")
}

// sixAddressesOfOneSubnet sends six keyless requests from six IPv6
// addresses of one /64 and returns their status codes: by default they
// share one bucket, so the sixth is over the burst.
func sixAddressesOfOneSubnet(t *testing.T, c *config.Config) string {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, Addresses: ratelimit.NewIPLimiter(wire.AddressSettings(c), nil)})
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for i := range 6 {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
		r.RemoteAddr = fmt.Sprintf("[2001:db8:0:1::%x]:4000", i+1)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		codes = append(codes, fmt.Sprint(w.Code))
	}
	return strings.Join(codes, ",")
}

// keyed asks for /v1/key with a key the stub accepts, under the key gates
// built from c and a register the admission sheds; it returns the status
// and the RateLimit fields.
func keyed(t *testing.T, c *config.Config) string {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, Auth: anyKey{}, Sources: shedding{},
		Keys: ratelimit.NewKeyLimiter(wire.QuotaZone(c), nil, nil)})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/key", nil)
	r.Header.Set("Authorization", "Bearer any")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return fmt.Sprintf("%d %q %q", w.Code, w.Header().Get("RateLimit-Policy"), w.Header().Get("RateLimit"))
}

func admissionSettings(t *testing.T, c *config.Config) string {
	t.Helper()
	return fmt.Sprintf("%+v", wire.AdmissionSettings(c))
}

func breakerSettings(t *testing.T, c *config.Config) string {
	t.Helper()
	return fmt.Sprintf("%+v", wire.BreakerSettings(c))
}

func addressSettings(t *testing.T, c *config.Config) string {
	t.Helper()
	return fmt.Sprintf("%+v", wire.AddressSettings(c))
}

func sharedBudget(t *testing.T, c *config.Config) string {
	t.Helper()
	return fmt.Sprintf("%+v", wire.SharedBudget(c))
}

func meteringSettings(t *testing.T, c *config.Config) string {
	t.Helper()
	return fmt.Sprintf("%+v", wire.MeteringSettings(c))
}

// limitBehaviours are the behaviours of the rate, quota, tier, admission
// and metering keys. Settings are compared through what the wire builds;
// the packages' own tests prove each setting drives their behaviour.
func limitBehaviours() map[string]behaviour {
	return map[string]behaviour{
		"server.trusted_proxies": {"127.0.0.0/8", anonymousCodes},
		"ratelimit.anon_rps":     {"0", anonymousCodes},
		"ratelimit.anon_burst": {"2", func(t *testing.T, c *config.Config) string {
			// One forwarded address for all: the untrusted peer is the client.
			return anonymousCodes(t, c)
		}},
		"ratelimit.headers":                  {"false", keyed},
		"ratelimit.ip_state_ttl":             {"2m", addressSettings},
		"ratelimit.ip_track_size":            {"500", addressSettings},
		"ratelimit.ipv6_prefix":              {"128", sixAddressesOfOneSubnet},
		"quota.reset_tz":                     {"UTC", func(t *testing.T, c *config.Config) string { return wire.QuotaZone(c).String() }},
		"tiers.critical.reserved":            {"2", admissionSettings},
		"tiers.critical.queue_len":           {"8", admissionSettings},
		"tiers.critical.queue_max_wait":      {"500ms", admissionSettings},
		"tiers.standard.reserved":            {"1", admissionSettings},
		"tiers.standard.queue_len":           {"8", admissionSettings},
		"tiers.standard.queue_max_wait":      {"300ms", admissionSettings},
		"tiers.bulk.reserved":                {"1", admissionSettings},
		"tiers.bulk.queue_len":               {"8", admissionSettings},
		"tiers.bulk.queue_max_wait":          {"100ms", admissionSettings},
		"tiers.bulk.max_inflight":            {"2", admissionSettings},
		"tiers.per_key_inflight":             {"2", admissionSettings},
		"admission.aging_after":              {"1s", admissionSettings},
		"admission.ewma_alpha":               {"0.5", admissionSettings},
		"admission.shed_status":              {"503", keyed},
		"breaker.failure_threshold":          {"3", breakerSettings},
		"breaker.window":                     {"1m", breakerSettings},
		"breaker.open_for":                   {"30s", breakerSettings},
		"breaker.half_open_probes":           {"1", breakerSettings},
		"metering.flush_interval":            {"10s", meteringSettings},
		"metering.max_keys_buffered":         {"200", meteringSettings},
		"metering.write_timeout":             {"500ms", meteringSettings},
		"metering.retention_days":            {"90", authSettings},
		"db.shared_budget.slots":             {"12", sharedBudget},
		"db.shared_budget.lock_space":        {"42", sharedBudget},
		"db.shared_budget.critical_reserved": {"0", sharedBudget},
		"db.shared_budget.max_wait":          {"50ms", sharedBudget},
		"db.shared_budget.retry_interval":    {"5ms", sharedBudget},
		"db.shared_budget.retry_after":       {"2s", sharedBudget},
	}
}
