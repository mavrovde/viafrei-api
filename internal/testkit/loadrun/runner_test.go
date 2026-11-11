package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestDoMeasuresAndReadsProblems(t *testing.T) {
	req.Covers(t, "TST-007")
	var gotKey string
	w := newWorld(t, func(rw http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Authorization")
		if strings.Contains(r.URL.Path, "parking") {
			problem(rw, http.StatusServiceUnavailable, "deadline-exhausted")
			return
		}
		answer(rw, map[string]any{"x": 1.0})
	}, healthyDocker)
	ctx := context.Background()

	q := w.r.charging()
	q.keep = true
	q.cold = true
	res := w.r.do(ctx, q)
	if res.status != 200 || res.class != classNear || res.body["meta"] == nil || gotKey != "Bearer vf_test_cold" || res.took <= 0 {
		t.Errorf("charging: %+v, key %q", res, gotKey)
	}
	res = w.r.do(ctx, w.r.parking())
	if res.status != 503 || res.problem != "deadline-exhausted" || !res.retry || res.body != nil || gotKey != "Bearer vf_test_warm" {
		t.Errorf("parking: %+v, key %q", res, gotKey)
	}
	if q := w.r.lookup(); q.class != classLookup || q.path != "/v1/key" {
		t.Errorf("lookup %+v", q)
	}
	if !strings.Contains(w.r.overview().path, "/v1/places/pl-1/overview") {
		t.Errorf("overview %q", w.r.overview().path)
	}

	// A transport error and an unusable URL are status 0, never a pass.
	w.api.Close()
	if res := w.r.do(ctx, w.r.charging()); res.status != 0 {
		t.Errorf("closed server: %+v", res)
	}
	w.r.api = "http://[::1"
	if res := w.r.do(ctx, w.r.charging()); res.status != 0 || res.took != 0 {
		t.Errorf("bad URL: %+v", res)
	}
}

func TestMixIsTwoNearSearchesPerComposedAnswer(t *testing.T) {
	w := newWorld(t, healthyAPI, healthyDocker)
	n := map[string]int{}
	for i := range 10 {
		n[w.r.mix(i).class]++
	}
	if n[classNear] != 8 || n[classComposed] != 2 {
		t.Errorf("mix %v", n)
	}
}

func TestDriveIsOpenLoopAndStopsWithItsContext(t *testing.T) {
	req.Covers(t, "TST-007")
	w := newWorld(t, healthyAPI, healthyDocker)
	rs := w.r.drive(context.Background(), 500, 20*time.Millisecond, w.r.mix)
	if len(rs) != 10 || count(rs, ok) != 10 {
		t.Errorf("drive sent %d, %d ok", len(rs), count(rs, ok))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rs := w.r.drive(ctx, 10, time.Second, w.r.mix); len(rs) != 0 {
		t.Errorf("a cancelled drive sent %d requests, want none", len(rs))
	}
	if rs := w.r.burst(context.Background(), 7, w.r.charging()); len(rs) != 7 || count(rs, ok) != 7 {
		t.Errorf("burst %d", len(rs))
	}
}

func TestSleepAndEvery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	if sleep(ctx, 0) != nil || sleep(ctx, time.Millisecond) != nil {
		t.Error("a sleep with a live context failed")
	}
	cancel()
	if sleep(ctx, 0) == nil || sleep(ctx, time.Hour) == nil {
		t.Error("a sleep with an ended context succeeded")
	}
	n := 0
	stop := every(context.Background(), time.Millisecond, func(context.Context) { n++ })
	time.Sleep(10 * time.Millisecond)
	stop()
	if n < 2 {
		t.Errorf("every ran %d times", n)
	}
}

func TestDatabaseReads(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, healthyAPI, healthyDocker)
	if s, err := w.r.backends(ctx); err != nil || s != (sample{conns: 2}) {
		t.Errorf("backends %+v %v", s, err)
	}
	if !w.dock.called("exec -T db psql -U owner -d contract") || !w.dock.called("interval '220 milliseconds'") {
		t.Errorf("calls %q", w.dock.calls)
	}
	if n, err := w.r.busy(ctx); err != nil || n != 0 {
		t.Errorf("busy %d %v", n, err)
	}
	if b, err := w.r.rss(ctx); err != nil || b != 3072*1024 {
		t.Errorf("rss %d %v", b, err)
	}

	for name, answer := range map[string]func(string) (string, error){
		"docker fails": func(string) (string, error) { return "", errFake },
		"garbage":      func(string) (string, error) { return "RSS\nmany\n", nil },
	} {
		w.dock.answer = answer
		if _, err := w.r.backends(ctx); err == nil {
			t.Errorf("%s: backends passed", name)
		}
		if _, err := w.r.busy(ctx); err == nil {
			t.Errorf("%s: busy passed", name)
		}
		if _, err := w.r.rss(ctx); err == nil {
			t.Errorf("%s: rss passed", name)
		}
	}
	for name, top := range map[string]func(string) (string, error){
		"no container": func(a string) (string, error) { return "\n", nil },
		"top fails": func(a string) (string, error) {
			if a == "ps -q api" {
				return "c1", nil
			}
			return "", errFake
		},
		"no process": func(a string) (string, error) {
			if a == "ps -q api" {
				return "c1", nil
			}
			return "RSS\n", nil
		},
	} {
		w.dock.answer = top
		if _, err := w.r.rss(ctx); err == nil {
			t.Errorf("%s: rss passed", name)
		}
	}
}

func TestFaultProxyCalls(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, healthyAPI, healthyDocker)
	if err := w.r.setProxy(ctx, proxyStorage, false); err != nil {
		t.Fatal(err)
	}
	if err := w.r.addLatency(ctx, proxyAccess, 2500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := w.r.reset(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /proxies/storage {"enabled":false}`,
		`POST /proxies/access/toxics {"attributes":{"latency":2500},"name":"latency","stream":"downstream","type":"latency"}`,
		"POST /reset ",
	}
	if strings.Join(w.proxy.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls\n%s", strings.Join(w.proxy.calls, "\n"))
	}
	w.proxy.fail = func(string, string, string) bool { return true }
	if err := w.r.reset(ctx); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a refused call: %v", err)
	}
	if err := w.r.toxi(ctx, http.MethodPost, "/x", make(chan int)); err == nil {
		t.Error("an unencodable body passed")
	}
	w.r.proxy = "http://[::1"
	if err := w.r.reset(ctx); err == nil {
		t.Error("a bad proxy URL passed")
	}
	w.r.proxy = w.api.URL
	w.api.Close()
	if err := w.r.reset(ctx); err == nil {
		t.Error("an unreachable proxy passed")
	}
}

func TestHoldWaitsForTheLock(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, healthyAPI, healthyDocker)
	wait, err := w.r.hold(ctx, featureTables, time.Second)
	if err != nil || wait() != nil {
		t.Fatalf("hold %v", err)
	}
	if !w.dock.called("lock table contract.place, contract.charging_site") || !w.dock.called("pg_sleep(1.000)") || !w.dock.called("'contract.place'::regclass") {
		t.Errorf("calls %q", w.dock.calls)
	}

	// The session fails before its lock is seen.
	w.dock.answer = func(a string) (string, error) {
		if strings.Contains(a, "begin;") {
			return "", errFake
		}
		return "0", nil
	}
	if wait, err := w.r.hold(ctx, parkingTables, time.Second); err == nil || wait() == nil {
		t.Errorf("a failed session: %v", err)
	}
	// The lock is never granted within the hold.
	w.dock.answer = func(a string) (string, error) {
		if strings.Contains(a, "begin;") {
			time.Sleep(50 * time.Millisecond)
		}
		return "0", nil
	}
	if wait, err := w.r.hold(ctx, parkingTables, 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), "not granted") || wait() != nil {
		t.Errorf("a lock never granted: %v", err)
	}
	// The context ends while it waits.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := w.r.hold(cctx, parkingTables, time.Second); err == nil {
		t.Error("an ended context passed")
	}
}

func TestAwaiting(t *testing.T) {
	ctx := context.Background()
	up := false
	w := newWorld(t, func(rw http.ResponseWriter, r *http.Request) {
		if !up {
			problem(rw, http.StatusServiceUnavailable, "upstream-unavailable")
			return
		}
		answer(rw, map[string]any{})
	}, healthyDocker)
	if rs, got := w.r.awaitOK(ctx, w.r.charging(), 200*time.Millisecond); got || len(rs) < 2 {
		t.Errorf("down: %v after %d", got, len(rs))
	}
	if _, got := w.r.awaitReady(ctx, 10*time.Millisecond); got {
		t.Error("ready while down")
	}
	up = true
	if rs, got := w.r.awaitOK(ctx, w.r.charging(), time.Second); !got || len(rs) != 1 {
		t.Errorf("up: %v after %d", got, len(rs))
	}
	if _, got := w.r.awaitReady(ctx, time.Second); !got {
		t.Error("not ready while up")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	up = false
	if _, got := w.r.awaitOK(cctx, w.r.charging(), time.Second); got {
		t.Error("an ended context passed awaitOK")
	}
	if _, got := w.r.awaitReady(cctx, time.Second); got {
		t.Error("an ended context passed awaitReady")
	}
	w.r.readyURL = "http://[::1"
	if _, got := w.r.awaitReady(ctx, time.Second); got {
		t.Error("a bad readiness URL passed")
	}
}

func TestPercentile(t *testing.T) {
	if percentile(nil, 0.95) != 0 {
		t.Error("percentile of nothing")
	}
	ds := []time.Duration{5, 1, 4, 2, 3, 6, 7, 8, 9, 10}
	if percentile(ds, 0.5) != 5 || percentile(ds, 0.95) != 10 || percentile(ds, 1) != 10 || percentile(ds, 0) != 1 {
		t.Errorf("percentiles %v %v %v", percentile(ds, 0.5), percentile(ds, 0.95), percentile(ds, 1))
	}
}

func TestExecDocker(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nif [ \"$8\" = fail ]; then echo broken >&2; exit 3; fi\necho \"$*\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	run := execDocker(bin, "proj", []string{"a.yaml", "b.yaml"})
	out, err := run(context.Background(), "ps", "-q", "api")
	if err != nil || strings.TrimSpace(out) != "compose -p proj -f a.yaml -f b.yaml ps -q api" {
		t.Errorf("%q %v", out, err)
	}
	if _, err := run(context.Background(), "fail"); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Errorf("a failing docker: %v", err)
	}
	// docker compose top takes no ps options, so docker top is the plain CLI.
	if out, err := execHost(bin)(context.Background(), "top", "c1", "-o", "rss"); err != nil || strings.TrimSpace(out) != "top c1 -o rss" {
		t.Errorf("plain docker: %q %v", out, err)
	}
}
