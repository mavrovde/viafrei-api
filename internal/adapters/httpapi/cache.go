package httpapi

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// VersionReader reads the freshness version of every source family.
type VersionReader interface {
	FreshnessVersions(ctx context.Context) (map[string]string, error)
}

// varyOn are the request headers a cached answer depends on; the
// compression layer adds Accept-Encoding.
const varyOn = "Authorization, X-API-Key, Accept, Accept-Language"

// cacheNote collects, while a data operation runs, the source families its
// answer was built from (dataMeta records them).
type cacheNote struct {
	mu   sync.Mutex
	fams []provenance.Family
}

type cacheNoteKey struct{}

// noteFamilies records the families of a data answer for its ETag and
// Cache-Control.
func noteFamilies(ctx context.Context, fams []provenance.Family) {
	if n, ok := ctx.Value(cacheNoteKey{}).(*cacheNote); ok {
		n.mu.Lock()
		n.fams = append(n.fams, fams...)
		n.mu.Unlock()
	}
}

// familyVersion is one family an answer was read from, at the version read.
type familyVersion struct{ family, version string }

// entry is one answer of the result cache. The body is kept only while the
// answer is younger than its max-age and fits the budget; the ETag and the
// family versions stay while the entry is in the cache, so a matching
// If-None-Match is answered without the handler for as long as no family
// changed.
type entry struct {
	key       string
	etag      string
	families  []familyVersion
	maxAge    time.Duration
	modified  *time.Time
	header    http.Header
	body      []byte
	requestID string
	expires   time.Time
	elem      *list.Element
	// unversioned is set when a family carried no freshness version: nothing
	// can tell when the answer changes, so it gets no ETag, is never stored
	// and is never current; it keeps its max-age.
	unversioned bool
}

// current reports whether every family of e still has the version it was
// read at.
func (e *entry) current(versions map[string]string) bool {
	if e.unversioned {
		return false
	}
	for _, f := range e.families {
		if versions[f.family] != f.version {
			return false
		}
	}
	return true
}

// resultCache is the in-process cache of encoded answers: a byte budget over
// the stored bodies, least recently used dropped first, and single-flight so
// identical concurrent requests cause one read.
type resultCache struct {
	maxBytes, maxEntry int
	mu                 sync.Mutex
	entries            map[string]*entry
	lru                list.List
	bytes              int
	flights            map[string]*flight
}

// flight is one handler run that identical requests wait for.
type flight struct {
	done chan struct{}
	e    *entry // nil when the run produced nothing cacheable
}

func newResultCache(c config.Cache) *resultCache {
	return &resultCache{maxBytes: c.MaxBytes, maxEntry: c.MaxEntryBytes, entries: map[string]*entry{}, flights: map[string]*flight{}}
}

func (c *resultCache) get(key string) *entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e != nil {
		c.lru.MoveToFront(e.elem)
	}
	return e
}

// put stores e under its key, dropping the oldest bodies past the budget.
// An entry keeps its body only when it fits maxEntry; the bodiless rest of
// an entry is small and counts as its key and ETag.
func (c *resultCache) put(e *entry) {
	if len(e.body) > c.maxEntry || c.maxBytes == 0 {
		e.body = nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.entries[e.key]; old != nil {
		c.remove(old)
	}
	e.elem = c.lru.PushFront(e)
	c.entries[e.key] = e
	c.bytes += e.size()
	for c.bytes > c.maxBytes && c.lru.Len() > 0 {
		c.remove(c.lru.Back().Value.(*entry))
	}
}

func (c *resultCache) remove(e *entry) {
	c.lru.Remove(e.elem)
	delete(c.entries, e.key)
	c.bytes -= e.size()
}

func (e *entry) size() int { return len(e.body) + len(e.key) + len(e.etag) }

// join makes the caller the leader of the flight for key, or returns the
// flight already running.
func (c *resultCache) join(key string) (f *flight, leader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.flights[key]; f != nil {
		return f, false
	}
	f = &flight{done: make(chan struct{})}
	c.flights[key] = f
	return f, true
}

func (c *resultCache) land(key string, f *flight, e *entry) {
	if e != nil {
		c.put(e)
	}
	c.mu.Lock()
	delete(c.flights, key)
	c.mu.Unlock()
	f.e = e
	close(f.done)
}

// versionCache holds the last read of every family's freshness version for
// cache.versions_ttl. It reads only when a request asks and the last read is
// older; while one request reads, the others wait for that read, so a burst
// of identical requests neither reads the versions twice nor falls through
// to the operation.
type versionCache struct {
	reader  VersionReader
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	at      time.Time
	vs      map[string]string
	failed  bool          // the last read failed
	reading chan struct{} // closed when the running read ends; nil when none runs
}

// get returns versions not older than the TTL, or ok false.
func (v *versionCache) get(ctx context.Context) (map[string]string, bool) {
	if v.reader == nil {
		return nil, false
	}
	v.mu.Lock()
	if v.vs != nil && v.now().Sub(v.at) < v.ttl {
		vs := v.vs
		v.mu.Unlock()
		return vs, true
	}
	if wait := v.reading; wait != nil {
		v.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, false
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.vs, !v.failed
	}
	done := make(chan struct{})
	v.reading = done
	v.mu.Unlock()
	vs, err := v.reader.FreshnessVersions(ctx)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reading, v.failed = nil, err != nil
	close(done)
	if err != nil {
		return nil, false
	}
	v.vs, v.at = vs, v.now()
	return vs, true
}

// humaContext names the embedded context: a field called Context would hide
// the interface's Context method.
type humaContext = huma.Context

// bufferedContext holds an operation's answer back so the cache can judge it
// before it is sent.
type bufferedContext struct {
	humaContext
	status int
	header http.Header
	body   bytes.Buffer
}

func (b *bufferedContext) SetStatus(code int)           { b.status = code }
func (b *bufferedContext) Status() int                  { return b.status }
func (b *bufferedContext) SetHeader(name, value string) { b.header.Set(name, value) }
func (b *bufferedContext) AppendHeader(name, value string) {
	b.header.Add(name, value)
}
func (b *bufferedContext) BodyWriter() io.Writer { return &b.body }

// cacheable reports whether op is answered through the cache: the data
// operations, which carry a scope.
func cacheable(op *huma.Operation) bool {
	_, ok := op.Metadata[scopeExtension]
	return ok
}

// cache is the HTTP cache of the data operations (Performance-and-Caching).
// It runs after the key gates, so a 304 counts against the quota and is
// metered, and before the operation, so a 304 or a stored answer takes no
// storage permit.
func (s *server) cache(ctx huma.Context, next func(huma.Context)) {
	if !s.cfg.Cache.Enabled || !cacheable(ctx.Operation()) {
		next(ctx)
		return
	}
	key := cacheKey(ctx)
	inm := ctx.Header("If-None-Match")
	if e := s.results.get(key); e != nil {
		if vs, ok := s.versions.get(ctx.Context()); ok && e.current(vs) {
			if etagMatch(inm, e.etag) {
				s.sendNotModified(ctx, e)
				return
			}
			if e.body != nil && s.now().Before(e.expires) {
				s.sendEntry(ctx, e, inm)
				return
			}
		}
	}
	f, leader := s.results.join(key)
	if !leader {
		select {
		case <-f.done:
			if f.e != nil && f.e.body != nil {
				s.sendEntry(ctx, f.e, inm)
				return
			}
		case <-ctx.Context().Done():
		}
		s.run(ctx, next, key, inm, nil)
		return
	}
	s.run(ctx, next, key, inm, f)
}

// run answers through the operation and, when the answer is a 200 built from
// source families, gives it an ETag and Cache-Control and stores it. f is the
// flight the run leads, if any.
func (s *server) run(ctx huma.Context, next func(huma.Context), key, inm string, f *flight) {
	note := &cacheNote{}
	b := &bufferedContext{humaContext: huma.WithValue(ctx, cacheNoteKey{}, note), header: http.Header{}}
	var stored *entry
	defer func() {
		if f != nil {
			s.results.land(key, f, stored)
		}
	}()
	next(b)
	if b.status != http.StatusOK || len(note.fams) == 0 {
		send(ctx, b.status, b.header, b.body.Bytes())
		return
	}
	e := s.newEntry(ctx.Context(), key, note.fams, b)
	if !e.unversioned {
		stored = e
	}
	if !e.unversioned && etagMatch(inm, e.etag) {
		s.sendNotModified(ctx, e)
		return
	}
	setCacheHeaders(b.header, e)
	send(ctx, b.status, b.header, b.body.Bytes())
}

func (s *server) newEntry(ctx context.Context, key string, fams []provenance.Family, b *bufferedContext) *entry {
	e := &entry{key: key, header: b.header.Clone(), body: slices.Clone(b.body.Bytes()), requestID: RequestIDFrom(ctx)}
	seen := map[string]bool{}
	for _, f := range fams {
		if seen[f.Source.Family] {
			continue
		}
		seen[f.Source.Family] = true
		e.families = append(e.families, familyVersion{family: f.Source.Family, version: f.Version})
		if f.Version == "" {
			e.unversioned = true
		}
		age := maxAge(s.cfg.Cache.MaxAge, f.Source.Domain)
		if e.maxAge == 0 || age < e.maxAge {
			e.maxAge = age
		}
		if f.DataAsOf != nil && (e.modified == nil || f.DataAsOf.After(*e.modified)) {
			e.modified = f.DataAsOf
		}
	}
	slices.SortFunc(e.families, func(a, b familyVersion) int { return strings.Compare(a.family, b.family) })
	h := sha256.New()
	h.Write([]byte(key))
	for _, f := range e.families {
		h.Write([]byte{0})
		h.Write([]byte(f.family))
		h.Write([]byte{0})
		h.Write([]byte(f.version))
	}
	e.etag = `W/"` + base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:18]) + `"`
	e.expires = s.now().Add(e.maxAge)
	return e
}

// maxAge is the Cache-Control max-age of a domain's answers.
func maxAge(m config.CacheMaxAge, domain string) time.Duration {
	switch domain {
	case "places":
		return m.Places
	case "roads":
		return m.Roads
	case "energy":
		return m.Energy
	case "parking":
		return m.Parking
	case "transit":
		return m.Transit
	case "accessibility":
		return m.Accessibility
	case "weather":
		return m.Weather
	case "rules":
		return m.Rules
	default:
		return m.Default
	}
}

// setCacheHeaders puts the validator and the freshness of e on h, replacing
// the operation's own Cache-Control.
func setCacheHeaders(h http.Header, e *entry) {
	if !e.unversioned {
		h.Set("ETag", e.etag)
	}
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(int(e.maxAge.Seconds())))
	h.Set("Vary", varyOn)
	if e.modified != nil {
		h.Set("Last-Modified", e.modified.UTC().Format(http.TimeFormat))
	}
}

func (s *server) sendNotModified(ctx huma.Context, e *entry) {
	h := http.Header{}
	setCacheHeaders(h, e)
	send(ctx, http.StatusNotModified, h, nil)
}

// sendEntry replays a stored answer under this request's id, or a 304 when
// it matches inm.
func (s *server) sendEntry(ctx huma.Context, e *entry, inm string) {
	if etagMatch(inm, e.etag) {
		s.sendNotModified(ctx, e)
		return
	}
	h := e.header.Clone()
	setCacheHeaders(h, e)
	body := e.body
	if id := RequestIDFrom(ctx.Context()); id != e.requestID {
		body = bytes.Replace(body, []byte(`"requestId":"`+e.requestID+`"`), []byte(`"requestId":"`+id+`"`), 1)
	}
	send(ctx, http.StatusOK, h, body)
}

// send writes an answer: the headers before the status, which the adapter
// sends at once.
func send(ctx huma.Context, status int, h http.Header, body []byte) {
	for name, vs := range h {
		for i, v := range vs {
			// Vary adds to what the compression layer said.
			if i == 0 && name != "Vary" {
				ctx.SetHeader(name, v)
			} else {
				ctx.AppendHeader(name, v)
			}
		}
	}
	if status == 0 {
		status = http.StatusOK
	}
	ctx.SetStatus(status)
	if len(body) > 0 {
		_, _ = ctx.BodyWriter().Write(body)
	}
}

// etagMatch is the weak comparison of If-None-Match (RFC 9110 §13.1.2).
func etagMatch(inm, etag string) bool {
	if inm == "" {
		return false
	}
	want := strings.TrimPrefix(etag, "W/")
	for tag := range strings.SplitSeq(inm, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == want {
			return true
		}
	}
	return false
}

// cacheKey is the input every answer of an operation depends on: the
// operation, its path and normalised query, the representation asked for and
// what the key may read. Two keys share an entry only when the licence gate
// would answer them alike, so no answer crosses permissions.
func cacheKey(ctx huma.Context) string {
	u := ctx.URL()
	h := sha256.New()
	for _, part := range []string{
		ctx.Operation().OperationID, u.Path, u.Query().Encode(),
		ctx.Header("Accept"), ctx.Header("Accept-Language"),
		permissions(principalFrom(ctx.Context()).Key),
	} {
		h.Write([]byte(strconv.Itoa(len(part))))
		h.Write([]byte{':'})
		h.Write([]byte(part))
	}
	return string(h.Sum(nil))
}

// permissions is everything of a key the licence gate reads.
func permissions(k access.Record) string {
	sources := slices.Sorted(slices.Values(k.Plan.Sources))
	scopes := make([]string, len(k.Plan.Scopes))
	for i, sc := range k.Plan.Scopes {
		scopes[i] = string(sc)
	}
	slices.Sort(scopes)
	accepted := make([]string, len(k.Accepted))
	for i, a := range k.Accepted {
		accepted[i] = a.Source + "@" + a.Version
	}
	slices.Sort(accepted)
	return strings.Join([]string{
		string(k.Environment), string(k.Plan.UseClass),
		strings.Join(sources, ","), strings.Join(scopes, ","), strings.Join(accepted, ","),
	}, "|")
}
