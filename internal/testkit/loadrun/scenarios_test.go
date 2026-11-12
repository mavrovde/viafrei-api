package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// runOne runs the named scenario of the catalogue in w.
func runOne(t *testing.T, w *world, name string) *outcome {
	t.Helper()
	sel, err := selectScenarios(name)
	if err != nil || len(sel) != 1 {
		t.Fatalf("select %q: %v", name, err)
	}
	return w.r.runScenario(context.Background(), sel[0])
}

func mustPass(t *testing.T, o *outcome) {
	t.Helper()
	if !o.passed() {
		t.Errorf("%s: %s\n%s", o.name, o.verdict(), renderReport("test", []*outcome{o}))
	}
}

func mustFail(t *testing.T, o *outcome, sub string) {
	t.Helper()
	if o.passed() {
		t.Errorf("%s passed, want a failure", o.name)
	}
	if c := checkNamed(t, o, sub); c.ok {
		t.Errorf("%s: check %q held, want it failed", o.name, c.name)
	}
}

// brokenDocker fails every call.
func brokenDocker(string) (string, error) { return "", errFake }

// brokenAPI answers every request 500.
func brokenAPI(w http.ResponseWriter, _ *http.Request) {
	problem(w, http.StatusInternalServerError, "internal")
}

func TestSharedAssertionsFailClosed(t *testing.T) {
	req.Covers(t, "TST-007")
	w := newWorld(t, healthyAPI, brokenDocker)
	w.proxy.fail = func(string, string, string) bool { return true }
	o := w.r.runScenario(context.Background(), scenario{name: "nothing", run: func(context.Context, *runner, *outcome) {}})
	mustFail(t, o, "requests sent")
	mustFail(t, o, "storage backends read")
	mustFail(t, o, "fault proxy reset")
	o = &outcome{results: []result{{took: time.Second}}, samples: []sample{{conns: 9, overdue: 1}}}
	w.r.shared(o)
	mustFail(t, o, "no request completes later")
	mustFail(t, o, "at most db.pool_max")
	mustFail(t, o, "no storage statement older")
}

func TestSoakAndSpikeOnAHealthyStack(t *testing.T) {
	req.Covers(t, "TST-007")
	w := newWorld(t, healthyAPI, healthyDocker)
	mustPass(t, runOne(t, w, "soak"))
	mustPass(t, runOne(t, w, "spike"))
}

func TestSoakFailsWithoutItsReadings(t *testing.T) {
	w := newWorld(t, healthyAPI, brokenDocker)
	o := runOne(t, w, "soak")
	mustFail(t, o, "resident memory read")
	mustFail(t, o, "no storage connection left busy")

	// Memory that grows past 10 % after warm-up is a failure.
	var rss atomic.Int64
	rss.Store(1000)
	w.dock.answer = func(a string) (string, error) {
		if strings.HasPrefix(a, "top") {
			return "PID RSS\n7 " + strconv.FormatInt(rss.Add(100), 10) + "\n", nil
		}
		return healthyDocker(a)
	}
	mustFail(t, runOne(t, w, "soak"), "resident memory grows")
}

func TestStressFindsTheKnee(t *testing.T) {
	req.Covers(t, "TST-007")
	w := newWorld(t, healthyAPI, healthyDocker)
	mustFail(t, runOne(t, w, "stress"), "the knee is reached")

	var n atomic.Int64
	w = newWorld(t, func(rw http.ResponseWriter, r *http.Request) {
		if n.Add(1) > 6 && n.Load()%2 == 0 {
			problem(rw, http.StatusTooManyRequests, "overloaded")
			return
		}
		healthyAPI(rw, r)
	}, healthyDocker)
	o := runOne(t, w, "stress")
	if c := checkNamed(t, o, "the knee is reached"); !c.ok {
		t.Errorf("knee: %+v", c)
	}
	if c := checkNamed(t, o, "every refusal is 429"); !c.ok {
		t.Errorf("refusals: %+v", c)
	}
	if len(o.notes) != 3 {
		t.Errorf("stress stopped after %d steps, want the knee plus one: %q", len(o.notes), o.notes)
	}
}

// lockedWorld answers like a stack whose feature tables are locked while a
// lock session runs: shed is the answer under the lock.
func lockedWorld(t *testing.T, shed func(http.ResponseWriter)) *world {
	t.Helper()
	var locked atomic.Bool
	w := newWorld(t, func(rw http.ResponseWriter, r *http.Request) {
		if locked.Load() && !strings.Contains(r.URL.Path, "/overview") {
			shed(rw)
			return
		}
		healthyAPI(rw, r)
	}, nil)
	w.dock.answer = func(a string) (string, error) {
		switch {
		case strings.Contains(a, "begin; lock table"):
			// Held well past the scenario's 40 ms of load, so a slow
			// runner still sends every request under the lock.
			locked.Store(true)
			time.Sleep(150 * time.Millisecond)
			locked.Store(false)
			return "", nil
		case strings.Contains(a, "pg_locks"):
			// The lock is seen only once it is taken, as in the database.
			if locked.Load() {
				return "1\n", nil
			}
			return "0\n", nil
		}
		return healthyDocker(a)
	}
	return w
}

func TestStorageFaults(t *testing.T) {
	req.Covers(t, "TST-007")
	cut := func(rw http.ResponseWriter) { problem(rw, http.StatusServiceUnavailable, "deadline-exhausted") }
	mustPass(t, runOne(t, lockedWorld(t, cut), "hanging-query"))
	mustPass(t, runOne(t, lockedWorld(t, func(rw http.ResponseWriter) { problem(rw, http.StatusTooManyRequests, "overloaded") }), "pool-exhausted"))
	mustPass(t, runOne(t, lockedWorld(t, cut), "composed-part-timeout"))

	// A lock that answers 2xx is no hanging query; a pool that never sheds
	// is a failure.
	mustFail(t, runOne(t, lockedWorld(t, func(rw http.ResponseWriter) { answer(rw, nil) }), "hanging-query"), "a hanging read is cut")
	mustFail(t, runOne(t, lockedWorld(t, cut), "pool-exhausted"), "the admission refuses")

	for _, name := range []string{"hanging-query", "pool-exhausted", "composed-part-timeout"} {
		mustFail(t, runOne(t, newWorld(t, healthyAPI, brokenDocker), name), "tables locked")
	}
}

// breakerLife is the log, read at now, of a breaker that opened at begin,
// let probes through that failed, and was closed by a probe after the
// repair: its last half-open period begins a moment before now, after the
// repair.
func breakerLife(begin time.Time) func(now time.Time) string {
	return func(now time.Time) string {
		return breakerLog(at("closed>open", begin), at("open>half-open", begin), at("half-open>open", now.Add(-2*time.Millisecond)),
			at("open>half-open", now.Add(-time.Millisecond)), at("half-open>closed", now))
	}
}

// proxiedWorld answers like a stack behind the fault proxy: a latency toxic
// makes every read time out, a cut storage route makes it unavailable until
// the breaker opens after two failures, then refused by the breaker; a cut
// access route refuses the cold key. Its log holds breakerLife.
func proxiedWorld(t *testing.T) *world {
	t.Helper()
	var slow, storageDown, accessDown atomic.Bool
	var failed atomic.Int64
	w := newWorld(t, func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case slow.Load():
			problem(rw, http.StatusServiceUnavailable, "deadline-exhausted")
		case storageDown.Load() && failed.Add(1) > 2:
			breakerProblem(rw)
		case storageDown.Load():
			problem(rw, http.StatusServiceUnavailable, "upstream-unavailable")
		case accessDown.Load() && r.Header.Get("Authorization") == "Bearer vf_test_cold":
			problem(rw, http.StatusServiceUnavailable, "upstream-unavailable")
		default:
			healthyAPI(rw, r)
		}
	}, logs(breakerLife(time.Now())))
	w.r.brk.failures = 2
	w.proxy.fail = func(method, path, body string) bool {
		switch {
		case path == "/reset":
			slow.Store(false)
			storageDown.Store(false)
			accessDown.Store(false)
		case strings.HasSuffix(path, "/toxics"):
			slow.Store(true)
		case path == "/proxies/storage":
			storageDown.Store(strings.Contains(body, "false"))
		case path == "/proxies/access":
			accessDown.Store(strings.Contains(body, "false"))
		}
		return false
	}
	return w
}

func TestProxyFaults(t *testing.T) {
	req.Covers(t, "TST-007")
	for _, name := range []string{"slow-storage", "storage-killed", "access-store-down"} {
		mustPass(t, runOne(t, proxiedWorld(t), name))
	}

	// Every call to the fault proxy refused.
	refuse := func(string, string, string) bool { return true }
	for name, check := range map[string]string{"slow-storage": "latency injected", "storage-killed": "storage route cut", "access-store-down": "access-store route cut"} {
		w := newWorld(t, healthyAPI, healthyDocker)
		w.proxy.fail = refuse
		mustFail(t, runOne(t, w, name), check)
	}
	// The repair refused.
	for name, check := range map[string]string{"slow-storage": "latency removed", "storage-killed": "storage route restored", "access-store-down": "access-store route restored"} {
		w := proxiedWorld(t)
		inner := w.proxy.fail
		w.proxy.fail = func(method, path, body string) bool {
			inner(method, path, body)
			return path == "/reset" || strings.Contains(body, "true")
		}
		mustFail(t, runOne(t, w, name), check)
	}
	// A cache that does not hold, a storage that does not come back.
	mustFail(t, runOne(t, newWorld(t, brokenAPI, healthyDocker), "access-store-down"), "the warm key answered")
	w := proxiedWorld(t)
	w.api.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		problem(rw, http.StatusServiceUnavailable, "upstream-unavailable")
	})
	o := runOne(t, w, "storage-killed")
	mustFail(t, o, "2xx again within")
	if c := checkNamed(t, o, "each 503 within 50 ms"); !c.ok {
		t.Errorf("fast 503s: %+v", c)
	}
	mustFail(t, o, "the breaker opens after at most")
}

func TestStorageKilledAssertsTheBreaker(t *testing.T) {
	req.Covers(t, "NFR-RES-002", "TST-007")
	// The log read fails, or says nothing of the breaker.
	for _, dock := range []func(string) (string, error){
		func(a string) (string, error) {
			if strings.HasPrefix(a, "logs ") {
				return "", errFake
			}
			return healthyDocker(a)
		},
		logs(func(time.Time) string { return breakerLog() }),
		logs(func(time.Time) string {
			return `{"msg":"storage circuit breaker changed state","from":"closed","to":"open"}` + "\n"
		}),
	} {
		w := proxiedWorld(t)
		w.dock.answer = dock
		o := runOne(t, w, "storage-killed")
		mustFail(t, o, "read from the instance's log")
		mustFail(t, o, "closed > open")
		mustFail(t, o, "half-open > closed, the last change")
	}
	// A log in which the breaker never opened again after a probe, and never
	// closed: each missing change is named.
	w := proxiedWorld(t)
	begin := time.Now()
	w.dock.answer = logs(func(time.Time) string {
		return breakerLog(at("closed>open", begin), at("open>half-open", begin))
	})
	o := runOne(t, w, "storage-killed")
	mustFail(t, o, "a failed one opened it again")
	mustFail(t, o, "half-open > closed, the last change")
	if c := checkNamed(t, o, "the breaker opened"); !c.ok {
		t.Errorf("opened: %+v", c)
	}
	// A fault no longer than twice breaker.open_for asserts no re-opening.
	w = proxiedWorld(t)
	w.r.brk.openFor = time.Hour
	o = runOne(t, w, "storage-killed")
	mustPass(t, o)
	for _, c := range o.checks {
		if strings.Contains(c.name, "a failed one opened it again") {
			t.Errorf("re-opening asserted on a fault shorter than open_for: %+v", c)
		}
	}
}

// sequenced is proxiedWorld whose lost storage answers the n-th request of
// the fault as seq[n-1] says, the breaker's refusal after seq: 'u' is a
// read that reached the lost storage (503 upstream-unavailable, after
// delay), 'B' the breaker's refusal. Its log is breakerLife from when it
// was made.
func sequenced(t *testing.T, seq string, delay time.Duration) *world {
	t.Helper()
	w := proxiedWorld(t)
	var n atomic.Int64
	w.api.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if w.proxy.restored() {
			healthyAPI(rw, r)
			return
		}
		if i := int(n.Add(1)); i <= len(seq) && seq[i-1] == 'u' {
			time.Sleep(delay)
			problem(rw, http.StatusServiceUnavailable, "upstream-unavailable")
			return
		}
		breakerProblem(rw)
	})
	return w
}

// The edges of the breaker's two bounds: it opens after at most
// failure_threshold reads, and lets at most half_open_probes through in a
// half-open period before the repair; none while open.
func TestStorageKilledHoldsTheBreakerToItsBounds(t *testing.T) {
	req.Covers(t, "NFR-RES-002", "TST-007")
	const opens = "the breaker opens after at most"
	const probes = "at most breaker.half_open_probes = 2 reads reached storage in each half-open period"
	for name, tc := range map[string]struct {
		seq   string
		delay time.Duration
		check string
		ok    bool
	}{
		"opens after exactly the threshold": {"uuB", 0, opens, true},
		"opens one read late":               {"uuuB", 0, opens, false},
		// The late reads answer last: only the order they were sent in
		// shows that the breaker opened late.
		"opens late, answers out of order": {"uuuB", 35 * time.Millisecond, opens, false},
		"exactly the probes":               {"Buu", 0, probes, true},
		"one probe too many":               {"Buuu", 0, probes, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := sequenced(t, tc.seq, tc.delay)
			w.r.brk.probes = 2
			o := runOne(t, w, "storage-killed")
			if c := checkNamed(t, o, tc.check); c.ok != tc.ok {
				t.Errorf("%+v, want ok %v", c, tc.ok)
			}
		})
	}
	// Reads that reached storage while the log shows it open: the only
	// half-open period began after the repair.
	w := sequenced(t, "Bu", 0)
	w.dock.answer = logs(func(now time.Time) string {
		return breakerLog(at("closed>open", now.Add(-time.Hour)), at("open>half-open", now.Add(-time.Millisecond)), at("half-open>closed", now))
	})
	o := runOne(t, w, "storage-killed")
	if c := checkNamed(t, o, probes); c.ok || !strings.Contains(c.detail, "1 while open") {
		t.Errorf("a read while open: %+v", c)
	}
}

func TestProbesAreCountedPerPeriod(t *testing.T) {
	req.Covers(t, "NFR-RES-002", "TST-007")
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	sec := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	cs := []change{
		at("closed>open", sec(0)), at("open>half-open", sec(2)), at("half-open>open", sec(2.1)),
		at("open>half-open", sec(4)), at("half-open>open", sec(4.1)), at("open>half-open", sec(9)), at("half-open>closed", sec(9.1)),
	}
	read := func(s float64) result { return result{status: http.StatusServiceUnavailable, sent: sec(s)} }
	refusal := result{status: http.StatusServiceUnavailable, problem: "upstream-unavailable", retry: true, body: map[string]any{"detail": breakerDetail}, sent: sec(3)}
	rs := []result{
		refusal,
		read(2), read(2.05), // the first period
		read(4 - probeSlack.Seconds()), // sent a moment before the second began: one of its probes
		read(1.5),                      // sent while open, before any period
		read(6),                        // after the second period: assigned to it
	}
	per, stray := probesPerPeriod(rs, cs, sec(8))
	if len(per) != 2 || per[0] != 2 || per[1] != 2 || stray != 1 {
		t.Errorf("per %v, stray %d; want [2 2] and 1 (the period after the repair is not counted)", per, stray)
	}
	if per, stray := probesPerPeriod(rs, cs, sec(1)); len(per) != 0 || stray != 5 {
		t.Errorf("no period before the repair: per %v, stray %d", per, stray)
	}
}

func TestSlowStorageAcceptsTheOpenBreaker(t *testing.T) {
	req.Covers(t, "NFR-RES-002", "TST-007")
	w := proxiedWorld(t)
	var n atomic.Int64
	w.api.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case w.proxy.restored():
			healthyAPI(rw, r)
		case n.Add(1) <= 3:
			problem(rw, http.StatusServiceUnavailable, "deadline-exhausted")
		default:
			breakerProblem(rw)
		}
	})
	o := runOne(t, w, "slow-storage")
	mustPass(t, o)
	if c := checkNamed(t, o, "refused by the breaker"); !strings.Contains(c.detail, "3 deadline-exhausted") {
		t.Errorf("slow storage: %+v", c)
	}
}

func TestInstanceKilled(t *testing.T) {
	req.Covers(t, "TST-007")
	var down atomic.Bool
	w := newWorld(t, func(rw http.ResponseWriter, r *http.Request) {
		if down.Load() {
			problem(rw, http.StatusServiceUnavailable, "not-ready")
			return
		}
		healthyAPI(rw, r)
	}, nil)
	w.dock.answer = func(a string) (string, error) {
		switch a {
		case "kill -s SIGKILL api":
			down.Store(true)
		case "start api":
			go func() {
				time.Sleep(10 * time.Millisecond)
				down.Store(false)
			}()
		}
		return healthyDocker(a)
	}
	o := runOne(t, w, "instance-killed")
	mustPass(t, o)
	if !w.dock.called("kill -s SIGKILL api") || !w.dock.called("start api") {
		t.Errorf("calls %q", w.dock.calls)
	}

	o = runOne(t, newWorld(t, brokenAPI, brokenDocker), "instance-killed")
	mustFail(t, o, "instance killed under load")
	mustFail(t, o, "instance started")
	mustFail(t, o, "ready within")
}

func TestClockSkew(t *testing.T) {
	req.Covers(t, "TST-007")
	var at atomic.Value
	at.Store("")
	view := func(age float64, stale bool, warn bool) map[string]any {
		m := map[string]any{"freshness": []any{
			map[string]any{"family": "a", "ageS": age, "stale": stale},
			map[string]any{"family": "b"},
		}}
		if warn {
			m["warnings"] = []any{map[string]any{"code": "data_stale"}}
		}
		return m
	}
	w := newWorld(t, func(rw http.ResponseWriter, _ *http.Request) {
		switch s, _ := at.Load().(string); {
		case strings.Contains(s, "+ interval"):
			answer(rw, view(0, false, false))
		case strings.Contains(s, "- interval"):
			answer(rw, view(2_600_000, true, true))
		default:
			answer(rw, view(-5, false, false))
		}
	}, nil)
	w.dock.answer = func(a string) (string, error) {
		if strings.Contains(a, "set data_as_of = now()") {
			at.Store(a)
		}
		return healthyDocker(a)
	}
	o := runOne(t, w, "clock-skew")
	// The fake's second family carries no instant, so neither direction is
	// seen in full: the scenario must say so rather than pass.
	mustFail(t, o, "10 minutes ahead")
	if c := checkNamed(t, o, "source clock restored"); !c.ok {
		t.Errorf("restore: %+v", c)
	}

	w.api.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		s, _ := at.Load().(string)
		ahead := strings.Contains(s, "+ interval")
		m := map[string]any{"freshness": []any{map[string]any{"family": "a", "ageS": 0.0, "stale": !ahead}}}
		if !ahead {
			m["warnings"] = []any{map[string]any{"code": "partial"}, map[string]any{"code": "data_stale"}}
		}
		answer(rw, m)
	})
	mustPass(t, runOne(t, w, "clock-skew"))

	w.api.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { answer(rw, view(-5, false, false)) })
	mustFail(t, runOne(t, w, "clock-skew"), "no negative age")

	w.dock.answer = func(a string) (string, error) {
		if strings.Contains(a, "set data_as_of = now()") {
			return "", errFake
		}
		return healthyDocker(a)
	}
	mustFail(t, runOne(t, w, "clock-skew"), "30 days behind")
	mustFail(t, runOne(t, newWorld(t, healthyAPI, brokenDocker), "clock-skew"), "source clock saved")
}

func TestCatalogueAndSelection(t *testing.T) {
	req.Covers(t, "TST-007")
	if len(catalogue) != 11 {
		t.Errorf("%d scenarios", len(catalogue))
	}
	seen := map[string]bool{}
	for _, s := range catalogue {
		if seen[s.name] || s.fault == "" || s.expect == "" || s.run == nil {
			t.Errorf("scenario %+v", s)
		}
		seen[s.name] = true
	}
	if catalogue[len(catalogue)-1].name != "instance-killed" {
		t.Error("the instance must be killed last")
	}
	if sel, err := selectScenarios(""); err != nil || len(sel) != len(catalogue) {
		t.Errorf("all: %d %v", len(sel), err)
	}
	if sel, err := selectScenarios("spike,soak"); err != nil || len(sel) != 2 || sel[0].name != "soak" {
		t.Errorf("two: %v %v", sel, err)
	}
	if _, err := selectScenarios("soak,nonesuch"); err == nil {
		t.Error("an unknown scenario was skipped, not refused")
	}
}

func TestReportAndVerdict(t *testing.T) {
	req.Covers(t, "TST-007")
	pass := &outcome{name: "a", fault: "none", expect: "x", results: []result{{status: 200, took: time.Millisecond}, {status: 0}}, notes: []string{"n1"}, pending: []string{"p1"}}
	pass.add("held", true, "d")
	fail := &outcome{name: "b", fault: "f", expect: "y", results: []result{{status: 503}, {status: 429}}}
	fail.add("held", true, "")
	fail.add("broke", false, "why")
	text := renderReport("pr", []*outcome{pass, fail, {name: "c"}})
	for _, want := range []string{
		"## Load and fault scenarios, pr budget",
		"| a | 2 | 1 | 0 | 0 | 1 | 1ms | 1ms | pass |",
		"| b | 2 | 0 | 1 | 1 | 0 | 0s | 0s | FAIL (1 of 2 assertions) |",
		"| c | 0 | 0 | 0 | 0 | 0 | 0s | 0s | FAIL (0 of 0 assertions) |",
		"3 scenario(s), 2 failed.",
		"- pass: held (d)", "- **FAIL**: broke (why)", "- note: n1", "- not asserted yet: p1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	if median([]int64{3, 1, 2}) != 2 || mib(1<<20) != 1 || errSuffix(nil) != "" || errSuffix(errFake) != ", fake failure" {
		t.Error("helpers")
	}
	if !refused(result{status: 503, problem: "overloaded"}) || refused(result{status: 503, problem: "deadline-exhausted"}) {
		t.Error("refused")
	}
	if !cutOrShed(result{status: 503, problem: "deadline-exhausted", retry: true}) || cutOrShed(result{status: 503, problem: "deadline-exhausted"}) {
		t.Error("cutOrShed")
	}
	if p := parts(map[string]any{"meta": map[string]any{"parts": []any{map[string]any{"part": "x", "status": "ok"}}}}); p["x"] != "ok" {
		t.Errorf("parts %v", p)
	}
	if v := freshnessOf(nil); v != (freshnessView{}) {
		t.Errorf("freshness of nothing %+v", v)
	}
}
