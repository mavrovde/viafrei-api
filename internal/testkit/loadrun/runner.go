package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The endpoint classes of NFR-PERF-003 the request mix uses.
const (
	classLookup   = "lookup"
	classNear     = "near"
	classComposed = "composed"
)

// request is one request of a scenario.
type request struct {
	class  string
	path   string
	cold   bool // presents the cold key instead of the warm one
	keep   bool // keeps the decoded body for a scenario's own check
	cached bool // goes to the instance with the result cache
}

// result is one answer, or a transport error (status 0).
type result struct {
	class   string
	status  int
	problem string // the last segment of the problem type of a 4xx or 5xx
	retry   bool   // Retry-After was sent
	age     string // the Age header, empty when none was sent
	took    time.Duration
	sent    time.Time // when the request left
	body    map[string]any
}

// sample is one read of the storage backends of the service's role.
type sample struct {
	conns   int // connections of the read-only role
	overdue int // of which running a statement past deadline + cancel grace + 100 ms
}

type runner struct {
	p        profile
	budgets  map[string]budget
	api      string
	proxy    string
	key      string
	cold     string
	place    string
	poolMax  int
	limit    time.Duration // the deadline plus 50 ms (NFR-PERF-001)
	overdue  time.Duration // the deadline plus the cancel grace plus 100 ms (NFR-PERF-002)
	fast     time.Duration // a refusal is fast within this (NFR-CAP-003)
	queued   time.Duration // the standard tier's queue_max_wait: a waiter is shed by then (RATE-011)
	brk      breakerSettings
	cached   string        // base URL of the instance with the result cache (NFR-RES-004)
	staleFor time.Duration // its cache.stale_if_error.parking
	app      string        // db.application_name of the instance under test
	// freshFor is the longest of its cache.max_age.parking,
	// cache.max_age.energy and cache.versions_ttl: within it the instance
	// replays a stored answer as fresh, from memory, storage or not.
	freshFor time.Duration
	http     *http.Client
	docker   func(context.Context, ...string) (string, error) // docker compose on the stack's project
	host     func(context.Context, ...string) (string, error) // the plain docker CLI (docker top)
	pollRSS  bool
	readyURL string
}

func newRunner(p profile, b map[string]budget, api, proxy, key, cold, place string, poolMax int, deadline, grace time.Duration) *runner {
	limit := deadline + 50*time.Millisecond
	return &runner{
		p: p, budgets: b, api: api, proxy: proxy, key: key, cold: cold, place: place, poolMax: poolMax,
		limit: limit, overdue: deadline + grace + 100*time.Millisecond, fast: 50 * time.Millisecond,
		http: newHTTPClient(limit), pollRSS: true, readyURL: api + "/readyz", app: "viafrei-api",
	}
}

// The request mix (benchmark-shaped): two near searches for each composed
// answer.
func (r *runner) charging() request {
	return request{class: classNear, path: "/v1/charging-options?at=50.82,6.0&radius_m=20000"}
}

func (r *runner) parking() request {
	return request{class: classNear, path: "/v1/parking-options?at=50.39,10.23&radius_m=50000"}
}

func (r *runner) overview() request {
	return request{class: classComposed, path: "/v1/places/" + url.PathEscape(r.place) + "/overview?needs=charging,parking"}
}

func (r *runner) lookup() request { return request{class: classLookup, path: "/v1/key"} }

func (r *runner) mix(i int) request {
	switch i % 5 {
	case 0, 1:
		return r.charging()
	case 2, 3:
		return r.parking()
	default:
		return r.overview()
	}
}

// do sends one request and measures it until the body is read.
func (r *runner) do(ctx context.Context, q request) result {
	res := result{class: q.class, sent: time.Now()}
	base := r.api
	if q.cached {
		base = r.cached
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+q.path, nil)
	if err != nil {
		return res
	}
	key := r.key
	if q.cold {
		key = r.cold
	}
	req.Header.Set("Authorization", "Bearer "+key)
	start := time.Now()
	resp, err := r.http.Do(req)
	if err != nil {
		res.took = time.Since(start)
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	res.took = time.Since(start)
	res.status = resp.StatusCode
	res.retry = resp.Header.Get("Retry-After") != ""
	res.age = resp.Header.Get("Age")
	if res.status >= 400 {
		var p struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(b, &p)
		res.problem = path.Base(p.Type)
	}
	if q.keep {
		_ = json.Unmarshal(b, &res.body)
	}
	return res
}

// drive sends requests open-loop at rate for d: each one leaves at its
// scheduled instant whatever the earlier ones are doing, so a slow service
// meets more concurrent requests rather than a gentler client.
func (r *runner) drive(ctx context.Context, rate float64, d time.Duration, next func(int) request) []result {
	n := max(int(rate*d.Seconds()), 1)
	interval := time.Duration(float64(time.Second) / rate)
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make([]result, 0, n)
	)
	start := time.Now()
	for i := range n {
		if sleep(ctx, time.Until(start.Add(time.Duration(i)*interval))) != nil {
			break
		}
		wg.Add(1)
		go func(q request) {
			defer wg.Done()
			res := r.do(ctx, q)
			mu.Lock()
			out = append(out, res)
			mu.Unlock()
		}(next(i))
	}
	wg.Wait()
	return out
}

// burst sends n copies of q at once.
func (r *runner) burst(ctx context.Context, n int, q request) []result {
	out := make([]result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = r.do(ctx, q)
		}()
	}
	wg.Wait()
	return out
}

// sleep waits d, or less when ctx ends first.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// every runs fn every interval until the returned stop is called; stop
// waits for the last run.
func every(ctx context.Context, interval time.Duration, fn func(context.Context)) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			fn(ctx)
			if sleep(ctx, interval) != nil {
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// psql runs one statement on the test stack's database as its owner, over
// docker compose exec: the database is never reached over the network.
func (r *runner) psql(ctx context.Context, sql string) (string, error) {
	out, err := r.docker(ctx, "exec", "-T", "db", "psql", "-U", "owner", "-d", "contract", "-v", "ON_ERROR_STOP=1", "-tAqc", sql)
	return strings.TrimSpace(out), err
}

// backends reads the connections of the instance under test (its read-only
// role and its application_name: the instance with the result cache reads
// as the same role under another name) and those running a statement for
// longer than the overdue limit.
func (r *runner) backends(ctx context.Context) (sample, error) {
	out, err := r.psql(ctx, fmt.Sprintf(
		"select count(*), count(*) filter (where state = 'active' and now() - query_start > interval '%d milliseconds') from pg_stat_activity where usename = 'api_reader' and application_name = '%s'",
		r.overdue.Milliseconds(), r.app))
	if err != nil {
		return sample{}, err
	}
	var s sample
	if _, err := fmt.Sscanf(out, "%d|%d", &s.conns, &s.overdue); err != nil {
		return sample{}, fmt.Errorf("pg_stat_activity: %w %q", errNoSample, out)
	}
	return s, nil
}

// busy counts the instance under test's backends that are not idle.
func (r *runner) busy(ctx context.Context) (int, error) {
	out, err := r.psql(ctx, fmt.Sprintf("select count(*) from pg_stat_activity where usename = 'api_reader' and application_name = '%s' and state <> 'idle'", r.app))
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("pg_stat_activity: %w %q", errNoSample, out)
	}
	return n, nil
}

// rss reads the resident memory of the API container, in bytes, from the
// host's process table (docker top), so the image needs no tools.
func (r *runner) rss(ctx context.Context) (int64, error) {
	id, err := r.docker(ctx, "ps", "-q", "api")
	if err != nil {
		return 0, err
	}
	if id = strings.TrimSpace(id); id == "" {
		return 0, fmt.Errorf("docker compose ps: %w: no api container", errNoSample)
	}
	// docker compose top takes no ps options; the plain docker top does,
	// and it needs the pid column to map the processes.
	out, err := r.host(ctx, "top", id, "-o", "pid,rss")
	if err != nil {
		return 0, err
	}
	var total int64
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		if len(f) != 2 {
			return 0, fmt.Errorf("docker top: %w %q", errNoSample, l)
		}
		kb, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("docker top: %w %q", errNoSample, l)
		}
		total += kb * 1024
	}
	if total == 0 {
		return 0, fmt.Errorf("docker top: %w: no process", errNoSample)
	}
	return total, nil
}

// toxi calls the fault proxy's API; anything but a 2xx is an error.
func (r *runner) toxi(ctx context.Context, method, p string, body any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.proxy+p, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("fault proxy %s %s: %d %s", method, p, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// The fault proxy's two routes, named in test/load/toxiproxy.json.
const (
	proxyStorage = "storage"
	proxyAccess  = "access"
)

func (r *runner) setProxy(ctx context.Context, name string, enabled bool) error {
	return r.toxi(ctx, http.MethodPost, "/proxies/"+name, map[string]bool{"enabled": enabled})
}

func (r *runner) addLatency(ctx context.Context, name string, d time.Duration) error {
	return r.toxi(ctx, http.MethodPost, "/proxies/"+name+"/toxics", map[string]any{
		"name": "latency", "type": "latency", "stream": "downstream",
		"attributes": map[string]int64{"latency": d.Milliseconds()},
	})
}

// reset enables every route of the fault proxy and removes every toxic.
func (r *runner) reset(ctx context.Context) error {
	return r.toxi(ctx, http.MethodPost, "/reset", nil)
}

// The tables the feature reads touch; a lock on them hangs every storage
// read of the request mix, a lock on the parking ones only the parking part.
var (
	featureTables = []string{"contract.place", "contract.charging_site", "contract.charge_point", "contract.charge_point_state_observation", "contract.parking_facility", "contract.occupancy_observation"}
	parkingTables = []string{"contract.parking_facility", "contract.occupancy_observation"}
)

// hold takes an exclusive lock on tables for d in a session of its own, so
// every read of them hangs in storage until the service cancels it: the
// hanging query of QAS-REL-01. It returns once the lock is granted; wait
// blocks until the session has ended.
func (r *runner) hold(ctx context.Context, tables []string, d time.Duration) (wait func() error, err error) {
	errc := make(chan error, 1)
	go func() {
		_, err := r.psql(ctx, fmt.Sprintf("begin; lock table %s in access exclusive mode; select pg_sleep(%.3f); commit;", strings.Join(tables, ", "), d.Seconds()))
		errc <- err
	}()
	wait = func() error { return <-errc }
	q := fmt.Sprintf("select count(*) from pg_locks where granted and mode = 'AccessExclusiveLock' and relation = '%s'::regclass", tables[0])
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case err := <-errc:
			return func() error { return err }, fmt.Errorf("the lock session ended before its lock was seen (%w)", err)
		default:
		}
		if out, err := r.psql(ctx, q); err == nil && out == "1" {
			return wait, nil
		}
		if err := sleep(ctx, r.p.settle); err != nil {
			return wait, err
		}
	}
	return wait, fmt.Errorf("the lock on %s was not granted within %s", tables[0], d)
}

// awaitOK polls q until it is answered 2xx, at most within.
func (r *runner) awaitOK(ctx context.Context, q request, within time.Duration) ([]result, bool) {
	return r.awaitWhere(ctx, q, within, ok)
}

// awaitWhere polls q until an answer satisfies want, at most within.
func (r *runner) awaitWhere(ctx context.Context, q request, within time.Duration, want func(result) bool) ([]result, bool) {
	var out []result
	deadline := time.Now().Add(within)
	for {
		res := r.do(ctx, q)
		out = append(out, res)
		if want(res) {
			return out, true
		}
		if !time.Now().Before(deadline) || sleep(ctx, r.p.settle) != nil {
			return out, false
		}
	}
}

// awaitReady polls /readyz until it answers 200, at most within, and
// returns how long that took.
func (r *runner) awaitReady(ctx context.Context, within time.Duration) (time.Duration, bool) {
	start := time.Now()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.readyURL, nil)
		if err != nil {
			return time.Since(start), false
		}
		if resp, err := r.http.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(start), true
			}
		}
		if time.Since(start) >= within || sleep(ctx, r.p.settle) != nil {
			return time.Since(start), false
		}
	}
}

// percentile is the nearest-rank percentile of ds (0 for none).
func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(p*float64(len(s))+0.999999) - 1
	return s[min(max(i, 0), len(s)-1)]
}

// took returns the durations of the results that match keep.
func took(rs []result, keep func(result) bool) []time.Duration {
	var out []time.Duration
	for _, r := range rs {
		if keep(r) {
			out = append(out, r.took)
		}
	}
	return out
}

func ok(r result) bool      { return r.status/100 == 2 }
func anyResult(result) bool { return true }

// refused: the service turned the request away under load (RATE-011).
func refused(r result) bool {
	return (r.status == http.StatusTooManyRequests || r.status == http.StatusServiceUnavailable) && r.problem == "overloaded"
}

func count(rs []result, keep func(result) bool) int { return len(took(rs, keep)) }
