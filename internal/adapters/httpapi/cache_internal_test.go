package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

func TestNegotiateCoding(t *testing.T) {
	for accept, want := range map[string]string{
		"":                        "",
		"gzip":                    codingGzip,
		"br, gzip":                codingBrotli,
		"gzip;q=1, br;q=0":        codingGzip,
		"br;q=0, gzip;q=0":        "",
		"*":                       codingBrotli,
		"*;q=0, gzip":             codingGzip,
		"identity":                "",
		"BR":                      codingBrotli,
		"br;q=x, gzip;q=0.5":      codingGzip,
		" gzip ; q=0.2 , deflate": codingGzip,
	} {
		if got := negotiateCoding(accept); got != want {
			t.Errorf("negotiateCoding(%q) = %q, want %q", accept, got, want)
		}
	}
}

func TestCompressible(t *testing.T) {
	for ct, want := range map[string]bool{
		"application/json; charset=utf-8": true,
		"application/problem+json":        true,
		"text/plain":                      true,
		"image/png":                       false,
		"":                                false,
		"not a/type/at all;;":             false,
	} {
		if got := compressible(ct); got != want {
			t.Errorf("compressible(%q) = %v", ct, got)
		}
	}
}

// serveCompressed runs body through the compression layer with the given
// Accept-Encoding and returns the recorded answer.
func serveCompressed(t *testing.T, minBytes int, accept, method string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), method, "/", nil)
	if accept != "" {
		r.Header.Set("Accept-Encoding", accept)
	}
	w := httptest.NewRecorder()
	compress(minBytes, 5)(h).ServeHTTP(w, r)
	return w
}

func jsonBody(n int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(http.StatusOK)
		// Two writes, so the held part and the streamed part are both read.
		body := `"` + strings.Repeat("a", n) + `"`
		_, _ = io.WriteString(w, body[:len(body)/2])
		_, _ = io.WriteString(w, body[len(body)/2:])
	}
}

func TestCompressEncodesALargeBody(t *testing.T) {
	for _, tc := range []struct {
		coding string
		read   func(io.Reader) (io.Reader, error)
	}{
		{codingGzip, func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) }},
		{codingBrotli, func(r io.Reader) (io.Reader, error) { return brotli.NewReader(r), nil }},
	} {
		w := serveCompressed(t, 64, tc.coding, http.MethodGet, jsonBody(4000))
		if w.Header().Get("Content-Encoding") != tc.coding || w.Header().Get("Content-Length") != "" ||
			w.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("%s: headers %v", tc.coding, w.Header())
		}
		r, err := tc.read(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || len(got) != 4002 {
			t.Fatalf("%s: decoded %d bytes, %v", tc.coding, len(got), err)
		}
		// Larger than the threshold only on the second write: the first is held.
		w = serveCompressed(t, 3000, tc.coding, http.MethodGet, jsonBody(4000))
		if w.Header().Get("Content-Encoding") != tc.coding {
			t.Fatalf("%s: not encoded across two writes: %v", tc.coding, w.Header())
		}
	}
}

func TestCompressLeavesAnswersAlone(t *testing.T) {
	plain := func(status int, ct, enc string, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", ct)
			if enc != "" {
				w.Header().Set("Content-Encoding", enc)
			}
			w.WriteHeader(status)
			w.WriteHeader(http.StatusTeapot) // a second status is ignored
			_, _ = io.WriteString(w, body)
		}
	}
	big := strings.Repeat("x", 2000)
	for name, tc := range map[string]struct {
		accept, method string
		h              http.HandlerFunc
		status         int
		body           string
	}{
		"small body":      {"gzip", http.MethodGet, plain(200, "application/json", "", "{}"), 200, "{}"},
		"no coding":       {"identity", http.MethodGet, plain(200, "application/json", "", big), 200, big},
		"head":            {"gzip", http.MethodHead, plain(200, "application/json", "", big), 200, big},
		"image":           {"gzip", http.MethodGet, plain(200, "image/png", "", big), 200, big},
		"already encoded": {"gzip", http.MethodGet, plain(200, "application/json", "zstd", big), 200, big},
		"no content":      {"gzip", http.MethodGet, plain(204, "application/json", "", ""), 204, ""},
		"not modified":    {"gzip", http.MethodGet, plain(304, "application/json", "", big), 304, big},
		"implicit 200": {"gzip", http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "hi")
		}, 200, "hi"},
		"status, no body": {"gzip", http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}, 202, ""},
		"nothing written": {"gzip", http.MethodGet, func(http.ResponseWriter, *http.Request) {}, 200, ""},
	} {
		w := serveCompressed(t, 1024, tc.accept, tc.method, tc.h)
		enc := w.Header().Get("Content-Encoding")
		if w.Code != tc.status || w.Body.String() != tc.body || (enc != "" && enc != "zstd") {
			t.Errorf("%s: status %d, encoding %q, body %d bytes", name, w.Code, enc, w.Body.Len())
		}
	}
}

func TestCompressWriterUnwrapsAndReportsWriteErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := &compressWriter{ResponseWriter: failingWriter{rec}, coding: codingGzip, min: 1, level: 5}
	if cw.Unwrap() != (failingWriter{rec}) {
		t.Fatal("Unwrap does not reach the underlying writer")
	}
	cw.Header().Set("Content-Type", "image/png")
	if _, err := cw.Write([]byte("x")); err == nil {
		t.Fatal("a held body's failed write was swallowed")
	}
	cw = &compressWriter{ResponseWriter: rec, coding: codingGzip, min: 1, level: 5}
	cw.Header().Set("Content-Type", "image/png")
	_, _ = cw.Write([]byte("x"))
	if n, err := cw.Write([]byte("yz")); n != 2 || err != nil {
		t.Fatalf("a streamed write: %d, %v", n, err)
	}
}

type failingWriter struct{ *httptest.ResponseRecorder }

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("gone") }

func TestEtagMatch(t *testing.T) {
	etag := `W/"abc"`
	for inm, want := range map[string]bool{
		"":             false,
		`W/"abc"`:      true,
		`"abc"`:        true,
		`"x", W/"abc"`: true,
		"*":            true,
		`"abd"`:        false,
		`W/"ab"`:       false,
	} {
		if got := etagMatch(inm, etag); got != want {
			t.Errorf("etagMatch(%q) = %v", inm, got)
		}
	}
}

func TestMaxAgePerDomain(t *testing.T) {
	m := config.Defaults().Cache.MaxAge
	for domain, want := range map[string]time.Duration{
		"places": m.Places, "roads": m.Roads, "energy": m.Energy, "parking": m.Parking,
		"transit": m.Transit, "accessibility": m.Accessibility, "weather": m.Weather,
		"rules": m.Rules, "anything else": m.Default,
	} {
		if got := maxAge(m, domain); got != want {
			t.Errorf("%s: %s, want %s", domain, got, want)
		}
	}
}

func TestPermissionsSeparateEveryLicenceInput(t *testing.T) {
	base := access.Record{Environment: "live", Plan: access.Plan{UseClass: "commercial", Sources: []string{"b", "a"}, Scopes: []access.Scope{"energy", "places"}},
		Accepted: []access.Acceptance{{Source: "s", Version: "1"}}}
	same := base
	same.Plan.Sources = []string{"a", "b"}
	same.Plan.Scopes = []access.Scope{"places", "energy"}
	if permissions(base) != permissions(same) {
		t.Fatal("the order of a key's lists changed its permissions")
	}
	for name, mut := range map[string]func(*access.Record){
		"environment": func(r *access.Record) { r.Environment = "test" },
		"use class":   func(r *access.Record) { r.Plan.UseClass = "personal" },
		"sources":     func(r *access.Record) { r.Plan.Sources = []string{"a"} },
		"scopes":      func(r *access.Record) { r.Plan.Scopes = []access.Scope{"energy"} },
		"accepted":    func(r *access.Record) { r.Accepted = []access.Acceptance{{Source: "s", Version: "2"}} },
	} {
		r := base
		r.Plan.Sources = slicesClone(base.Plan.Sources)
		mut(&r)
		if permissions(r) == permissions(base) {
			t.Errorf("%s does not change the permissions", name)
		}
	}
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

func TestResultCacheKeepsItsBudget(t *testing.T) {
	c := newResultCache(config.Cache{MaxBytes: 100, MaxEntryBytes: 40})
	put := func(key string, n int) *entry {
		e := &entry{key: key, etag: "e", body: bytes.Repeat([]byte("x"), n)}
		c.put(e)
		return e
	}
	put("a", 30)
	put("b", 30)
	if c.get("a") == nil { // a is now the most recently used
		t.Fatal("a dropped too early")
	}
	put("c", 30) // 3 × 32 = 96 bytes: all fit
	put("d", 30) // over: the least recently used, b, goes
	if c.get("b") != nil || c.get("a") == nil || c.bytes > 100 {
		t.Fatalf("LRU order not kept: bytes %d", c.bytes)
	}
	if e := put("big", 50); e.body != nil || c.get("big") == nil {
		t.Fatal("an entry above max_entry_bytes kept its body or was not kept at all")
	}
	before := c.bytes
	put("a", 10) // replacing a key does not count it twice
	if c.bytes != before-32+12 {
		t.Fatalf("replacing an entry: %d bytes, want %d", c.bytes, before-32+12)
	}
	none := newResultCache(config.Cache{MaxBytes: 0, MaxEntryBytes: 1024})
	none.put(&entry{key: "k", body: []byte("x")})
	if none.get("k") != nil {
		t.Fatal("max_bytes 0 kept an entry")
	}
}

func TestResultCacheFlights(t *testing.T) {
	c := newResultCache(config.Cache{MaxBytes: 1 << 20, MaxEntryBytes: 1 << 10})
	f, leader := c.join("k")
	g, second := c.join("k")
	if !leader || second || f != g {
		t.Fatal("a second request did not join the running flight")
	}
	c.land("k", f, nil)
	<-f.done
	if f.e != nil || c.get("k") != nil {
		t.Fatal("a flight with nothing cacheable stored something")
	}
	if _, again := c.join("k"); !again {
		t.Fatal("a landed flight still runs")
	}
}

type versionsFake struct {
	vs    map[string]string
	err   error
	calls int
	wait  chan struct{}
}

func (v *versionsFake) FreshnessVersions(context.Context) (map[string]string, error) {
	v.calls++
	if v.wait != nil {
		<-v.wait
	}
	return v.vs, v.err
}

func TestVersionCache(t *testing.T) {
	var none versionCache
	if _, ok := none.get(context.Background()); ok {
		t.Fatal("no reader answered versions")
	}
	now := time.Unix(0, 0)
	fake := &versionsFake{vs: map[string]string{"f": "1"}}
	v := &versionCache{reader: fake, ttl: time.Second, now: func() time.Time { return now }}
	for range 3 {
		if vs, ok := v.get(context.Background()); !ok || vs["f"] != "1" {
			t.Fatal("versions not read")
		}
	}
	if fake.calls != 1 {
		t.Fatalf("read %d times within the TTL", fake.calls)
	}
	now = now.Add(time.Second)
	fake.err = errors.New("down")
	if _, ok := v.get(context.Background()); ok || fake.calls != 2 {
		t.Fatal("an expired snapshot was not re-read, or a failed read answered")
	}
	// While one request reads, the others wait for that read: one that gives
	// up waiting answers nothing, the rest share the result.
	for _, fail := range []bool{false, true} {
		now = now.Add(time.Second)
		fake.err, fake.wait = nil, make(chan struct{})
		if fail {
			fake.err = errors.New("down")
		}
		calls := fake.calls
		results := make(chan bool, 2)
		go func() { _, ok := v.get(context.Background()); results <- ok }()
		waitReading(v)
		go func() { _, ok := v.get(context.Background()); results <- ok }()
		gone, cancel := context.WithCancel(context.Background())
		cancel()
		if _, ok := v.get(gone); ok {
			t.Fatal("a request that gave up waiting got versions")
		}
		time.Sleep(10 * time.Millisecond) // let the second request wait
		close(fake.wait)
		a, b := <-results, <-results
		if a == fail || b == fail || fake.calls != calls+1 {
			t.Fatalf("fail=%v: results %v %v after %d reads", fail, a, b, fake.calls-calls)
		}
	}
}

func waitReading(v *versionCache) {
	for {
		v.mu.Lock()
		reading := v.reading != nil
		v.mu.Unlock()
		if reading {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEntryWithoutAVersionIsNeverCurrent(t *testing.T) {
	e := &entry{families: []familyVersion{{"f", "1"}}}
	if !e.current(map[string]string{"f": "1"}) || e.current(map[string]string{"f": "2"}) {
		t.Fatal("current does not compare versions")
	}
	e.unversioned = true
	if e.current(map[string]string{"f": "1"}) {
		t.Fatal("an unversioned entry is current")
	}
}
