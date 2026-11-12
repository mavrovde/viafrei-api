// Command loadrun runs the soak, stress, spike and fault-injection scenarios
// against the test stack (TST-007, NFR-PERF-001, NFR-PERF-002, NFR-CAP-001
// to NFR-CAP-003, NFR-RES-002 to NFR-RES-007; issue #19) and writes a
// Markdown report with every scenario, its assertions and its counts.
//
//	go run ./internal/testkit/loadrun -profile nightly -place <placeId> \
//	    -report load-report.md
//
// scripts/load-test.sh starts the stack (compose.yaml plus
// test/load/compose.yaml: a fault proxy between the service and both of its
// stores), issues two throwaway keys and runs this command; nothing else
// should call it.
//
// The runner only speaks to hosts on the allow-list (-allow, one host per
// line): the service and the fault proxy of the stack it was started for,
// never anything else. A URL whose host is not listed is refused before a
// single request is sent (exit 2). Faults go through the fault proxy's API
// and through docker compose on that stack's project; the database is read
// through docker compose exec, never over the network.
//
// The keys come from LOADRUN_KEY (used throughout) and LOADRUN_COLD_KEY
// (used only while the access store is down, so it is not in the key
// cache); neither is ever printed.
//
// Every scenario holds every answer to the deadline plus 50 ms, every
// storage connection count to -pool-max and every storage query to the
// deadline plus the cancel grace plus 100 ms. A scenario that sent no
// request, could not read the database or could not inject its fault fails.
//
// Exit 0 when every assertion held, 1 when one did not, 2 on a usage error,
// a refused target or an unreadable budget file.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

const prog = "loadrun"

// profile is one budget: pr keeps the pull-request run to a few minutes,
// nightly is the long run of the Performance Test Plan.
type profile struct {
	rate       float64       // design load, requests a second (NFR-CAP-001)
	warm       time.Duration // warm-up before the soak's measurements
	soak       time.Duration // the soak itself (TST-007)
	step       time.Duration // one step of the stress ramp
	maxRate    float64       // the stress ramp's ceiling
	spikeBase  time.Duration // design load before the spike
	spikeBurst time.Duration // four times design load
	recover    time.Duration // design load after a fault or a spike
	fault      time.Duration // how long a fault is held
	faultRate  float64       // requests a second while a fault is held
	after      int           // requests after a hanging query (QAS-REL-01)
	burst      int           // concurrent requests while the pool is held
	sample     time.Duration // how often memory and connections are read
	ready      time.Duration // restart budget of a killed instance (QAS-REL-05)
	settle     time.Duration // poll interval while waiting for a state
}

var profiles = map[string]profile{
	"pr": {
		rate: 20, warm: 5 * time.Second, soak: 30 * time.Second,
		step: 4 * time.Second, maxRate: 1280,
		spikeBase: 4 * time.Second, spikeBurst: 4 * time.Second, recover: 6 * time.Second,
		fault: 6 * time.Second, faultRate: 5, after: 100, burst: 40,
		sample: time.Second, ready: 30 * time.Second, settle: 100 * time.Millisecond,
	},
	"nightly": {
		rate: 50, warm: 2 * time.Minute, soak: time.Hour,
		step: 30 * time.Second, maxRate: 3200,
		spikeBase: 30 * time.Second, spikeBurst: 10 * time.Second, recover: time.Minute,
		fault: time.Minute, faultRate: 10, after: 1000, burst: 80,
		sample: 5 * time.Second, ready: 30 * time.Second, settle: 100 * time.Millisecond,
	},
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(prog, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		api      = fs.String("api", "http://[::1]:8080", "base URL of the service under test")
		proxy    = fs.String("proxy", "http://[::1]:8474", "base URL of the fault proxy's API")
		allow    = fs.String("allow", "test/load/allow-list.txt", "the hosts the runner may reach, one per line")
		budgets  = fs.String("budgets", "test/load/budgets.tsv", "latency budget per endpoint class (NFR-PERF-003)")
		prof     = fs.String("profile", "", "pr or nightly")
		only     = fs.String("scenarios", "", "comma-separated scenario names; empty runs every scenario")
		report   = fs.String("report", "load-report.md", "the Markdown report to write; the figures go beside it, .json for .md (TST-021)")
		previous = fs.String("previous", "", "the previous main run's figures to compare with; empty compares with nothing, and the report says so (TST-021)")
		tol      = fs.Float64("regression-tolerance", 0.10, "the growth of a class's p95 or p99 over the previous run that fails it, a fraction (TST-021)")
		floor    = fs.Duration("regression-floor", 5*time.Millisecond, "a p95 or p99 growth no larger than this is never a regression (TST-021)")
		place    = fs.String("place", "", "a place id of the fixture set, for the composed answer")
		docker   = fs.String("docker", "docker", "the docker CLI")
		project  = fs.String("project", "viafrei-api", "the compose project of the test stack")
		files    = fs.String("compose-files", "compose.yaml,test/load/compose.yaml", "the compose files of the test stack, comma-separated")
		poolMax  = fs.Int("pool-max", 4, "db.pool_max of the instance under test (NFR-CAP-002)")
		deadline = fs.Duration("deadline", 2*time.Second, "deadline.request of the instance under test (NFR-PERF-001)")
		grace    = fs.Duration("cancel-grace", 100*time.Millisecond, "db.cancel_grace of the instance under test (NFR-PERF-002)")
		queued   = fs.Duration("queue-max-wait", 400*time.Millisecond, "tiers.standard.queue_max_wait of the instance under test (RATE-011)")
		failures = fs.Int("breaker-failures", 5, "breaker.failure_threshold of the instance under test (NFR-RES-002)")
		openFor  = fs.Duration("breaker-open-for", 2*time.Second, "breaker.open_for of the instance under test, as test/load/compose.yaml sets it (NFR-RES-002)")
		probes   = fs.Int("breaker-probes", 2, "breaker.half_open_probes of the instance under test (NFR-RES-002)")
		cached   = fs.String("cached-api", "http://[::1]:8081", "base URL of the instance with the result cache, behind the same fault proxy (NFR-RES-004)")
		staleFor = fs.Duration("stale-if-error", 3*time.Second, "cache.stale_if_error of the instance with the result cache, as test/load/compose.yaml sets it (NFR-RES-004)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, prog+": "+format+"\n", a...)
		return 2
	}
	p, ok := profiles[*prof]
	switch {
	case !ok:
		return fail("-profile must be pr or nightly")
	case fs.NArg() != 0:
		return fail("unexpected arguments %q", fs.Args())
	case *place == "":
		return fail("-place is required")
	case *poolMax < 1:
		return fail("-pool-max must be at least 1")
	case !(*tol > 0 && *tol <= 10):
		return fail("-regression-tolerance must be above 0 and at most 10")
	case *floor < 0:
		return fail("-regression-floor must not be negative")
	case !strings.HasSuffix(*report, ".md"):
		return fail("-report must end in .md, so that the figures can go beside it")
	case *failures < 1 || *probes < 1 || *openFor <= 0:
		return fail("-breaker-failures and -breaker-probes must be at least 1, -breaker-open-for positive")
	case *staleFor <= 0:
		return fail("-stale-if-error must be positive")
	}
	key, cold := getenv("LOADRUN_KEY"), getenv("LOADRUN_COLD_KEY")
	if key == "" || cold == "" || key == cold {
		return fail("LOADRUN_KEY and LOADRUN_COLD_KEY must be set to two different keys")
	}
	hosts, err := readAllowList(*allow)
	if err != nil {
		return fail("%v", err)
	}
	for _, u := range []string{*api, *proxy, *cached} {
		if err := guard(u, hosts); err != nil {
			return fail("%v", err)
		}
	}
	b, err := readBudgets(*budgets)
	if err != nil {
		return fail("%v", err)
	}
	sel, err := selectScenarios(*only)
	if err != nil {
		return fail("%v", err)
	}
	r := newRunner(p, b, strings.TrimRight(*api, "/"), strings.TrimRight(*proxy, "/"), key, cold, *place, *poolMax, *deadline, *grace)
	r.docker = execDocker(*docker, *project, strings.Split(*files, ","))
	r.host = execHost(*docker)
	r.queued = *queued
	r.brk = breakerSettings{failures: *failures, openFor: *openFor, probes: *probes}
	r.cached, r.staleFor = strings.TrimRight(*cached, "/"), *staleFor
	outs := r.runAll(context.Background(), sel)
	figs := collect(*prof, outs)
	cmp := compare(*previous, figs, *tol, *floor)
	text := renderReport(*prof, outs) + cmp.render(*tol, *floor)
	if err := os.WriteFile(*report, []byte(text), 0o600); err != nil {
		return fail("%v", err)
	}
	if err := writeFigures(figuresPath(*report), figs); err != nil {
		return fail("%v", err)
	}
	failed := 0
	for _, o := range outs {
		if !o.passed() {
			failed++
		}
		fmt.Fprintf(stdout, "%s: %s: %d requests, %s\n", prog, o.name, len(o.results), o.verdict())
	}
	for _, r := range cmp.regressions {
		fmt.Fprintf(stdout, "%s: regression: %s\n", prog, r)
	}
	if cmp.state != compared {
		fmt.Fprintf(stdout, "%s: not compared with the previous main run: %s\n", prog, cmp.reason)
	}
	fmt.Fprintf(stdout, "%s: %d scenario(s), %d failed; report in %s, figures in %s\n", prog, len(outs), failed, *report, figuresPath(*report))
	if failed > 0 || cmp.failed() {
		return 1
	}
	return 0
}

// readAllowList reads the hosts the runner may reach. An empty list allows
// nothing and is refused rather than read as "no restriction".
func readAllowList(path string) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the path is the operator's own flag
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var hosts []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			hosts = append(hosts, strings.ToLower(line))
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%s allows no host; a run against nothing named is refused", path)
	}
	return hosts, nil
}

// guard refuses a URL that is not plain http to a host on the allow-list:
// the test stack is reached over the loopback, never over TLS to a real
// deployment.
func guard(raw string, hosts []string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil {
		return fmt.Errorf("refused target %q: an http URL of the test stack is required", raw)
	}
	if !slices.Contains(hosts, strings.ToLower(u.Hostname())) {
		return fmt.Errorf("refused target %q: host %q is not on the test-environment allow-list", raw, u.Hostname())
	}
	return nil
}

// budget is one endpoint class of NFR-PERF-003.
type budget struct{ p50, p95, p99 time.Duration }

// requiredClasses are the classes the request mix uses; the budget file
// must name each of them.
var requiredClasses = []string{classLookup, classNear, classComposed}

// readBudgets reads test/load/budgets.tsv: class, p50, p95 and p99 in
// milliseconds, tab-separated, # comments allowed.
func readBudgets(path string) (map[string]budget, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator's own flag
	if err != nil {
		return nil, err
	}
	out := map[string]budget{}
	for n, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("%s:%d: want class, p50, p95, p99", path, n+1)
		}
		var ms [3]time.Duration
		for i, s := range f[1:] {
			v, err := strconv.Atoi(s)
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("%s:%d: %q is not a positive number of milliseconds", path, n+1, s)
			}
			ms[i] = time.Duration(v) * time.Millisecond
		}
		out[f[0]] = budget{ms[0], ms[1], ms[2]}
	}
	for _, c := range requiredClasses {
		if _, ok := out[c]; !ok {
			return nil, fmt.Errorf("%s has no budget for class %q", path, c)
		}
	}
	return out, nil
}

// selectScenarios returns the scenarios named, in their catalogue order;
// an unknown name is refused rather than skipped.
func selectScenarios(only string) ([]scenario, error) {
	if only == "" {
		return catalogue, nil
	}
	want := strings.Split(only, ",")
	var out []scenario
	for _, s := range catalogue {
		if slices.Contains(want, s.name) {
			out = append(out, s)
		}
	}
	for _, w := range want {
		if !slices.ContainsFunc(catalogue, func(s scenario) bool { return s.name == w }) {
			return nil, fmt.Errorf("unknown scenario %q", w)
		}
	}
	return out, nil
}

// execDocker runs the docker CLI on the test stack's compose project.
func execDocker(bin, project string, files []string) func(context.Context, ...string) (string, error) {
	base := []string{"compose", "-p", project}
	for _, f := range files {
		base = append(base, "-f", f)
	}
	return execWith(bin, base)
}

// execHost runs the plain docker CLI, for what compose does not offer.
func execHost(bin string) func(context.Context, ...string) (string, error) {
	return execWith(bin, nil)
}

func execWith(bin string, base []string) func(context.Context, ...string) (string, error) {
	return func(ctx context.Context, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, bin, append(slices.Clone(base), args...)...) //nolint:gosec // G204: the operator's docker CLI on the runner's own arguments
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return string(out), nil
	}
}

// newHTTPClient: one client for every request; its timeout is above the
// limit so that an answer past the deadline is measured, not cut short.
func newHTTPClient(limit time.Duration) *http.Client {
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: time.Second}).DialContext,
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 1024,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: limit + time.Second}
}

// errNoSample: a database read that answered nothing parseable.
var errNoSample = errors.New("unreadable answer")
