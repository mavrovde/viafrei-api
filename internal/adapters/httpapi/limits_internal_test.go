package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/domain/limits"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestAnAddressThatIsNoAddressIsNeverTrusted(t *testing.T) {
	req.Covers(t, "SEC-051")
	if isTrusted("proxy.invalid", TrustedProxies([]string{"0.0.0.0/0"})) {
		t.Fatal("a host name was trusted")
	}
	if c, ok := parseForwarded(nil, nil); ok || c != "" {
		t.Fatalf("no header: %q %v", c, ok)
	}
}

// X-Forwarded-For is walked from the right past every trusted hop: behind
// two trusted proxies the client is the entry left of both, and whatever
// the client wrote further left is never read (RATE-001, SEC-051).
func TestTheForwardedClientIsTheRightmostUntrustedEntry(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	trusted := TrustedProxies([]string{"10.0.0.0/8", "2001:db8:ffff::/48"})
	for _, tc := range []struct {
		values []string
		client string
		ok     bool
	}{
		{[]string{"spoofed, 198.51.100.4, 192.0.2.7, 10.1.1.1, 10.2.2.2"}, "192.0.2.7", true},
		{[]string{"192.0.2.7, 10.1.1.1", "2001:db8:ffff::1"}, "192.0.2.7", true}, // split over two header lines
		{[]string{"::ffff:192.0.2.7 ,10.1.1.1"}, "192.0.2.7", true},
		{[]string{"10.3.3.3, 10.1.1.1"}, "10.3.3.3", true}, // every hop trusted: the leftmost
		{[]string{"192.0.2.7, garbage, 10.1.1.1"}, "", false},
		{[]string{"garbage, 192.0.2.7, 10.1.1.1"}, "192.0.2.7", true}, // left of the client: never read
		{[]string{""}, "", false},
	} {
		c, ok := parseForwarded(tc.values, trusted)
		if c != tc.client || ok != tc.ok {
			t.Errorf("%q: got %q %v, want %q %v", tc.values, c, ok, tc.client, tc.ok)
		}
	}
}

// Behind two trusted proxies the per-address gate is charged with the
// client, not with the nearer proxy.
func TestTwoTrustedHopsChargeTheClient(t *testing.T) {
	req.Covers(t, "RATE-001", "SEC-051")
	var got string
	h := withClient(TrustedProxies([]string{"10.0.0.0/8"}))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = clientFrom(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:4000"
	r.Header.Set("X-Forwarded-For", "203.0.113.1, 192.0.2.7, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "192.0.2.7" {
		t.Fatalf("charged %q, want 192.0.2.7", got)
	}
}

func TestWithoutAClientTheAddressIsThePeer(t *testing.T) {
	req.Covers(t, "SEC-052")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.7:4000"
	if got := clientAddress(humatest.NewContext(nil, r, httptest.NewRecorder())); got != "192.0.2.7" {
		t.Fatalf("got %q", got)
	}
}

func TestTheRateLimitFieldsNameEveryQuota(t *testing.T) {
	req.Covers(t, "RATE-006")
	p := ratelimit.Plan{Rate: limits.Rate{Per: 10, Burst: 20}, Quota: limits.Quota{Day: 100, Month: 1000}}
	v := ratelimit.Verdict{Burst: limits.Decision{Remaining: 19, Reset: 100 * time.Millisecond},
		DayLeft: 99, DayReset: time.Hour, MonthLeft: 999, MonthReset: 48 * time.Hour}
	policy, state := rateLimitFields(p, v)
	if policy != `"burst";q=20;w=2, "day";q=100;w=86400, "month";q=1000;w=2592000` {
		t.Errorf("policy %s", policy)
	}
	if state != `"burst";r=19;t=1, "day";r=99;t=3600, "month";r=999;t=172800` {
		t.Errorf("state %s", state)
	}
}
