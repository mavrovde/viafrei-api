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
	if c, ok := parseForwarded(nil); ok || c != "" {
		t.Fatalf("no header: %q %v", c, ok)
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
