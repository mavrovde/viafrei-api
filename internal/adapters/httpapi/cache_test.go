package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// fakeVersions is the freshness-version register the cache validates with.
type fakeVersions struct {
	mu  sync.Mutex
	vs  map[string]string
	err error
}

func (f *fakeVersions) FreshnessVersions(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]string{"src-alpha-energy": f.vs["src-alpha-energy"]}, f.err
}

func (f *fakeVersions) set(v string) {
	f.mu.Lock()
	f.vs = map[string]string{"src-alpha-energy": v}
	f.mu.Unlock()
}

// countingFeatures counts the charging reads and can hold them.
type countingFeatures struct {
	*fakeFeatures
	calls atomic.Int32
	hold  chan struct{}
	mu    sync.Mutex
}

func (c *countingFeatures) Charging(ctx context.Context, key access.Record, q energy.Query) (features.Answer[energy.Site], error) {
	c.calls.Add(1)
	if c.hold != nil {
		<-c.hold
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fakeFeatures.Charging(ctx, key, q)
}

// clock is a test clock the cache's expiry and the version TTL read.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type cacheRig struct {
	h        http.Handler
	features *countingFeatures
	versions *fakeVersions
	clock    *clock
	auth     *lockedAuth
}

// lockedAuth answers one principal and may be changed between requests.
type lockedAuth struct {
	mu sync.Mutex
	p  auth.Principal
}

func (l *lockedAuth) Authenticate(context.Context, string, string) (auth.Principal, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.p, nil
}

func newCacheRig(t *testing.T, cfg *config.Config, version string) *cacheRig {
	t.Helper()
	answer := chargingAnswer()
	fam := fresh
	fam.Version = version
	answer.Families = []provenance.Family{fam}
	r := &cacheRig{
		features: &countingFeatures{fakeFeatures: &fakeFeatures{charging: answer}},
		versions: &fakeVersions{vs: map[string]string{"src-alpha-energy": version}},
		clock:    &clock{t: fixedNow},
		auth:     &lockedAuth{p: featureScopes()},
	}
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: cfg, Build: buildinfo.Info{Version: "t", Commit: "c"}, ContractVersion: "0.1", Now: r.clock.now,
		Auth: r.auth, Sources: &fakeSources{register: fixtureRegister},
		Features: r.features, Versions: r.versions,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.h = h
	return r
}

// setVersion moves the family to a new version, in the register and in the
// answer the next read builds.
func (r *cacheRig) setVersion(v string) {
	r.versions.set(v)
	r.features.mu.Lock()
	r.features.charging.Families[0].Version = v
	r.features.mu.Unlock()
}

const chargingURL = "/v1/charging-options?at=52.52,13.405"

func (r *cacheRig) get(t *testing.T, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	h := http.Header{"Authorization": {"Bearer x"}}
	for k, vs := range header {
		h[k] = vs
	}
	return do(t, r.h, http.MethodGet, chargingURL, h)
}

func TestARepeatedRequestIsANotModified(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	r := newCacheRig(t, config.Defaults(), "v1")
	first := r.get(t, nil)
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || !strings.HasPrefix(etag, `W/"`) ||
		first.Header().Get("Cache-Control") != "private, max-age=60" ||
		first.Header().Get("Last-Modified") != fixedNow.Add(-5*time.Minute).Format(http.TimeFormat) ||
		!strings.Contains(strings.Join(first.Header().Values("Vary"), ","), "Accept-Language") {
		t.Fatalf("first answer: %d %v", first.Code, first.Header())
	}
	again := r.get(t, http.Header{"If-None-Match": {etag}})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("ETag") != etag {
		t.Fatalf("repeat: %d %v %q", again.Code, again.Header(), again.Body)
	}
	if n := r.features.calls.Load(); n != 1 {
		t.Fatalf("the 304 ran the operation: %d reads", n)
	}
	// A changed family version is a new ETag and a fresh read.
	r.clock.add(2 * time.Second)
	r.setVersion("v2")
	changed := r.get(t, http.Header{"If-None-Match": {etag}})
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etag || r.features.calls.Load() != 2 {
		t.Fatalf("after a version change: %d, etag %q, %d reads", changed.Code, changed.Header().Get("ETag"), r.features.calls.Load())
	}
}

func TestARepeatedRequestIsACacheHit(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	r := newCacheRig(t, config.Defaults(), "v1")
	first := r.get(t, nil)
	second := r.get(t, nil)
	if second.Code != http.StatusOK || r.features.calls.Load() != 1 {
		t.Fatalf("the repeat was not served from the cache: %d, %d reads", second.Code, r.features.calls.Load())
	}
	// The replay carries this request's id, not the stored one.
	id1, id2 := meta(t, decode(t, first))["requestId"], meta(t, decode(t, second))["requestId"]
	if id1 == id2 || id2 != second.Header().Get("X-Request-Id") {
		t.Fatalf("replayed request id %v (first %v, header %q)", id2, id1, second.Header().Get("X-Request-Id"))
	}
	// A stored answer matching If-None-Match is a 304 as well.
	if w := r.get(t, http.Header{"If-None-Match": {first.Header().Get("ETag")}}); w.Code != http.StatusNotModified {
		t.Fatalf("stored answer with a matching validator: %d", w.Code)
	}
	// Past its max-age the body is read again; the ETag is the same while
	// the version is.
	r.clock.add(61 * time.Second)
	third := r.get(t, nil)
	if r.features.calls.Load() != 2 || third.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Fatalf("after max-age: %d reads, etag %q", r.features.calls.Load(), third.Header().Get("ETag"))
	}
}

func TestTheCacheNeverCrossesPermissions(t *testing.T) {
	r := newCacheRig(t, config.Defaults(), "v1")
	first := r.get(t, nil)
	other := featureScopes()
	other.Key.Plan.UseClass = "personal"
	r.auth.mu.Lock()
	r.auth.p = other
	r.auth.mu.Unlock()
	second := r.get(t, http.Header{"If-None-Match": {first.Header().Get("ETag")}})
	if second.Code != http.StatusOK || second.Header().Get("ETag") == first.Header().Get("ETag") || r.features.calls.Load() != 2 {
		t.Fatalf("a key with other permissions shared an answer: %d, %d reads", second.Code, r.features.calls.Load())
	}
	// Another language is another entry too.
	if w := r.get(t, http.Header{"Accept-Language": {"de"}}); w.Code != http.StatusOK || r.features.calls.Load() != 3 {
		t.Fatalf("another language shared an answer: %d reads", r.features.calls.Load())
	}
}

func TestIdenticalConcurrentRequestsReadOnce(t *testing.T) {
	r := newCacheRig(t, config.Defaults(), "v1")
	r.features.hold = make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Go(func() { codes[i] = r.get(t, nil).Code })
	}
	for r.features.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the followers join the flight
	close(r.features.hold)
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("codes %v", codes)
		}
	}
	if n := r.features.calls.Load(); n != 1 {
		t.Fatalf("%d reads for identical concurrent requests", n)
	}
}

func TestAFollowerWithAMatchingValidatorGetsANotModified(t *testing.T) {
	// The ETag depends only on the request, the key and the versions, so a
	// second rig yields the same one.
	etag := newCacheRig(t, config.Defaults(), "v1").get(t, nil).Header().Get("ETag")
	r := newCacheRig(t, config.Defaults(), "v1")
	r.features.hold = make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Go(func() { codes[i] = r.get(t, http.Header{"If-None-Match": {etag}}).Code })
	}
	for r.features.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(r.features.hold)
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusNotModified {
			t.Fatalf("codes %v", codes)
		}
	}
}

func TestAFollowerRunsItselfWhenTheLeaderStoredNothing(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cache.MaxBytes = 0 // nothing keeps a body
	r := newCacheRig(t, cfg, "v1")
	r.features.hold = make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { r.get(t, nil) })
	}
	for r.features.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(r.features.hold)
	wg.Wait()
	if n := r.features.calls.Load(); n != 2 {
		t.Fatalf("%d reads; a follower without a stored body must read itself", n)
	}
}

func TestAFollowerGivesUpWaitingWithItsRequest(t *testing.T) {
	r := newCacheRig(t, config.Defaults(), "v1")
	r.features.hold = make(chan struct{})
	go r.h.ServeHTTP(httptest.NewRecorder(), authed(context.Background()))
	for r.features.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { r.h.ServeHTTP(httptest.NewRecorder(), authed(ctx)); close(done) }()
	for r.features.calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	close(r.features.hold)
	<-done
}

func authed(ctx context.Context) *http.Request {
	q := httptest.NewRequestWithContext(ctx, http.MethodGet, chargingURL, nil)
	q.Header.Set("Authorization", "Bearer x")
	return q
}

func TestAnUnversionedFamilyIsNeitherValidatedNorStored(t *testing.T) {
	r := newCacheRig(t, config.Defaults(), "")
	first := r.get(t, nil)
	again := r.get(t, http.Header{"If-None-Match": {"*"}})
	if first.Header().Get("ETag") != "" || first.Header().Get("Cache-Control") != "private, max-age=60" ||
		again.Code != http.StatusOK || r.features.calls.Load() != 2 {
		t.Fatalf("an unversioned answer: %v, then %d after %d reads", first.Header(), again.Code, r.features.calls.Load())
	}
}

func TestUnreadableVersionsReadTheAnswer(t *testing.T) {
	r := newCacheRig(t, config.Defaults(), "v1")
	first := r.get(t, nil)
	r.versions.mu.Lock()
	r.versions.err = errFake
	r.versions.mu.Unlock()
	r.clock.add(2 * time.Second)
	again := r.get(t, http.Header{"If-None-Match": {first.Header().Get("ETag")}})
	// The answer is read again and still validated by its own versions.
	if again.Code != http.StatusNotModified || r.features.calls.Load() != 2 {
		t.Fatalf("%d after %d reads", again.Code, r.features.calls.Load())
	}
}

func TestCacheDisabledSendsNoStore(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cache.Enabled = false
	r := newCacheRig(t, cfg, "v1")
	first := r.get(t, nil)
	second := r.get(t, http.Header{"If-None-Match": {`"*"`}})
	if first.Header().Get("Cache-Control") != "no-store" || first.Header().Get("ETag") != "" ||
		second.Code != http.StatusOK || r.features.calls.Load() != 2 {
		t.Fatalf("cache.enabled=false: %v, %d, %d reads", first.Header(), second.Code, r.features.calls.Load())
	}
}

func TestAnErrorIsNeverCached(t *testing.T) {
	r := newCacheRig(t, config.Defaults(), "v1")
	r.features.err = errFake
	for range 2 {
		if w := r.get(t, nil); w.Code == http.StatusOK || w.Header().Get("ETag") != "" {
			t.Fatalf("an error answer: %d %v", w.Code, w.Header())
		}
	}
	if r.features.calls.Load() != 2 {
		t.Fatal("an error was served from the cache")
	}
}

var errFake = errors.New("down")
