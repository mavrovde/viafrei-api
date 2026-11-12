package main

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"
)

// check is one assertion of a scenario.
type check struct {
	name   string
	ok     bool
	detail string
}

// outcome is what one scenario did and found.
type outcome struct {
	name, fault, expect string
	pending             []string
	results             []result
	samples             []sample
	sampleErr           error
	checks              []check
	notes               []string
}

func (o *outcome) add(name string, ok bool, format string, a ...any) {
	o.checks = append(o.checks, check{name: name, ok: ok, detail: fmt.Sprintf(format, a...)})
}

func (o *outcome) note(format string, a ...any) { o.notes = append(o.notes, fmt.Sprintf(format, a...)) }

// passed: every assertion held, and there was at least one.
func (o *outcome) passed() bool {
	for _, c := range o.checks {
		if !c.ok {
			return false
		}
	}
	return len(o.checks) > 0
}

func (o *outcome) verdict() string {
	if o.passed() {
		return "pass"
	}
	failed := 0
	for _, c := range o.checks {
		if !c.ok {
			failed++
		}
	}
	return fmt.Sprintf("FAIL (%d of %d assertions)", failed, len(o.checks))
}

// scenario is one entry of the catalogue: the fault it injects, the
// behaviour it expects, and what of that behaviour is not built yet.
type scenario struct {
	name, fault, expect string
	pending             []string
	run                 func(context.Context, *runner, *outcome)
}

// catalogue lists every scenario in the order it runs. The instance is
// killed last, so nothing after it depends on how the restart went.
var catalogue = []scenario{
	{name: "soak", fault: "none", expect: "design load: every answer 2xx and within the class budget; resident memory grows at most 10 % after warm-up; no connection left busy", run: soak},
	{name: "spike", fault: "none", expect: "four times design load briefly: refusals carry Retry-After; back within budget after it", run: spike},
	{name: "stress", fault: "none", expect: "a ramp past the knee: throughput flat above it; excess refused 429 within 50 ms", run: stress},
	{name: "slow-storage", fault: "latency on every storage byte, beyond the deadline", expect: "503 deadline-exhausted by the deadline; storage idle within the cancel grace plus 100 ms", run: slowStorage},
	{name: "hanging-query", fault: "every feature table locked, so each read hangs in storage", expect: "cut at the deadline; afterwards no poisoned connection", run: hangingQuery},
	{name: "pool-exhausted", fault: "every permit held by hanging reads, plus bursts", expect: "the admission refuses, each refusal by the tier's queue_max_wait (RATE-011)", run: poolExhausted},
	{name: "composed-part-timeout", fault: "the parking tables locked", expect: "200 with the parking part timeout and the charging part ok", run: composedPart},
	{
		name: "storage-killed", fault: "the storage route of the fault proxy cut", expect: "503 upstream-unavailable with Retry-After within 50 ms; 2xx again after the repair", run: storageKilled,
		pending: []string{
			"the circuit breaker (NFR-RES-002) does not exist yet, so its opening and its half-open probes are not asserted",
			"no result cache exists yet, so no answer may carry served_stale (NFR-RES-004); stale answers are asserted once the cache is built",
		},
	},
	{
		name: "access-store-down", fault: "the access-store route of the fault proxy cut", expect: "a cached key keeps working; an uncached key is 503 upstream-unavailable; both work after the repair", run: accessStoreDown,
		pending: []string{"metering buffering up to metering.max_keys_buffered (NFR-RES-005) is covered by unit tests, not observed here"},
	},
	{
		name: "clock-skew", fault: "the source clock moved ahead of and behind the service clock", expect: "no negative age; ahead is fresh, behind is stale with data_stale", run: clockSkew,
		pending: []string{"the service's own clock cannot be moved inside its distroless container; quota windows under a moved clock (RATE-003) are covered by unit tests"},
	},
	{
		name: "instance-killed", fault: "the instance killed (SIGKILL) under load, then started", expect: "nothing past the deadline; ready within 30 s; 2xx again", run: instanceKilled,
		pending: []string{"counts lost to the kill (at most one metering interval, QAS-REL-05) are not read back"},
	},
}

func (r *runner) runAll(ctx context.Context, sel []scenario) []*outcome {
	out := make([]*outcome, 0, len(sel))
	for _, s := range sel {
		out = append(out, r.runScenario(ctx, s))
	}
	return out
}

// runScenario runs one scenario with the storage sampler beside it, puts
// the fault proxy back, and adds the assertions every scenario shares.
func (r *runner) runScenario(ctx context.Context, s scenario) *outcome {
	o := &outcome{name: s.name, fault: s.fault, expect: s.expect, pending: s.pending}
	var mu sync.Mutex
	stop := every(ctx, r.p.sample, func(ctx context.Context) {
		smp, err := r.backends(ctx)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			o.samples = append(o.samples, smp)
		case ctx.Err() == nil && o.sampleErr == nil:
			o.sampleErr = err
		}
	})
	s.run(ctx, r, o)
	stop()
	if err := r.reset(ctx); err != nil {
		o.add("fault proxy reset after the scenario", false, "%v", err)
	}
	r.shared(o)
	return o
}

// shared adds the assertions of every scenario: it sent requests, nothing
// was answered past the deadline plus 50 ms, the storage connections stayed
// within the pool, and no storage statement outlived its deadline.
func (r *runner) shared(o *outcome) {
	o.add("requests sent (a scenario that sent none fails)", len(o.results) > 0, "%d", len(o.results))
	worst := percentile(took(o.results, anyResult), 1)
	late := count(o.results, func(x result) bool { return x.took > r.limit })
	o.add(fmt.Sprintf("no request completes later than %s (NFR-PERF-001)", r.limit), late == 0, "%d late, slowest %s", late, worst.Round(time.Millisecond))
	conns, overdue := 0, 0
	for _, s := range o.samples {
		conns, overdue = max(conns, s.conns), max(overdue, s.overdue)
	}
	o.add("storage backends read throughout", o.sampleErr == nil && len(o.samples) > 0, "%d reads%s", len(o.samples), errSuffix(o.sampleErr))
	o.add(fmt.Sprintf("storage connections at most db.pool_max = %d (NFR-CAP-002)", r.poolMax), conns <= r.poolMax, "peak %d", conns)
	o.add(fmt.Sprintf("no storage statement older than %s: idle within the cancel grace plus 100 ms of a deadline (NFR-PERF-002)", r.overdue), overdue == 0, "peak %d", overdue)
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return ", " + err.Error()
}

// classBudgets asserts p95 and p99 of the 2xx answers of each class present
// in rs against its budget (NFR-PERF-003).
func (r *runner) classBudgets(o *outcome, rs []result) {
	for _, c := range requiredClasses {
		ds := took(rs, func(x result) bool { return x.class == c && ok(x) })
		if len(ds) == 0 {
			continue
		}
		b := r.budgets[c]
		p95, p99 := percentile(ds, 0.95), percentile(ds, 0.99)
		o.add(fmt.Sprintf("%s: p95 within %s and p99 within %s (NFR-PERF-003)", c, b.p95, b.p99), p95 <= b.p95 && p99 <= b.p99,
			"p95 %s, p99 %s over %d answers", p95.Round(time.Millisecond), p99.Round(time.Millisecond), len(ds))
	}
}

// recovered asserts that q is answered 2xx again within the restart budget
// and that design load afterwards is all 2xx.
func (r *runner) recovered(ctx context.Context, o *outcome, q request) {
	rs, up := r.awaitOK(ctx, q, r.p.ready)
	o.results = append(o.results, rs...)
	o.add(fmt.Sprintf("2xx again within %s of the repair", r.p.ready), up, "after %d tries", len(rs))
	if !up {
		return
	}
	tail := r.drive(ctx, r.p.rate, r.p.recover, r.mix)
	o.results = append(o.results, tail...)
	n := count(tail, ok)
	o.add("design load after the repair: all 2xx", n == len(tail), "%d of %d", n, len(tail))
}

func problemIs(slug string) func(result) bool {
	return func(r result) bool { return r.problem == slug }
}

// cutOrShed: an answer of a request whose storage read could not finish in
// time: 503 deadline-exhausted, or turned away by the admission.
func cutOrShed(r result) bool {
	return (r.status == http.StatusServiceUnavailable && r.problem == "deadline-exhausted" && r.retry) || (refused(r) && r.retry)
}

func soak(ctx context.Context, r *runner, o *outcome) {
	o.results = append(o.results, r.drive(ctx, r.p.rate, r.p.warm, r.mix)...)
	var (
		mu     sync.Mutex
		rss    []int64
		rssErr error
	)
	stop := every(ctx, r.p.sample, func(ctx context.Context) {
		v, err := r.rss(ctx)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			rss = append(rss, v)
		case ctx.Err() == nil && rssErr == nil:
			rssErr = err
		}
	})
	steady := r.drive(ctx, r.p.rate, r.p.soak, r.mix)
	stop()
	o.results = append(o.results, steady...)
	n := count(steady, ok)
	o.add("every answer at design load is 2xx", n == len(steady), "%d of %d", n, len(steady))
	r.classBudgets(o, steady)
	if third := len(rss) / 3; rssErr == nil && third > 0 {
		first, last := median(rss[:third]), median(rss[len(rss)-third:])
		growth := float64(last-first) / float64(first)
		o.add("resident memory grows at most 10 % after warm-up (TST-007)", growth <= 0.10,
			"%.1f MiB to %.1f MiB (%+.1f %%), median of the first and the last third of %d reads", mib(first), mib(last), growth*100, len(rss))
	} else {
		o.add("resident memory read throughout", false, "%d reads%s", len(rss), errSuffix(rssErr))
	}
	_ = sleep(ctx, r.p.sample)
	busy, err := r.busy(ctx)
	o.add("no storage connection left busy after the load (no leak)", err == nil && busy == 0, "%d busy%s", busy, errSuffix(err))
}

func median(v []int64) int64 {
	s := slices.Clone(v)
	slices.Sort(s)
	return s[len(s)/2]
}

func mib(b int64) float64 { return float64(b) / (1 << 20) }

func spike(ctx context.Context, r *runner, o *outcome) {
	o.results = append(o.results, r.drive(ctx, r.p.rate, r.p.spikeBase, r.mix)...)
	sp := r.drive(ctx, 4*r.p.rate, r.p.spikeBurst, r.mix)
	rec := r.drive(ctx, r.p.rate, r.p.recover, r.mix)
	o.results = append(append(o.results, sp...), rec...)
	shed := count(sp, refused)
	o.add("every refusal in the spike carries Retry-After", shed == count(sp, func(x result) bool { return refused(x) && x.retry }), "%d refused of %d", shed, len(sp))
	tail := rec[len(rec)/2:]
	n := count(tail, ok)
	o.add("the second half of design load after the spike: all 2xx", n == len(tail), "%d of %d", n, len(tail))
	r.classBudgets(o, tail)
}

func stress(ctx context.Context, r *runner, o *outcome) {
	knee, peak, above := -1, 0.0, 0.0
	for rate, i := r.p.rate, 0; rate <= r.p.maxRate; rate, i = rate*2, i+1 {
		rs := r.drive(ctx, rate, r.p.step, r.mix)
		o.results = append(o.results, rs...)
		served := float64(count(rs, ok)) / r.p.step.Seconds()
		shed := count(rs, refused)
		o.note("%.0f/s offered: %.0f/s answered 2xx, %d of %d refused", rate, served, shed, len(rs))
		peak = max(peak, served)
		if knee < 0 && float64(shed) >= 0.01*float64(len(rs)) {
			knee, above = i, served
		}
		if knee >= 0 {
			above = min(above, served)
			if i > knee {
				break // the knee plus one step of margin
			}
		}
	}
	o.add(fmt.Sprintf("the knee is reached below %.0f/s", r.p.maxRate), knee >= 0, "first step refusing 1 %% or more: %d", knee)
	if knee < 0 {
		return
	}
	o.add("throughput stays flat above the knee: at least 80 % of the peak (NFR-CAP-003)", above >= 0.8*peak, "%.0f/s against a peak of %.0f/s", above, peak)
	shed := took(o.results, refused)
	p95 := percentile(shed, 0.95)
	o.add("excess refused within 50 ms, p95 (NFR-CAP-003)", p95 <= r.fast, "p95 %s over %d refusals", p95.Round(time.Millisecond), len(shed))
	n := count(o.results, func(x result) bool { return refused(x) && x.status == http.StatusTooManyRequests && x.retry })
	o.add("every refusal is 429 with Retry-After", n == len(shed), "%d of %d", n, len(shed))
}

func slowStorage(ctx context.Context, r *runner, o *outcome) {
	if err := r.addLatency(ctx, proxyStorage, r.limit+500*time.Millisecond); err != nil {
		o.add("latency injected", false, "%v", err)
		return
	}
	rs := r.drive(ctx, r.p.faultRate, r.p.fault, func(int) request { return r.charging() })
	o.results = append(o.results, rs...)
	cut := count(rs, problemIs("deadline-exhausted"))
	o.add("slowed storage is answered 503 deadline-exhausted, or shed, with Retry-After", count(rs, cutOrShed) == len(rs) && cut > 0,
		"%d deadline-exhausted, %d shed, %d answers", cut, count(rs, refused), len(rs))
	if err := r.reset(ctx); err != nil {
		o.add("latency removed", false, "%v", err)
		return
	}
	r.recovered(ctx, o, r.charging())
}

func hangingQuery(ctx context.Context, r *runner, o *outcome) {
	wait, err := r.hold(ctx, featureTables, r.p.fault+r.limit)
	if err != nil {
		o.add("feature tables locked", false, "%v", err)
		_ = wait()
		return
	}
	rs := r.drive(ctx, r.p.faultRate, r.p.fault, func(int) request { return r.charging() })
	o.results = append(o.results, rs...)
	cut := count(rs, problemIs("deadline-exhausted"))
	o.add("a hanging read is cut at the deadline: 503 deadline-exhausted, or shed, with Retry-After", count(rs, cutOrShed) == len(rs) && cut > 0,
		"%d deadline-exhausted, %d shed, %d answers", cut, count(rs, refused), len(rs))
	err = wait()
	o.add("lock released", err == nil, "%v", err)
	after := r.drive(ctx, r.p.rate, time.Duration(float64(r.p.after)/r.p.rate*float64(time.Second)), r.mix)
	o.results = append(o.results, after...)
	n := count(after, ok)
	o.add(fmt.Sprintf("%d requests after it, all 2xx: no poisoned connection (NFR-RES-007)", len(after)), n == len(after), "%d of %d", n, len(after))
}

func poolExhausted(ctx context.Context, r *runner, o *outcome) {
	wait, err := r.hold(ctx, featureTables, r.p.fault+r.limit)
	if err != nil {
		o.add("feature tables locked", false, "%v", err)
		_ = wait()
		return
	}
	var rs []result
	for end := time.Now().Add(r.p.fault); time.Now().Before(end); {
		rs = append(rs, r.burst(ctx, r.p.burst, r.charging())...)
	}
	o.results = append(o.results, rs...)
	shed := took(rs, refused)
	o.add("the admission refuses while every permit is held (RATE-011)", len(shed) > 0, "%d of %d refused", len(shed), len(rs))
	p95 := percentile(shed, 0.95)
	// A request is refused at once when its estimated wait does not fit,
	// else it waits in its tier's queue at most queue_max_wait
	// (docs/rate-limits.md, gate 4): no refusal may come later than that.
	o.add(fmt.Sprintf("each refusal within the tier's queue_max_wait plus 50 ms: p95 within %s (RATE-011)", r.queued+r.fast), len(shed) > 0 && p95 <= r.queued+r.fast, "p95 %s", p95.Round(time.Millisecond))
	err = wait()
	o.add("lock released", err == nil, "%v", err)
	r.recovered(ctx, o, r.charging())
}

func composedPart(ctx context.Context, r *runner, o *outcome) {
	wait, err := r.hold(ctx, parkingTables, r.p.fault+r.limit)
	if err != nil {
		o.add("parking tables locked", false, "%v", err)
		_ = wait()
		return
	}
	q := r.overview()
	q.keep = true
	rs := r.drive(ctx, 2, r.p.fault, func(int) request { return q })
	o.results = append(o.results, rs...)
	want := map[string]string{"charging": "ok", "parking": "timeout"}
	n := count(rs, func(x result) bool { return x.status == http.StatusOK && maps.Equal(parts(x.body), want) })
	o.add("200 with the parking part timeout and the charging part ok (NFR-RES-003)", n == len(rs), "%d of %d", n, len(rs))
	err = wait()
	o.add("lock released", err == nil, "%v", err)
	r.recovered(ctx, o, r.overview())
}

// parts reads meta.parts of a composed answer as part → status.
func parts(body map[string]any) map[string]string {
	meta, _ := body["meta"].(map[string]any)
	list, _ := meta["parts"].([]any)
	out := map[string]string{}
	for _, p := range list {
		m, _ := p.(map[string]any)
		part, _ := m["part"].(string)
		status, _ := m["status"].(string)
		out[part] = status
	}
	return out
}

func storageKilled(ctx context.Context, r *runner, o *outcome) {
	if err := r.setProxy(ctx, proxyStorage, false); err != nil {
		o.add("storage route cut", false, "%v", err)
		return
	}
	rs := r.drive(ctx, r.p.faultRate, r.p.fault, func(int) request { return r.charging() })
	o.results = append(o.results, rs...)
	lost := took(rs, func(x result) bool {
		return x.status == http.StatusServiceUnavailable && x.retry && x.problem == "upstream-unavailable"
	})
	o.add("lost storage is answered 503 upstream-unavailable with Retry-After", len(lost) == len(rs), "%d of %d", len(lost), len(rs))
	p95 := percentile(lost, 0.95)
	o.add("each 503 within 50 ms, p95 (QAS-REL-02)", len(lost) > 0 && p95 <= r.fast, "p95 %s", p95.Round(time.Millisecond))
	stale := count(rs, func(x result) bool { return x.problem == "served_stale" })
	o.add("no answer claims served_stale while no cache exists (NFR-RES-004)", stale == 0, "%d", stale)
	if err := r.setProxy(ctx, proxyStorage, true); err != nil {
		o.add("storage route restored", false, "%v", err)
		return
	}
	r.recovered(ctx, o, r.charging())
}

func accessStoreDown(ctx context.Context, r *runner, o *outcome) {
	warm, up := r.awaitOK(ctx, r.charging(), r.p.ready)
	o.results = append(o.results, warm...)
	if !up {
		o.add("the warm key answered before the fault", false, "after %d tries", len(warm))
		return
	}
	if err := r.setProxy(ctx, proxyAccess, false); err != nil {
		o.add("access-store route cut", false, "%v", err)
		return
	}
	rs := r.drive(ctx, r.p.faultRate, r.p.fault, func(int) request { return r.charging() })
	coldQ := r.charging()
	coldQ.cold = true
	cold := r.do(ctx, coldQ)
	o.results = append(append(o.results, rs...), cold)
	n := count(rs, ok)
	o.add("a key in the key cache keeps working while the access store is down (NFR-RES-005)", n == len(rs), "%d of %d", n, len(rs))
	o.add("a key not in the cache is answered 503 upstream-unavailable", cold.status == http.StatusServiceUnavailable && cold.problem == "upstream-unavailable", "%d %s", cold.status, cold.problem)
	if err := r.setProxy(ctx, proxyAccess, true); err != nil {
		o.add("access-store route restored", false, "%v", err)
		return
	}
	again, up := r.awaitOK(ctx, coldQ, r.p.ready)
	o.results = append(o.results, again...)
	o.add(fmt.Sprintf("the uncached key works within %s of the repair", r.p.ready), up, "after %d tries", len(again))
}

func instanceKilled(ctx context.Context, r *runner, o *outcome) {
	done := make(chan []result, 1)
	go func() { done <- r.drive(ctx, r.p.rate, r.p.fault, r.mix) }()
	_ = sleep(ctx, r.p.fault/3)
	_, kerr := r.docker(ctx, "kill", "-s", "SIGKILL", "api")
	o.add("instance killed under load", kerr == nil, "%v", kerr)
	_, serr := r.docker(ctx, "start", "api")
	o.add("instance started", serr == nil, "%v", serr)
	after, ready := r.awaitReady(ctx, r.p.ready)
	o.add(fmt.Sprintf("ready within %s of the start (QAS-REL-05)", r.p.ready), ready, "after %s", after.Round(time.Millisecond))
	rs := <-done
	o.results = append(o.results, rs...)
	o.note("%d of %d requests met a closed or refused connection while the instance was down", count(rs, func(x result) bool { return x.status == 0 }), len(rs))
	if ready {
		r.recovered(ctx, o, r.charging())
	}
}

// The clock-skew statements: the freshness instants are saved, moved and
// put back, all as the owner of the throwaway database.
const (
	skewSave    = "drop table if exists loadrun_freshness; create table loadrun_freshness as select * from contract.source_freshness"
	skewRestore = "update contract.source_freshness f set data_as_of = s.data_as_of from loadrun_freshness s where s.source_ref = f.source_ref; drop table loadrun_freshness"
)

func clockSkew(ctx context.Context, r *runner, o *outcome) {
	if _, err := r.psql(ctx, skewSave); err != nil {
		o.add("source clock saved", false, "%v", err)
		return
	}
	q := r.charging()
	q.keep = true
	for _, c := range []struct {
		name, at string
		want     func(freshnessView) bool
	}{
		{"source clock 10 minutes ahead: age 0 and fresh", "now() + interval '10 minutes'", func(f freshnessView) bool { return f.n > 0 && f.zeroAge == f.n && f.stale == 0 }},
		{"source clock 30 days behind: stale, with the data_stale warning", "now() - interval '30 days'", func(f freshnessView) bool { return f.n > 0 && f.stale == f.n && f.staleWarning }},
	} {
		if _, err := r.psql(ctx, "update contract.source_freshness set data_as_of = "+c.at); err != nil {
			o.add(c.name, false, "%v", err)
			continue
		}
		rs, seen := r.awaitWhere(ctx, q, r.p.ready, func(x result) bool { return x.status == http.StatusOK && c.want(freshnessOf(x.body)) })
		o.results = append(o.results, rs...)
		o.add(c.name, seen, "after %d answers", len(rs))
	}
	_, err := r.psql(ctx, skewRestore)
	o.add("source clock restored", err == nil, "%v", err)
	neg := count(o.results, func(x result) bool { return freshnessOf(x.body).negative > 0 })
	o.add("no negative age in any answer", neg == 0, "%d", neg)
}

// freshnessView summarises meta.freshness and meta.warnings of an answer.
type freshnessView struct {
	n, zeroAge, stale, negative int
	staleWarning                bool
}

func freshnessOf(body map[string]any) freshnessView {
	var v freshnessView
	meta, _ := body["meta"].(map[string]any)
	list, _ := meta["freshness"].([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		v.n++
		if age, isNum := m["ageS"].(float64); isNum {
			if age == 0 {
				v.zeroAge++
			}
			if age < 0 {
				v.negative++
			}
		}
		if s, _ := m["stale"].(bool); s {
			v.stale++
		}
	}
	warnings, _ := meta["warnings"].([]any)
	for _, w := range warnings {
		m, _ := w.(map[string]any)
		v.staleWarning = v.staleWarning || m["code"] == "data_stale"
	}
	return v
}
