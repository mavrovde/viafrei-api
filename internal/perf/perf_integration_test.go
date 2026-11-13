//go:build integration

package perf_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/perf"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The benchmark leg runs this file with -perf.assert, a larger sample and,
// for a release, -perf.profile=benchmark (docs/performance.md). The
// integration job runs it as a smoke: every plan and cache assertion, the
// latencies reported but not asserted (a race-instrumented run is no
// latency measurement).
var (
	profile = flag.String("perf.profile", "ci", "fixture profile to load: tiny, ci or benchmark")
	samples = flag.Int("perf.samples", 30, "timed requests per operation, after the warm-up")
	assert  = flag.Bool("perf.assert", false, "fail when an operation misses its p95 or p99 budget")
)

const (
	warmup      = 10
	concurrency = 4
	// testBound bounds one request; the loops around them are bounded by the
	// sample count.
	testBound = 2 * time.Second
)

var (
	db      *pgtest.DB
	loadErr error
)

// TestMain starts PostGIS, adds day partitions beside the default ones and
// loads the fixtures: all setup, outside every test body.
func TestMain(m *testing.M) {
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	var err error
	db, err = pgtest.Start(ctx)
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", err)
		os.Exit(1)
	}
	loadErr = loadFixtures(ctx)
	cancel()
	code := m.Run()
	db.Stop()
	os.Exit(code)
}

// loadFixtures partitions every time-series relation by day across the fixture
// window, loads the fixtures and analyses them, as a database with a long
// history is.
func loadFixtures(ctx context.Context) error {
	conn, err := db.Owner(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	parents, err := partitioned(ctx, conn)
	if err != nil {
		return err
	}
	if len(parents) == 0 {
		return fmt.Errorf("the contract stub has no partitioned relation")
	}
	for _, p := range parents {
		for d := -2; d <= 10; d++ {
			from := fixtures.ReferenceInstant.AddDate(0, 0, d)
			stmt := fmt.Sprintf("create table contract.%s_%s partition of contract.%s for values from ('%s') to ('%s')",
				p, from.Format("20060102"), p, from.Format(time.RFC3339), from.AddDate(0, 0, 1).Format(time.RFC3339))
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return err
			}
		}
	}
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, *profile, 1, contract.Version()); err != nil {
		return err
	}
	if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, buf.String())
		return err
	}); err != nil {
		return err
	}
	_, err = conn.Exec(ctx, "analyze") //nolint:misspell // the SQL keyword
	return err
}

// partitioned names the partitioned relations of the contract schema.
func partitioned(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, _ := conn.Query(ctx, `
select c.relname from pg_partitioned_table p
join pg_class c on c.oid = p.partrelid
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = 'contract' order by 1`)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// partitions names every partition of a partitioned contract relation.
func partitions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, _ := conn.Query(ctx, `
select c.relname from pg_inherits i
join pg_class c on c.oid = i.inhrelid
join pg_class p on p.oid = i.inhparent
join pg_namespace n on n.oid = p.relnamespace
where n.nspname = 'contract' and p.relkind = 'p'`)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out, err
}

// statement is one SQL statement the service sent, with its arguments.
type statement struct {
	sql  string
	args []any
}

// capture records every read statement the store sends.
type capture struct {
	mu    sync.Mutex
	seen  map[string]statement
	count map[string]*atomic.Int64
}

func (c *capture) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	sql := strings.TrimSpace(d.SQL)
	lower := strings.ToLower(sql)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return ctx
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[sql]; !ok {
		c.seen[sql] = statement{sql: sql, args: slices.Clone(d.Args)}
		c.count[sql] = &atomic.Int64{}
	}
	c.count[sql].Add(1)
	return ctx
}

func (c *capture) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// reads counts the statements sent that mention relation.
func (c *capture) reads(relation string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int64
	for sql, k := range c.count {
		if strings.Contains(sql, relation) {
			n += k.Load()
		}
	}
	return n
}

type keyAuth struct{ p auth.Principal }

func (k keyAuth) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return k.p, nil
}

// rig is the service over the loaded database: the real store, use cases
// and handler, a key allowed every source, and the SQL it sends captured.
type rig struct {
	cfg     *config.Config
	st      *store.Store
	sql     *capture
	key     auth.Principal
	now     time.Time
	targets map[string][]string
}

func open(t *testing.T) *rig {
	t.Helper()
	if loadErr != nil {
		t.Fatalf("loading the fixtures: %v", loadErr)
	}
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + db.ReaderURL}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	pc, err := store.PoolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &capture{seen: map[string]statement{}, count: map[string]*atomic.Int64{}}
	pc.ConnConfig.Tracer = c
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r := &rig{cfg: cfg, st: store.New(pool, cfg), sql: c, now: fixtures.ReferenceInstant.AddDate(0, 0, 2), targets: map[string][]string{}}
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	sources, err := pgx.CollectRows(must(conn.Query(ctx, "select source_ref from contract.source order by 1")), pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	r.key = auth.Principal{Key: access.Record{
		ID: "benchmarkk", Environment: access.Live, ExpiresAt: r.now.Add(24 * time.Hour),
		Plan: access.Plan{Name: "benchmark", UseClass: "commercial", Tier: "standard", Sources: sources,
			Scopes: []access.Scope{"places", "energy", "parking", "roads", "weather", "rules"}},
	}}
	near := func(table, path string) {
		pts, err := pgx.CollectRows(must(conn.Query(ctx, fmt.Sprintf(
			"select round(st_y(location)::numeric, 3)::text || ',' || round(st_x(location)::numeric, 3)::text from contract.%s where withdrawn_at is null order by 1 limit 40", table))), pgx.RowTo[string])
		if err != nil || len(pts) == 0 {
			t.Fatalf("no %s to ask near: %v", table, err)
		}
		for _, p := range pts {
			r.targets[path] = append(r.targets[path], path+"?at="+p+"&radius_m=5000")
		}
	}
	near("charging_site", "/v1/charging-options")
	near("parking_facility", "/v1/parking-options")
	ids, err := pgx.CollectRows(must(conn.Query(ctx,
		"select place_id from contract.place where withdrawn_at is null order by prominence desc, place_id limit 40")), pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		t.Fatalf("no place to ask about: %v", err)
	}
	for _, id := range ids {
		r.targets["overview"] = append(r.targets["overview"], "/v1/places/"+id+"/overview")
	}
	// The road reads: each motorway's conditions, the conditions near each
	// junction, and each situation in force at the benchmark's instant.
	list := func(key, query, prefix, suffix string, args ...any) {
		vals, err := pgx.CollectRows(must(conn.Query(ctx, query, args...)), pgx.RowTo[string])
		if err != nil || len(vals) == 0 {
			t.Fatalf("nothing to ask for %s: %v", key, err)
		}
		for _, v := range vals {
			r.targets[key] = append(r.targets[key], prefix+v+suffix)
		}
	}
	list("motorway", "select replace(road_ref, ' ', '') from contract.road where road_ref like 'A%' order by 1 limit 40",
		"/v1/motorways/", "/conditions")
	list("road-conditions", "select round(st_y(location)::numeric, 3)::text || ',' || round(st_x(location)::numeric, 3)::text from contract.junction order by 1 limit 40",
		"/v1/road-conditions?at=", "&radius_m=50000")
	// The weather and rules reads: the warnings at a point of each warning
	// area, and each rule topic.
	list("weather", "select round(st_y(p)::numeric, 3)::text || ',' || round(st_x(p)::numeric, 3)::text "+
		"from (select st_pointonsurface(boundary) as p from contract.admin_region where region_level = 'district') d order by 1 limit 40",
		"/v1/weather-risk?at=", "")
	list("rules", "select topic_code from contract.rule_topic order by 1 limit 40", "/v1/driving-rules?topic=", "")
	// A situation is answered only from a family the key may read: the
	// licence gate decides, as it does for the request.
	reg, err := r.st.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	permitted, _, err := access.NewGate(reg, r.key.Key.Plan, nil, cfg.Sources.Disabled).Answer(access.Answering(reg, "roads"))
	if err != nil {
		t.Fatal(err)
	}
	list("situation", "select event_id from contract.traffic_event where source_ref = any($1) and withdrawn_at is null "+
		"and (valid_to is null or valid_to > $2) order by 1 limit 40", "/v1/road-situations/", "", permitted, r.now)
	return r
}

func must(rows pgx.Rows, _ error) pgx.Rows { return rows }

// handler builds the handler, with or without the HTTP cache.
func (r *rig) handler(t *testing.T, cached bool) http.Handler {
	t.Helper()
	cfg := *r.cfg
	cfg.Cache.Enabled = cached
	now := func() time.Time { return r.now }
	expected, err := health.ExpectedVersion("", contract.Version())
	if err != nil {
		t.Fatal(err)
	}
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: &cfg, Build: buildinfo.Info{Version: "bench", Commit: "bench"}, ContractVersion: contract.Version(), Now: now,
		Readiness: health.NewChecker(r.st, expected, cfg.Health.ReadyTimeout),
		Auth:      keyAuth{r.key}, Sources: r.st, Versions: r.st,
		Features: features.New(features.Repositories{Register: r.st, Charging: r.st, Parking: r.st, Places: r.st, Roads: r.st, Weather: r.st, Rules: r.st}, wire.FeatureSettings(&cfg), now),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// targetsOf are the requests one operation is measured with.
func (r *rig) targetsOf(op string) []string {
	switch op {
	case "getHealth":
		return []string{"/healthz"}
	case "getReadiness":
		return []string{"/readyz"}
	case "getMeta":
		return []string{"/v1/meta"}
	case "getKey":
		return []string{"/v1/key"}
	case "listChargingOptions":
		return r.targets["/v1/charging-options"]
	case "listParkingOptions":
		return r.targets["/v1/parking-options"]
	case "getPlaceOverview":
		return r.targets["overview"]
	case "getMotorwayConditions":
		return r.targets["motorway"]
	case "listRoadConditions":
		return r.targets["road-conditions"]
	case "getRoadSituation":
		return r.targets["situation"]
	case "getWeatherRisk":
		return r.targets["weather"]
	case "getDrivingRules":
		return r.targets["rules"]
	}
	return nil
}

// call sends one request and returns its status, ETag and the time from the
// first byte read to the last byte written, as the service boundary sees it.
func call(h http.Handler, target, ifNoneMatch string) (int, string, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	q := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	q.Header.Set("Authorization", "Bearer benchmark")
	q.Header.Set("Accept-Encoding", "br, gzip")
	if ifNoneMatch != "" {
		q.Header.Set("If-None-Match", ifNoneMatch)
	}
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, q)
	return w.Code, w.Header().Get("ETag"), time.Since(start)
}

// timed sends n requests over the targets from concurrency workers and
// returns their latencies; any answer but want fails.
func timed(t *testing.T, h http.Handler, targets []string, n int, want int, inm func(string) string) []time.Duration {
	t.Helper()
	var (
		mu  sync.Mutex
		out []time.Duration
		bad atomic.Value
		wg  sync.WaitGroup
		at  atomic.Int64
	)
	for range concurrency {
		wg.Go(func() {
			for {
				i := at.Add(1) - 1
				if i >= int64(n) {
					return
				}
				target := targets[int(i)%len(targets)]
				code, _, d := call(h, target, inm(target))
				if code != want {
					bad.Store(fmt.Sprintf("%s answered %d, want %d", target, code, want))
					continue
				}
				mu.Lock()
				out = append(out, d)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if b := bad.Load(); b != nil {
		t.Fatal(b)
	}
	return out
}

func noValidator(string) string { return "" }

// TestEveryOperationMeetsItsBudget measures every operation through the real
// handler and store on the fixtures, the result cache off so each request
// reads storage, and the 304 of a data operation with the cache on.
func TestEveryOperationMeetsItsBudget(t *testing.T) {
	if *assert {
		req.Covers(t, "NFR-PERF-003")
	}
	r := open(t)
	budgets := loadBudgets(t, r.cfg.Deadline.Request)
	h := r.handler(t, false)
	var findings []string
	// One subtest per operation: each is its own leaf under the per-test
	// bound (TST-012), so the catalogue can grow without one test carrying
	// every operation's samples.
	for _, op := range httpapi.OperationIDs() {
		t.Run(op, func(t *testing.T) {
			targets := r.targetsOf(op)
			if len(targets) == 0 {
				t.Errorf("%s has no benchmark request; add one to targetsOf", op)
				return
			}
			class, _ := budgets.Of(op)
			timed(t, h, targets, warmup, http.StatusOK, noValidator)
			res := perf.Measure(op, timed(t, h, targets, *samples, http.StatusOK, noValidator))
			t.Logf("%s  budget %s p95=%s p99=%s", res, class.Name, class.P95, class.P99)
			findings = append(findings, res.Check(class)...)
		})
	}
	t.Run("conditional", func(t *testing.T) {
		cached := r.handler(t, true)
		targets := r.targetsOf("listChargingOptions")
		etags := map[string]string{}
		for _, target := range targets {
			code, etag, _ := call(cached, target, "")
			if code != http.StatusOK || etag == "" {
				t.Fatalf("%s: %d without an ETag", target, code)
			}
			etags[target] = etag
		}
		res := perf.Measure("304 (listChargingOptions)", timed(t, cached, targets, *samples, http.StatusNotModified, func(s string) string { return etags[s] }))
		class := budgets.Classes[perf.Conditional]
		t.Logf("%s  budget %s p95=%s p99=%s", res, class.Name, class.P95, class.P99)
		findings = append(findings, res.Check(class)...)
	})
	if *assert {
		for _, f := range findings {
			t.Error(f)
		}
	} else if len(findings) > 0 {
		t.Logf("not asserted in this run (-perf.assert is off): %s", strings.Join(findings, "; "))
	}
}

func loadBudgets(t *testing.T, deadline time.Duration) perf.Budgets {
	t.Helper()
	c, err := os.Open("../../test/perf/classes.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	o, err := os.Open("../../test/perf/operations.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	b, err := perf.Load(c, o, deadline)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRepeatedRequestsAreNotModified is the repeated-request scenario: a client repeating
// one request with the last ETag is answered 304 at least 95 % of the time.
func TestRepeatedRequestsAreNotModified(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	r := open(t)
	h := r.handler(t, true)
	target := r.targetsOf("listChargingOptions")[0]
	_, etag, _ := call(h, target, "")
	notModified := 0
	const repeats = 100
	for range repeats {
		code, next, _ := call(h, target, etag)
		switch code {
		case http.StatusNotModified:
			notModified++
		case http.StatusOK:
			etag = next
		default:
			t.Fatalf("a repeat answered %d", code)
		}
	}
	if notModified*100 < repeats*95 {
		t.Fatalf("%d of %d repeats were 304; at least 95 %% must be", notModified, repeats)
	}
}

// TestIdenticalConcurrentRequestsReadStorageOnce is the single-flight
// scenario: 50 identical concurrent requests on a cold cache cause exactly
// one storage read of the operation.
func TestIdenticalConcurrentRequestsReadStorageOnce(t *testing.T) {
	req.Covers(t, "NFR-PERF-006")
	r := open(t)
	h := r.handler(t, true)
	target := r.targetsOf("listChargingOptions")[1]
	before := r.sql.reads("contract.charging_site")
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 50)
	for i := range codes {
		wg.Go(func() {
			<-start
			codes[i], _, _ = call(h, target, "")
		})
	}
	close(start)
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("answers %v", codes)
		}
	}
	if n := r.sql.reads("contract.charging_site") - before; n != 1 {
		t.Fatalf("%d storage reads for 50 identical concurrent requests, want 1", n)
	}
}

// TestNoSequentialScanOfTimeSeries runs every operation once, then explains
// every read statement the service sent, as a custom plan with its real
// arguments and as a generic plan, with sequential scans priced out, and
// fails on a sequential scan of any time partition (NFR-PERF-004).
//
// With enable_seqscan off the planner takes a sequential scan only when no
// other path exists, so the test proves that an index path exists for every
// read of a time series. It does not prove the planner picks that path with
// sequential scans priced normally: on the small fixture tables a
// sequential scan is often cheaper, and the planner rightly takes it.
func TestNoSequentialScanOfTimeSeries(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	r := open(t)
	h := r.handler(t, false)
	for _, op := range httpapi.OperationIDs() {
		for _, target := range r.targetsOf(op)[:1] {
			if code, _, _ := call(h, target, ""); code != http.StatusOK {
				t.Fatalf("%s answered %d", target, code)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	parts, err := partitions(ctx, conn)
	if err != nil || len(parts) == 0 {
		t.Fatalf("no time partition to guard: %v", err)
	}
	r.sql.mu.Lock()
	stmts := make([]statement, 0, len(r.sql.seen))
	for _, s := range r.sql.seen {
		stmts = append(stmts, s)
	}
	r.sql.mu.Unlock()
	timeSeries := 0
	for _, s := range stmts {
		for _, mode := range []string{"custom", "generic"} {
			plan, err := explain(ctx, conn, s, mode)
			if err != nil {
				t.Errorf("explain (%s) failed: %v\n%s", mode, err, s.sql)
				continue
			}
			scanned, seq := relations(plan, parts)
			if len(scanned) > 0 && mode == "custom" {
				timeSeries++
			}
			for _, rel := range seq {
				t.Errorf("a sequential scan of the time partition %s (%s plan):\n%s", rel, mode, s.sql)
			}
		}
	}
	t.Logf("explained %d read statements, %d of them on time series, in custom and generic plans", len(stmts), timeSeries)
	if timeSeries == 0 {
		t.Fatal("no statement read a time series: the test guarded nothing")
	}
}

// explain returns the JSON plan of s with sequential scans priced out
// (enable_seqscan = off): a sequential scan in it means no other path exists.
func explain(ctx context.Context, conn *pgx.Conn, s statement, mode string) (any, error) {
	var raw []byte
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local enable_seqscan = off"); err != nil {
			return err
		}
		if mode == "generic" {
			// The raw simple protocol: the driver would otherwise bind (or
			// substitute) the $n placeholders a generic plan keeps.
			res, err := tx.Conn().PgConn().Exec(ctx, "explain (generic_plan, format json) "+s.sql).ReadAll()
			if err != nil {
				return err
			}
			if len(res) != 1 || len(res[0].Rows) != 1 {
				return fmt.Errorf("explain answered %d results", len(res))
			}
			raw = res[0].Rows[0][0]
			return nil
		}
		return tx.QueryRow(ctx, "explain (format json) "+s.sql, s.args...).Scan(&raw)
	})
	if err != nil {
		return nil, err
	}
	var plan any
	return plan, json.Unmarshal(raw, &plan)
}

// relations walks a JSON plan and returns the time partitions it reads and
// those it reads by a sequential scan.
func relations(plan any, parts map[string]bool) (scanned, seq []string) {
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			if rel, ok := v["Relation Name"].(string); ok && parts[rel] {
				scanned = append(scanned, rel)
				if v["Node Type"] == "Seq Scan" {
					seq = append(seq, rel)
				}
			}
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(plan)
	return scanned, seq
}
