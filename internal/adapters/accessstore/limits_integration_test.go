//go:build integration

package accessstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/logging"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// slowSources holds a storage permit for a fixed time before each register
// read: the synthetic storage load of the overload test.
type slowSources struct {
	*store.Store
	hold time.Duration
}

func (s slowSources) Register(ctx context.Context) ([]access.Terms, error) {
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `select pg_sleep($1)`, s.hold.Seconds())
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Store.Register(ctx)
}

// oneRead answers the register read before the test from memory, and no
// source rows: each /v1/key request then takes exactly one permit, so the
// order permits are handed out in is the order requests are served in. It
// holds its permit until the test opens the stage the permit belongs to:
// the first two permits handed out are stage 0, the next two stage 1, every
// later one stage 2. Nothing is released by a clock, so who holds a permit
// when the test looks, and the order the requests are served in, cannot
// depend on how long a request took to reach the queue or to finish.
type oneRead struct {
	slowSources
	register []access.Terms
	stages   *stages
}

// stages are the gates of the permits handed out, in that order.
type stages struct {
	granted atomic.Int32
	gates   [3]chan struct{}
	once    [3]sync.Once
}

func newStages() *stages {
	s := &stages{}
	for i := range s.gates {
		s.gates[i] = make(chan struct{})
	}
	return s
}

// open lets the permits of stage i go.
func (s *stages) open(i int) { s.once[i].Do(func() { close(s.gates[i]) }) }

// openAll lets every permit go.
func (s *stages) openAll() {
	for i := range s.gates {
		s.open(i)
	}
}

// settle lets every permit go and waits for the test's requests when it
// ends, so none of them reports on a test that has already finished.
func (s *stages) settle(t *testing.T, wg *sync.WaitGroup) {
	t.Cleanup(func() {
		s.openAll()
		wg.Wait()
	})
}

func (o oneRead) Register(ctx context.Context) ([]access.Terms, error) {
	err := o.Read(ctx, func(ctx context.Context, _ pgx.Tx) error {
		stage := min(int(o.stages.granted.Add(1)-1)/2, len(o.stages.gates)-1)
		select {
		case <-o.stages.gates[stage]:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	return o.register, err
}

func (oneRead) Sources(context.Context, []string) ([]access.Source, error) { return nil, nil }

// limitedStack is the whole service with every gate on: admission in front
// of the reader pool, the key gates and the meter on the access store.
type limitedStack struct {
	stack
	store *accessstore.Store
	meter *metering.Meter
	adm   *admission.Controller
}

func newLimitedStack(t *testing.T, hold time.Duration, extra ...string) limitedStack {
	t.Helper()
	return newLimitedStackOf(t, func(s slowSources) httpapi.SourceReader { return s }, hold, extra...)
}

func newLimitedStackOf(t *testing.T, sources func(slowSources) httpapi.SourceReader, hold time.Duration, extra ...string) limitedStack {
	t.Helper()
	c := cfg(t, extra...)
	as := open(t)
	st, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	adm := admission.New(wire.AdmissionSettings(c), nil)
	st.WithAdmission(adm)
	meter := metering.New(wire.MeteringSettings(c), as, nil)
	var log bytes.Buffer
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: c, Sources: sources(slowSources{st, hold}), Auth: auth.New(wire.AuthSettings(c), as, nil),
		Logger: logging.New(c.Log, &log), ContractVersion: contract.Version(),
		Keys: ratelimit.NewKeyLimiter(wire.QuotaZone(c), as, nil), Meter: meter,
	})
	if err != nil {
		t.Fatal(err)
	}
	return limitedStack{stack: stack{h: h, keys: auth.NewKeys(wire.AuthSettings(c), as, nil, nil), log: &log}, store: as, meter: meter, adm: adm}
}

// contended is the stack of the contended tests: two permits, none kept for
// the critical tier, and the bulk tier allowed to hold both, so a bulk flood
// can take the whole pool and the order of the waiters behind it is decided
// by dispatch alone (RATE-009, RATE-010). Each permit is held until its
// stage is opened (oneRead).
func contended(t *testing.T, agingAfter string) (limitedStack, *stages) {
	t.Helper()
	st := newStages()
	t.Cleanup(st.openAll)
	return newLimitedStackOf(t, func(s slowSources) httpapi.SourceReader {
		register, err := s.Store.Register(bounded(t))
		if err != nil {
			t.Fatal(err)
		}
		return oneRead{s, register, st}
	}, 0,
		"VFAPI_DB_POOL_MAX=2", "VFAPI_TIERS_CRITICAL_RESERVED=0", "VFAPI_TIERS_BULK_MAX_INFLIGHT=2",
		"VFAPI_TIERS_PER_KEY_INFLIGHT=1", "VFAPI_ADMISSION_AGING_AFTER="+agingAfter,
		"VFAPI_TIERS_CRITICAL_QUEUE_MAX_WAIT=1500ms", "VFAPI_TIERS_STANDARD_QUEUE_MAX_WAIT=1500ms", "VFAPI_TIERS_BULK_QUEUE_MAX_WAIT=1500ms"), st
}

// handOn opens the holders' stage and waits until both holders are served
// and their permits handed on, and takes the snapshot of who took them; then
// lets those two finish, waits until both are served, and lets the last
// waiter go. Each stage is served before the next is opened, so the order
// served is the order the permits were handed out.
func (s limitedStack) handOn(t *testing.T, ctx context.Context, st *stages, mu *sync.Mutex, order *[]string) admission.Snapshot {
	t.Helper()
	st.open(0)
	servedN(t, ctx, mu, order, 2)
	snap := s.until(t, ctx, "both permits handed on", func(n admission.Snapshot) bool {
		return int(st.granted.Load()) == 4 && n.Free == 0
	})
	st.open(1)
	servedN(t, ctx, mu, order, 4)
	st.open(2)
	return snap
}

// servedN waits, within the test's bound, until n requests are served.
func servedN(t *testing.T, ctx context.Context, mu *sync.Mutex, order *[]string, n int) {
	t.Helper()
	for {
		mu.Lock()
		got := len(*order)
		mu.Unlock()
		if got >= n {
			return
		}
		if ctx.Err() != nil {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("never served %d: %v", n, *order)
		}
		time.Sleep(time.Millisecond)
	}
}

// served runs one /v1/key request in the background and, when it is
// answered, appends name to the order (on a 200) or fails the test.
func (s limitedStack) served(t *testing.T, ctx context.Context, wg *sync.WaitGroup, mu *sync.Mutex, order *[]string, name, key string) {
	wg.Go(func() {
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/key", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.h.ServeHTTP(w, r)
		mu.Lock()
		defer mu.Unlock()
		if w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
			return
		}
		*order = append(*order, name)
	})
}

// until waits, within the test's bound, for the admission to reach a state.
func (s limitedStack) until(t *testing.T, ctx context.Context, what string, ok func(admission.Snapshot) bool) admission.Snapshot {
	t.Helper()
	for {
		snap := s.adm.Snapshot()
		if ok(snap) {
			return snap
		}
		if ctx.Err() != nil {
			t.Fatalf("never reached %s: %+v", what, snap)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestUnderContentionPermitsGoToTheHighestTierFirst: two bulk requests hold
// every permit, nothing being reserved; a bulk, a standard and a critical
// request queue behind them in that order. When the permits come back they
// go to the critical and the standard request, and the bulk one, the oldest
// waiter, is served last (RATE-009).
func TestUnderContentionPermitsGoToTheHighestTierFirst(t *testing.T) {
	req.Covers(t, "RATE-008", "RATE-009")
	s, st := contended(t, "2s")
	holders := []access.Key{s.keyOn(t, "bulk", 1000, 1000, 0), s.keyOn(t, "bulk", 1000, 1000, 0)}
	bulk, standard, critical := s.keyOn(t, "bulk", 1000, 1000, 0), s.keyOn(t, "standard", 1000, 1000, 0), s.keyOn(t, "critical", 1000, 1000, 0)
	ctx := bounded(t)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		order []string
	)
	st.settle(t, &wg)
	s.served(t, ctx, &wg, &mu, &order, "holder", holders[0].Full)
	s.served(t, ctx, &wg, &mu, &order, "holder", holders[1].Full)
	s.until(t, ctx, "bulk holding both permits", func(n admission.Snapshot) bool { return n.Inflight[admission.Bulk] == 2 && n.Free == 0 })
	s.served(t, ctx, &wg, &mu, &order, "bulk", bulk.Full)
	s.until(t, ctx, "the bulk waiter", func(n admission.Snapshot) bool { return n.Waiting[admission.Bulk] == 1 })
	s.served(t, ctx, &wg, &mu, &order, "standard", standard.Full)
	s.until(t, ctx, "the standard waiter", func(n admission.Snapshot) bool { return n.Waiting[admission.Standard] == 1 })
	s.served(t, ctx, &wg, &mu, &order, "critical", critical.Full)
	s.until(t, ctx, "the critical waiter", func(n admission.Snapshot) bool { return n.Waiting[admission.Critical] == 1 })
	snap := s.handOn(t, ctx, st, &mu, &order)
	if snap.Inflight[admission.Critical] != 1 || snap.Inflight[admission.Standard] != 1 || snap.Waiting[admission.Bulk] != 1 {
		t.Errorf("the permits did not go to the higher tiers: %+v", snap)
	}
	wg.Wait()
	if len(order) != 5 || order[4] != "bulk" || !slices.Contains(order[2:4], "critical") || !slices.Contains(order[2:4], "standard") {
		t.Fatalf("served in the order %v; want both holders, then critical and standard, then bulk", order)
	}
}

// TestAnAgedBulkWaiterIsServedAheadOfNewerStandardWaiters: two bulk
// requests hold every permit; a bulk request queues, and two standard ones
// queue well after it. With admission.aging_after shorter than the bulk
// waiter's wait and longer than the standard ones', the bulk waiter counts
// as standard and, being the oldest, takes one of the two permits; with
// aging out of reach, both standard waiters go first (RATE-010). Each wait
// is measured from the moment the waiter is seen in the queue: the bulk
// waiter has waited more than 250 ms when the holders let go, each standard
// waiter only as long as that takes.
func TestAnAgedBulkWaiterIsServedAheadOfNewerStandardWaiters(t *testing.T) {
	req.Covers(t, "RATE-009", "RATE-010")
	const bulkWait = 250 * time.Millisecond
	for _, tc := range []struct {
		aging string
		aged  bool
	}{{"200ms", true}, {"2s", false}} {
		t.Run(tc.aging, func(t *testing.T) {
			s, st := contended(t, tc.aging)
			keys := []access.Key{s.keyOn(t, "bulk", 1000, 1000, 0), s.keyOn(t, "bulk", 1000, 1000, 0), s.keyOn(t, "bulk", 1000, 1000, 0),
				s.keyOn(t, "standard", 1000, 1000, 0), s.keyOn(t, "standard", 1000, 1000, 0)}
			ctx := bounded(t)
			var (
				wg    sync.WaitGroup
				mu    sync.Mutex
				order []string
			)
			st.settle(t, &wg)
			s.served(t, ctx, &wg, &mu, &order, "holder", keys[0].Full)
			s.served(t, ctx, &wg, &mu, &order, "holder", keys[1].Full)
			s.until(t, ctx, "bulk holding both permits", func(n admission.Snapshot) bool { return n.Inflight[admission.Bulk] == 2 })
			s.served(t, ctx, &wg, &mu, &order, "bulk", keys[2].Full)
			s.until(t, ctx, "the bulk waiter", func(n admission.Snapshot) bool { return n.Waiting[admission.Bulk] == 1 })
			queued := time.Now()
			// The bulk waiter waits past 200 ms, and short of 2 s, before
			// the standard waiters arrive. The 200 ms case still needs the
			// standard waiters to be younger than 200 ms when the holders
			// let go: they are, by the few milliseconds between being seen
			// in the queue and the hand-on.
			time.Sleep(bulkWait)
			s.served(t, ctx, &wg, &mu, &order, "standard", keys[3].Full)
			s.served(t, ctx, &wg, &mu, &order, "standard", keys[4].Full)
			s.until(t, ctx, "the standard waiters", func(n admission.Snapshot) bool { return n.Waiting[admission.Standard] == 2 })
			if d := time.Since(queued); d >= time.Second {
				t.Fatalf("the bulk waiter has waited %s; it no longer brackets the 2 s threshold", d)
			}
			snap := s.handOn(t, ctx, st, &mu, &order)
			if n := snap.Waiting[admission.Bulk] + snap.Waiting[admission.Standard]; n != 1 {
				t.Fatalf("aging %s: %d waiting after the hand-on, want 1: %+v", tc.aging, n, snap)
			}
			if got := snap.Waiting[admission.Bulk] == 0; got != tc.aged {
				t.Errorf("aging %s: the bulk waiter served ahead %v, want %v: %+v", tc.aging, got, tc.aged, snap)
			}
			wg.Wait()
			want := "standard"
			if !tc.aged {
				want = "bulk"
			}
			if len(order) != 5 || order[4] != want {
				t.Fatalf("aging %s: served in the order %v; want %s last", tc.aging, order, want)
			}
		})
	}
}

// keyOn issues a key on a new plan of the tier with the given limits.
func (s limitedStack) keyOn(t *testing.T, tier string, rate, burst int, day int64) access.Key {
	t.Helper()
	planSeq++
	name := fmt.Sprintf("plan-%d", planSeq)
	p := access.Plan{Name: name, UseClass: "consumer", Tier: tier, Scopes: access.Scopes, Sources: allFamilies, Rate: rate, Burst: burst, QuotaDay: day}
	if err := s.keys.PutPlan(bounded(t), p, "tester"); err != nil {
		t.Fatal(err)
	}
	key, err := s.keys.Issue(bounded(t), name, "l", "c", 0, "tester")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// TestAHigherTierKeepsItsLatencyUnderOverload is acceptance criterion 1 of
// issue 8 (RATE-017), on a test stack: two permits, each storage read
// holding one for 40 ms. Sixteen bulk callers flood the service far above
// its capacity while one critical caller asks in sequence. Every critical
// answer is a 200 inside the latency target; bulk is shed with 429, the
// overloaded problem and Retry-After; no request runs past the 2 s ceiling.
func TestAHigherTierKeepsItsLatencyUnderOverload(t *testing.T) {
	req.Covers(t, "RATE-008", "RATE-009", "RATE-011", "RATE-012", "RATE-017")
	const (
		hold     = 40 * time.Millisecond
		target   = 400 * time.Millisecond
		flooders = 16
		asks     = 8
	)
	s := newLimitedStack(t, hold, "VFAPI_DB_POOL_MAX=2", "VFAPI_TIERS_CRITICAL_RESERVED=1",
		"VFAPI_TIERS_BULK_MAX_INFLIGHT=1", "VFAPI_TIERS_BULK_QUEUE_LEN=2", "VFAPI_TIERS_BULK_QUEUE_MAX_WAIT=100ms",
		"VFAPI_TIERS_PER_KEY_INFLIGHT=2")
	critical := s.keyOn(t, "critical", 1000, 1000, 0)
	// The largest bucket a plan may have, so the flood is shed by the
	// admission and not by the key's own bucket alone.
	bulk := s.keyOn(t, "bulk", 100000, 100000, 0)

	ctx, stop := context.WithCancel(bounded(t))
	var (
		mu      sync.Mutex
		types   = map[string]int{}
		shed    []*httptest.ResponseRecorder
		slowest time.Duration
		wg      sync.WaitGroup
	)
	for range flooders {
		wg.Go(func() {
			for ctx.Err() == nil {
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/key", nil)
				r.Header.Set("Authorization", "Bearer "+bulk.Full)
				w := httptest.NewRecorder()
				began := time.Now()
				s.h.ServeHTTP(w, r)
				took := time.Since(began)
				var p struct{ Type string }
				_ = json.Unmarshal(w.Body.Bytes(), &p)
				kind := strconv.Itoa(w.Code) + " " + path.Base(p.Type)
				mu.Lock()
				types[kind]++
				slowest = max(slowest, took)
				if strings.HasSuffix(p.Type, "/overloaded") && len(shed) < 4 {
					shed = append(shed, w)
				}
				mu.Unlock()
				// Paced, so the flood is load on the service and not a
				// busy loop starving the test's own scheduler.
				time.Sleep(time.Millisecond)
			}
		})
	}
	// Let the flood fill the bulk queue first.
	time.Sleep(2 * hold)
	var (
		worst  time.Duration
		times  []time.Duration
		failed []string
	)
	for i := range asks {
		start := time.Now()
		w := s.get(t, critical.Full)
		took := time.Since(start)
		if w.Code != http.StatusOK {
			failed = append(failed, fmt.Sprintf("critical request %d: %d %s", i, w.Code, w.Body))
		}
		worst = max(worst, took)
		times = append(times, took)
	}
	stop()
	wg.Wait()
	for _, f := range failed {
		t.Error(f)
	}
	if worst > target {
		t.Errorf("the slowest critical answer took %v, over the %v target", worst, target)
	}
	if slowest > testBound {
		t.Errorf("a bulk request ran %v, past the %v ceiling", slowest, testBound)
	}
	if types["429 overloaded"] == 0 || len(shed) == 0 {
		t.Fatalf("no bulk request was shed as overloaded: %v", types)
	}
	for _, w := range shed {
		ra, err := strconv.Atoi(w.Header().Get("Retry-After"))
		if err != nil || ra < 1 || w.Header().Get("RateLimit") == "" {
			t.Errorf("a shed answer without Retry-After or RateLimit: %v", w.Header())
		}
	}
	slices.Sort(times)
	t.Logf("critical: %d answers, median %v, slowest %v; bulk: slowest %v, answers %v", asks, times[len(times)/2], worst, slowest, types)
}

// TestUsageIsRecordedPerKeyAndHour is acceptance criterion 3 of issue 8:
// each keyed request is counted per key, hour and operation, a refusal by
// its reason, and the maintainers read it back through the key ledger.
func TestUsageIsRecordedPerKeyAndHour(t *testing.T) {
	req.Covers(t, "RATE-002", "RATE-003", "RATE-014")
	s := newLimitedStack(t, 0)
	ctx := bounded(t)
	tight := s.keyOn(t, "standard", 1, 1, 0)
	quota := s.keyOn(t, "standard", 1000, 1000, 2)
	if a, b := s.get(t, tight.Full), s.get(t, tight.Full); a.Code != http.StatusOK || b.Code != http.StatusTooManyRequests {
		t.Fatalf("the bucket: %d %d", a.Code, b.Code)
	}
	var last *httptest.ResponseRecorder
	for range 3 {
		last = s.get(t, quota.Full)
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("RateLimit-Policy") == "" {
		t.Fatalf("the quota: %d %v", last.Code, last.Header())
	}
	if rep := s.meter.Flush(ctx); rep.Err != nil || rep.Rows != 2 {
		t.Fatalf("flush: %+v", rep)
	}
	// A second flush of later counts adds to the same row.
	s.get(t, quota.Full)
	if rep := s.meter.Flush(ctx); rep.Err != nil || rep.Rows != 1 {
		t.Fatalf("second flush: %+v", rep)
	}
	rows, err := s.keys.Usage(ctx, tight.ID, time.Hour)
	if err != nil || len(rows) != 1 {
		t.Fatalf("usage of one key: %v %+v", err, rows)
	}
	if r := rows[0]; r.Operation != "getKey" || r.Requests != 2 || r.RateLimited != 1 || r.Hour.Location() != time.UTC || r.Hour.Minute() != 0 {
		t.Fatalf("the bucket's row: %+v", r)
	}
	all, err := s.keys.Usage(ctx, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(r metering.Row) bool { return r.Key == quota.ID })
	if i < 0 || all[i].Requests != 4 || all[i].QuotaExceeded != 2 {
		t.Fatalf("the quota's row: %+v", all)
	}
	// The recorded units seed a new instance's quota window: the key
	// spent its two today, so a restarted gate refuses it at once.
	day, _, err := s.store.Used(ctx, quota.ID, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if err != nil || day != 2 {
		t.Fatalf("units used: %d %v", day, err)
	}
	fresh := newLimitedStack(t, 0)
	if w := fresh.get(t, quota.Full); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a restarted gate forgot the spent quota: %d", w.Code)
	}
}

func TestUsageStoreRefusalsAndPruning(t *testing.T) {
	req.Covers(t, "RATE-014", "SEC-044")
	s := open(t)
	c := cfg(t)
	k := auth.NewKeys(wire.AuthSettings(c), s, nil, nil)
	ctx := bounded(t)
	key, err := k.Issue(ctx, newPlan(t, k, "consumer"), "l", "c", 0, "tester")
	if err != nil {
		t.Fatal(err)
	}
	// The plan's limits are read back with the key.
	rec, err := s.Lookup(ctx, key.ID)
	if err != nil || rec.Plan.Rate != 1000 || rec.Plan.Burst != 1000 {
		t.Fatalf("lookup: %v %+v", err, rec.Plan)
	}
	if err := s.AddUsage(ctx, []metering.Row{{Key: "nosuchkey0", Hour: time.Now().UTC().Truncate(time.Hour), Operation: "getKey", Counts: metering.Counts{Requests: 1}}}); err == nil {
		t.Error("usage of an unknown key was stored")
	}
	ended, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.Used(ended, key.ID, time.Now(), time.Now()); err == nil {
		t.Error("an ended context read the used units")
	}
	if _, err := s.Usage(ended, key.ID, time.Now()); err == nil {
		t.Error("an ended context read the usage")
	}
	old := time.Now().UTC().Add(-40 * 24 * time.Hour).Truncate(time.Hour)
	if err := s.AddUsage(ctx, []metering.Row{{Key: key.ID, Hour: old, Operation: "getKey", Counts: metering.Counts{Requests: 3}}}); err != nil {
		t.Fatal(err)
	}
	// The configured retention, 400 days, keeps it; 30 days removes it.
	if n, err := k.PruneUsage(ctx, "tester"); err != nil || n != 0 {
		t.Fatalf("prune at 400 days: %d %v", n, err)
	}
	short := auth.NewKeys(wire.AuthSettings(cfg(t, "VFAPI_METERING_RETENTION_DAYS=30")), s, nil, nil)
	if n, err := short.PruneUsage(ctx, "tester"); err != nil || n != 1 {
		t.Fatalf("prune at 30 days: %d %v", n, err)
	}
	// Whatever the caller asks, nothing younger than 30 days goes.
	if _, err := s.PruneUsage(ctx, auth.Change{Action: auth.ActionUsage, Actor: "t", At: time.Now()}, 29); err == nil {
		t.Error("usage younger than 30 days was pruned")
	}
	var audited int
	if err := owner(t).QueryRow(ctx, `select count(*) from access.audit where action = 'usage_pruned'`).Scan(&audited); err != nil || audited < 2 {
		t.Fatalf("pruning is not audited: %d %v", audited, err)
	}
}
