package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tiny is a profile that runs every scenario in a few hundred milliseconds
// against the fakes below.
var tiny = profile{
	rate: 200, warm: 20 * time.Millisecond, soak: 80 * time.Millisecond,
	step: 20 * time.Millisecond, maxRate: 1600,
	spikeBase: 10 * time.Millisecond, spikeBurst: 10 * time.Millisecond, recover: 20 * time.Millisecond,
	fault: 40 * time.Millisecond, faultRate: 100, after: 5, burst: 5,
	sample: 5 * time.Millisecond, ready: 60 * time.Millisecond, settle: 2 * time.Millisecond,
}

var testBudgets = map[string]budget{
	classLookup:   {40 * time.Millisecond, 120 * time.Millisecond, 300 * time.Millisecond},
	classNear:     {60 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond},
	classComposed: {300 * time.Millisecond, 800 * time.Millisecond, 1500 * time.Millisecond},
}

var errFake = errors.New("fake failure")

// fakeDocker answers docker compose calls in process.
type fakeDocker struct {
	mu     sync.Mutex
	calls  []string
	answer func(args string) (string, error)
}

func (f *fakeDocker) run(_ context.Context, args ...string) (string, error) {
	a := strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, a)
	f.mu.Unlock()
	return f.answer(a)
}

func (f *fakeDocker) called(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// healthyDocker answers like the test stack at rest.
func healthyDocker(a string) (string, error) {
	switch {
	case strings.Contains(a, "state <> 'idle'"):
		return "0\n", nil
	case strings.Contains(a, "pg_stat_activity"):
		return "2|0\n", nil
	case strings.Contains(a, "pg_locks"):
		return "1\n", nil
	case strings.Contains(a, "begin; lock table"):
		time.Sleep(20 * time.Millisecond)
		return "", nil
	case a == "ps -q api":
		return "c1\n", nil
	case a == "top c1 -o rss":
		return "RSS\n2048\n1024\n", nil
	}
	return "", nil
}

// problem writes an RFC 9457 problem of slug.
func problem(w http.ResponseWriter, status int, slug string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"https://api.example.test/problems/%s","status":%d}`, slug, status)
}

// answer writes a 2xx answer with the meta a scenario reads.
func answer(w http.ResponseWriter, meta map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}, "meta": meta})
}

var composedOK = map[string]any{"parts": []any{
	map[string]any{"part": "charging", "status": "ok"},
	map[string]any{"part": "parking", "status": "timeout"},
}}

// healthyAPI answers every request like a healthy instance.
func healthyAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/overview"):
		answer(w, composedOK)
	default:
		answer(w, map[string]any{})
	}
}

// toxi is a fake fault proxy: fail decides which calls fail.
type toxi struct {
	mu    sync.Mutex
	calls []string
	fail  func(method, path, body string) bool
}

func (x *toxi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	x.mu.Lock()
	x.calls = append(x.calls, r.Method+" "+r.URL.Path+" "+string(b))
	x.mu.Unlock()
	if x.fail != nil && x.fail(r.Method, r.URL.Path, string(b)) {
		http.Error(w, "proxy refused", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// world is a runner against a fake API, a fake fault proxy and fake docker.
type world struct {
	r     *runner
	api   *httptest.Server
	proxy *toxi
	dock  *fakeDocker
	hits  atomic.Int64
}

func newWorld(t *testing.T, api http.HandlerFunc, dock func(string) (string, error)) *world {
	t.Helper()
	w := &world{proxy: &toxi{}, dock: &fakeDocker{answer: dock}}
	w.api = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.hits.Add(1)
		api(rw, r)
	}))
	t.Cleanup(w.api.Close)
	ps := httptest.NewServer(w.proxy)
	t.Cleanup(ps.Close)
	w.r = newRunner(tiny, testBudgets, w.api.URL, ps.URL, "vf_test_warm", "vf_test_cold", "pl-1", 4, 100*time.Millisecond, 20*time.Millisecond)
	w.r.docker = w.dock.run
	return w
}

// checkNamed returns the check whose name contains sub, failing the test
// when there is none.
func checkNamed(t *testing.T, o *outcome, sub string) check {
	t.Helper()
	for _, c := range o.checks {
		if strings.Contains(c.name, sub) {
			return c
		}
	}
	t.Fatalf("no check named %q among %+v", sub, o.checks)
	return check{}
}
