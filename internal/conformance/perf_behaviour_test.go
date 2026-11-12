package conformance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// cachedFeatures answers the charging operation from one family of a
// given domain, padded to a given number of sites, and counts its reads.
type cachedFeatures struct {
	domain string
	sites  int
	reads  atomic.Int32
	down   atomic.Bool // storage fails every read
}

func (f *cachedFeatures) Charging(context.Context, access.Record, energy.Query) (features.Answer[energy.Site], error) {
	f.reads.Add(1)
	if f.down.Load() {
		return features.Answer[energy.Site]{}, store.ErrUnavailable
	}
	fam := provenance.Family{Source: access.Source{Family: "fam-" + f.domain, Domain: f.domain, Publisher: "P",
		AttributionText: "T", Licence: "L", LicenceURL: "https://licences.example/l"}, StaleAfter: time.Hour, Version: "1"}
	items := make([]energy.Site, f.sites)
	for i := range items {
		items[i] = energy.Site{ID: fmt.Sprintf("cs-%04d", i), Family: fam.Source.Family, Access: energy.AccessPublic}
	}
	return features.Answer[energy.Site]{Items: items, Families: []provenance.Family{fam}, At: time.Now()}, nil
}

func (f *cachedFeatures) Parking(context.Context, access.Record, parking.Query) (features.Answer[parking.Site], error) {
	return features.Answer[parking.Site]{}, nil
}

func (f *cachedFeatures) Overview(context.Context, access.Record, features.OverviewQuery) (features.Overview, error) {
	return features.Overview{}, features.ErrNoPart
}

func (f *cachedFeatures) MotorwayConditions(context.Context, access.Record, roads.CorridorQuery) (features.Conditions, error) {
	return features.Conditions{}, nil
}

func (f *cachedFeatures) RoadConditions(context.Context, access.Record, roads.AreaQuery) (features.Conditions, error) {
	return features.Conditions{}, nil
}

func (f *cachedFeatures) RoadSituation(context.Context, access.Record, string) (features.Answer[roads.Situation], error) {
	return features.Answer[roads.Situation]{}, nil
}

// countedVersions answers one fixed version for every family and counts
// its reads.
type countedVersions struct{ reads atomic.Int32 }

func (v *countedVersions) FreshnessVersions(context.Context) (provenance.Versions, error) {
	v.reads.Add(1)
	return provenance.Versions{Families: map[string]string{
		"fam-places": "1", "fam-roads": "1", "fam-energy": "1", "fam-parking": "1", "fam-transit": "1",
		"fam-accessibility": "1", "fam-weather": "1", "fam-rules": "1", "fam-other": "1",
	}}, nil
}

// cacheRun sends the same charging request n times, pause apart, and
// reports the last answer's caching headers, the operation's reads and the
// version reads.
func cacheRun(domain string, sites, n int, pause time.Duration) func(t *testing.T, c *config.Config) string {
	return func(t *testing.T, c *config.Config) string {
		t.Helper()
		f, v := &cachedFeatures{domain: domain, sites: sites}, &countedVersions{}
		h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: f, Versions: v})
		if err != nil {
			t.Fatal(err)
		}
		var w *httptest.ResponseRecorder
		for i := range n {
			if i > 0 {
				time.Sleep(pause)
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/charging-options?at=1,2", nil))
		}
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		return fmt.Sprintf("cache-control=%q etag=%t reads=%d version-reads=%d",
			w.Header().Get("Cache-Control"), w.Header().Get("ETag") != "", f.reads.Load(), v.reads.Load())
	}
}

// cacheAlternate asks two different charging requests, then the first
// again, and reports the operation's reads: with room for one entry the
// first is dropped and read again.
func cacheAlternate(t *testing.T, c *config.Config) string {
	t.Helper()
	f, v := &cachedFeatures{domain: "energy", sites: 1}, &countedVersions{}
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: f, Versions: v})
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"1,2", "3,4", "1,2"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/charging-options?at="+at, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	}
	return fmt.Sprintf("reads=%d", f.reads.Load())
}

// cacheStale reads a charging answer, then asks again two minutes later
// while storage is down, and reports the status and Age of that answer: the
// stored one served stale, or the refusal.
func cacheStale(domain string) func(t *testing.T, c *config.Config) string {
	return func(t *testing.T, c *config.Config) string {
		t.Helper()
		return staleRun(t, c, domain)
	}
}

func staleRun(t *testing.T, c *config.Config, domain string) string {
	t.Helper()
	// Every answer is past its max-age at the second request, so only the
	// stale allowance can answer it, whatever the domain's max-age.
	second := time.Second
	c.Cache.MaxAge = config.CacheMaxAge{Places: second, Roads: second, Energy: second, Parking: second, Transit: second,
		Accessibility: second, Weather: second, Rules: second, Default: second}
	f, v := &cachedFeatures{domain: domain, sites: 1}, &countedVersions{}
	now := time.Now()
	clock := func() time.Time { return now }
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1", Auth: everyScope{}, Features: f, Versions: v, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	var w *httptest.ResponseRecorder
	for range 2 {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/charging-options?at=1,2", nil))
		now = now.Add(2 * time.Minute)
		f.down.Store(true)
	}
	return fmt.Sprintf("status=%d age=%q", w.Code, w.Header().Get("Age"))
}

// compressed fetches the OpenAPI document gzip-encoded and reports the coding
// and the encoded size.
func compressed(t *testing.T, c *config.Config) string {
	t.Helper()
	h, err := httpapi.NewHandler(httpapi.Deps{Config: c, ContractVersion: "0.1"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/openapi.json", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return fmt.Sprintf("%s %d", w.Header().Get("Content-Encoding"), w.Body.Len())
}

// perfBehaviours are the behaviours of the caching, compression and
// statement keys.
func perfBehaviours() map[string]behaviour {
	out := map[string]behaviour{
		"http.compress_min_bytes":     {"1048576", compressed},
		"http.compress_level":         {"1", compressed},
		"db.query_exec_mode":          {"exec", poolConfig},
		"db.statement_cache_capacity": {"16", poolConfig},
		"cache.enabled":               {"false", cacheRun("energy", 1, 2, 0)},
		"cache.max_bytes":             {"0", cacheRun("energy", 1, 2, 0)},
		// 40 sites are above 1024 bytes, below the default entry limit.
		"cache.max_entry_bytes": {"1024", cacheRun("energy", 40, 2, 0)},
		// Three requests 150ms apart read the versions once within 1s, twice
		// within 100ms.
		"cache.versions_ttl": {"100ms", cacheRun("energy", 1, 3, 150*time.Millisecond)},
		// Room for one entry: the first request's entry is dropped by the
		// second and read again by the third.
		"cache.max_entries": {"1", cacheAlternate},
		// An answer read two minutes ago is served stale within five
		// minutes, not within one, and never for a family listed.
		"cache.no_stale_families": {"fam-energy", cacheStale("energy")},
	}
	for _, d := range []string{"places", "roads", "energy", "parking", "transit", "accessibility", "weather", "rules", "default"} {
		domain := d
		if d == "default" {
			domain = "other"
		}
		out["cache.max_age."+d] = behaviour{"2h", cacheRun(domain, 1, 1, 0)}
		out["cache.stale_if_error."+d] = behaviour{"1m", cacheStale(domain)}
	}
	return out
}
