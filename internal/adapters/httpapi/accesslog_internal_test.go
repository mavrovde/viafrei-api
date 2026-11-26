package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
)

// fakeNow is a clock the test moves by hand.
type fakeNow struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeNow) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }

func (f *fakeNow) advance(d time.Duration) { f.mu.Lock(); defer f.mu.Unlock(); f.t = f.t.Add(d) }

// TestTheRequestLineSaysWhereTheTimeWent: the request's log line carries how
// long its reads waited for a permit, how long they held one and the
// statement its deadline cut, each from the request's own tally, beside the
// fields it carried before.
func TestTheRequestLineSaysWhereTheTimeWent(t *testing.T) {
	clk := &fakeNow{t: time.Now()}
	tier := admission.TierSettings{QueueLen: 4, MaxWait: time.Second, MaxInflight: 1}
	c := admission.New(admission.Settings{Permits: 1, Tiers: [3]admission.TierSettings{tier, tier, tier}, PerKey: 1, AgingAfter: time.Hour, Alpha: 0.5}, clk.now)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hold, err := c.AcquireRead(admission.WithCaller(r.Context(), admission.Caller{Tier: admission.Standard, Key: "other"}))
		if err != nil {
			t.Error(err)
			return
		}
		go func() {
			for c.Snapshot().Waiting[admission.Standard] == 0 {
				time.Sleep(time.Millisecond)
			}
			clk.advance(40 * time.Millisecond)
			hold(true)
		}()
		release, err := c.AcquireRead(admission.WithCaller(r.Context(), admission.Caller{Tier: admission.Standard, Key: "k"}))
		if err != nil {
			t.Error(err)
			return
		}
		clk.advance(25 * time.Millisecond)
		release(true)
		admission.NoteCut(r.Context(), "api_cut")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	accessLog(log, time.Now)(handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/germany", nil))
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	// The other read's 40 ms with the permit and this one's 25 ms both
	// count: both ran under the request's tally.
	want := map[string]any{"msg": "request", "path": "/v1/germany", "status": float64(503), "admissionWaitMs": float64(40), "storageMs": float64(65), "cutStatement": "api_cut"}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("%s = %v, want %v (line %s)", k, line[k], v, buf.String())
		}
	}
	if _, ok := line["durationMs"]; !ok {
		t.Errorf("durationMs is gone: %s", buf.String())
	}
}
