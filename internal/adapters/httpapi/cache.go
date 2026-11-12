package httpapi

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

// VersionReader reads, in one snapshot, the freshness version of every source
// family and the fingerprint of the licence register.
type VersionReader interface {
	FreshnessVersions(ctx context.Context) (provenance.Versions, error)
}

// varyOn are the request headers a cached answer depends on; the
// compression layer adds Accept-Encoding.
const varyOn = "Authorization, X-API-Key, Accept, Accept-Language"

// cacheNote collects, while a data operation runs, what its answer depends
// on besides the request: the source families it was built from (dataMeta
// records them), the earliest instant it changes with the clock alone, what
// it shows that the clock changes, and whether it is degraded.
type cacheNote struct {
	mu   sync.Mutex
	fams []provenance.Family
	// until is the earliest instant the answer would change with no family
	// changing its version: a family turning stale, a charging report
	// ageing past its limit, a parking report leaving a free_min filter.
	// Zero when nothing in it changes with the clock.
	until time.Time
	// shown is what the answer states that changes with the clock (each
	// family's stale flag, each charge point's shown state, the sites a
	// filter on live reports kept); it is part of the ETag, so an answer
	// built after until is never validated by one built before.
	shown []string
	// degraded is set when a part of the answer is missing for a reason
	// that may pass (a part timed out or was unavailable): such an answer
	// is never stored and carries no ETag.
	degraded bool
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

// noteChangesAt records that the answer changes at t with the clock alone.
func noteChangesAt(ctx context.Context, t time.Time) {
	if n, ok := ctx.Value(cacheNoteKey{}).(*cacheNote); ok {
		n.mu.Lock()
		if n.until.IsZero() || t.Before(n.until) {
			n.until = t
		}
		n.mu.Unlock()
	}
}

// noteShown records what the answer states that the clock changes.
func noteShown(ctx context.Context, shown ...string) {
	if n, ok := ctx.Value(cacheNoteKey{}).(*cacheNote); ok {
		n.mu.Lock()
		n.shown = append(n.shown, shown...)
		n.mu.Unlock()
	}
}

// noteDegraded records that a part of the answer is missing for a reason
// that may pass.
func noteDegraded(ctx context.Context) {
	if n, ok := ctx.Value(cacheNoteKey{}).(*cacheNote); ok {
		n.mu.Lock()
		n.degraded = true
		n.mu.Unlock()
	}
}

// familyVersion is one family an answer was read from, at the version read.
type familyVersion struct{ family, version string }

// entry is one answer of the result cache. Its body is kept while it is
// younger than its max-age and fits the budget; get drops an expired body.
// The ETag, the family versions and the register fingerprint stay while the
// entry is in the cache, so a matching If-None-Match is answered without the
// handler for as long as nothing the answer rests on changed. A stored entry
// is never written again: get replaces it with a copy, so a request still
// sending it reads what it was given.
type entry struct {
	key      string
	etag     string
	families []familyVersion
	// register is the licence register's fingerprint the answer was read
	// under: the gate's decision, the attribution and the notes rest on it.
	register  string
	until     time.Time // cacheNote.until
	maxAge    time.Duration
	modified  *time.Time
	header    http.Header // nil when the entry keeps no body
	body      []byte
	requestID string
	// built is the instant the answer was read: a replay says how long ago
	// in its Age header (sendEntry).
	built   time.Time
	expires time.Time
	elem    *list.Element
	// unversioned is set when nothing can tell when the answer changes: a
	// family carried no freshness version, the versions could not be read
	// before the answer, or the answer is degraded. It gets no ETag, is
	// never stored, is never current and is sent with no-store, so no cache
	// downstream keeps it either.
	unversioned bool
}

// current reports whether e still answers at now under versions: every
// family has the version it was read at, the register is the one it was read
// under, and the clock has not reached the instant it changes.
func (e *entry) current(versions provenance.Versions, now time.Time) bool {
	if e.unversioned || versions.Register != e.register {
		return false
	}
	if !e.until.IsZero() && !now.Before(e.until) {
		return false
	}
	for _, f := range e.families {
		if versions.Families[f.family] != f.version {
			return false
		}
	}
	return true
}

// resultCache is the in-process cache of encoded answers: a byte budget over
// the entries and a cap on their number, least recently used dropped first,
// and single-flight so identical concurrent requests cause one read.
type resultCache struct {
	maxBytes, maxEntry, maxEntries int
	mu                             sync.Mutex
	entries                        map[string]*entry
	lru                            list.List
	bytes                          int
	flights                        map[string]*flight
}

// flight is one handler run that identical requests wait for. Its answer is
// handed to them whether or not it is stored: an unversioned answer, or one
// too large to keep, still answers every request that waited for it.
type flight struct {
	done chan struct{}
	e    *entry // the leader's answer; nil when it had no 200 from source families
}

func newResultCache(c config.Cache) *resultCache {
	return &resultCache{
		maxBytes: c.MaxBytes, maxEntry: c.MaxEntryBytes, maxEntries: c.MaxEntries,
		entries: map[string]*entry{}, flights: map[string]*flight{},
	}
}

// get returns the entry of key, the most recently used from now on. An entry
// whose body has expired at now is replaced by its bodiless copy first.
func (c *resultCache) get(key string, now time.Time) *entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e == nil {
		return nil
	}
	if e.body != nil && !now.Before(e.expires) {
		bare := *e
		bare.body, bare.header = nil, nil
		c.remove(e)
		c.insert(&bare)
		return &bare
	}
	c.lru.MoveToFront(e.elem)
	return e
}

// put stores e under its key, dropping the least recently used entries past
// the budget or the cap. An entry keeps its body (and the headers it is
// replayed with) only when the body fits maxEntry; otherwise a bodiless copy
// is stored, so e itself still answers the requests waiting on its flight.
func (c *resultCache) put(e *entry) {
	if e.body == nil || len(e.body) > c.maxEntry || c.maxBytes == 0 {
		bare := *e
		bare.body, bare.header = nil, nil
		e = &bare
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.entries[e.key]; old != nil {
		c.remove(old)
	}
	c.insert(e)
}

func (c *resultCache) insert(e *entry) {
	e.elem = c.lru.PushFront(e)
	c.entries[e.key] = e
	c.bytes += e.size()
	for (c.bytes > c.maxBytes || c.lru.Len() > c.maxEntries) && c.lru.Len() > 0 {
		c.remove(c.lru.Back().Value.(*entry))
	}
}

func (c *resultCache) remove(e *entry) {
	c.lru.Remove(e.elem)
	delete(c.entries, e.key)
	c.bytes -= e.size()
}

// entryOverhead is what an entry holds besides its strings and body: the
// struct, its list element and its map slot, rounded up.
const entryOverhead = 256

// size is what e holds in memory, as the budget counts it: every string,
// the stored headers and the body, plus the fixed overhead.
func (e *entry) size() int {
	n := entryOverhead + len(e.key) + len(e.etag) + len(e.register) + len(e.requestID) + len(e.body)
	for _, f := range e.families {
		n += len(f.family) + len(f.version)
	}
	for name, vs := range e.header {
		n += len(name)
		for _, v := range vs {
			n += len(v)
		}
	}
	return n
}

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

// land ends the flight for key: stored, when set, is kept in the cache, and
// answer is handed to the requests that waited.
func (c *resultCache) land(key string, f *flight, stored, answer *entry) {
	if stored != nil {
		c.put(stored)
	}
	c.mu.Lock()
	delete(c.flights, key)
	c.mu.Unlock()
	f.e = answer
	close(f.done)
}

// versionCache holds the last read of the family versions and the register
// fingerprint for cache.versions_ttl. It reads only when a request asks and
// the last read is older; while one request reads, the others wait for that
// read, so a burst of identical requests neither reads the versions twice nor
// falls through to the operation.
type versionCache struct {
	reader  VersionReader
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	at      time.Time
	vs      provenance.Versions
	read    bool          // vs holds a read
	failed  bool          // the last read failed
	reading chan struct{} // closed when the running read ends; nil when none runs
}

// get returns versions not older than the TTL, or ok false. Every value it
// returns was read before it returns, so versions taken before an operation
// runs are never newer than the data the operation reads.
func (v *versionCache) get(ctx context.Context) (provenance.Versions, bool) {
	if v.reader == nil {
		return provenance.Versions{}, false
	}
	v.mu.Lock()
	if v.read && v.now().Sub(v.at) < v.ttl {
		vs := v.vs
		v.mu.Unlock()
		return vs, true
	}
	if wait := v.reading; wait != nil {
		v.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return provenance.Versions{}, false
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.vs, v.read && !v.failed
	}
	done := make(chan struct{})
	v.reading = done
	v.mu.Unlock()
	rctx, cancel := serviceRead(ctx)
	vs, err := v.reader.FreshnessVersions(rctx)
	cancel()
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reading, v.failed = nil, err != nil
	close(done)
	if err != nil {
		return provenance.Versions{}, false
	}
	v.vs, v.at, v.read = vs, v.now(), true
	return vs, true
}

// serviceRead is the context of a read the service makes for every request,
// not for the one that happened to ask: it carries none of that request's
// values, so no caller rides on it and it is admitted as the service's own
// read, never in the asking caller's tier (a shed bulk caller would otherwise
// fail the read for everyone waiting on it). It ends at the asking request's
// deadline at the latest.
func serviceRead(ctx context.Context) (context.Context, context.CancelFunc) {
	if d, ok := ctx.Deadline(); ok {
		return context.WithDeadline(context.Background(), d)
	}
	return context.WithCancel(context.Background())
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
// metered, and before the operation, so a 304 or a stored answer runs no
// feature read. Both read the versions, which the service reads as its own
// read once per cache.versions_ttl (serviceRead), never under the caller's
// tier. The versions are read before the operation runs and its answer is
// tagged with them: an answer is never tagged with versions newer than its
// data.
func (s *server) cache(ctx huma.Context, next func(huma.Context)) {
	if !s.cfg.Cache.Enabled || !cacheable(ctx.Operation()) {
		next(ctx)
		return
	}
	key := cacheKey(ctx)
	inm := ctx.Header("If-None-Match")
	vs, versioned := s.versions.get(ctx.Context())
	now := s.now()
	if e := s.results.get(key, now); e != nil && versioned && e.current(vs, now) {
		if etagMatch(inm, e.etag) {
			s.sendNotModified(ctx, e)
			return
		}
		if e.body != nil {
			s.sendEntry(ctx, e, inm)
			return
		}
	}
	f, leader := s.results.join(key)
	if !leader {
		select {
		case <-f.done:
			if f.e != nil {
				s.sendEntry(ctx, f.e, inm)
				return
			}
		case <-ctx.Context().Done():
			// The request ended while waiting: it runs no read of its own.
			sendProblem(ctx, newProblem(ctx.Context(), SlugDeadlineExhausted, "The request ended while an identical request was being answered.", 1))
			return
		}
		s.run(ctx, next, key, inm, nil, vs, versioned)
		return
	}
	s.run(ctx, next, key, inm, f, vs, versioned)
}

// sendProblem writes a problem from the cache layer.
func sendProblem(ctx huma.Context, p *Problem) {
	h := http.Header{}
	for k, vs := range p.headers {
		h[k] = vs
	}
	h.Set("Content-Type", ProblemContentType)
	h.Set("Cache-Control", "no-store")
	body, _ := json.Marshal(p)
	send(ctx, p.Status, h, body)
}

// run answers through the operation and, when the answer is a 200 built from
// source families, gives it an ETag and Cache-Control and stores it. f is the
// flight the run leads, if any, and every such answer is handed to the
// requests waiting on it, stored or not; vs are the versions read before it,
// when versioned.
func (s *server) run(ctx huma.Context, next func(huma.Context), key, inm string, f *flight, vs provenance.Versions, versioned bool) {
	note := &cacheNote{}
	b := &bufferedContext{humaContext: huma.WithValue(ctx, cacheNoteKey{}, note), header: http.Header{}}
	var stored, answer *entry
	defer func() {
		if f != nil {
			s.results.land(key, f, stored, answer)
		}
	}()
	next(b)
	if b.status != http.StatusOK || len(note.fams) == 0 {
		send(ctx, b.status, b.header, b.body.Bytes())
		return
	}
	e := s.newEntry(ctx.Context(), key, note, vs, versioned, b)
	answer = e
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

// newEntry builds the entry of an answer. Its family versions and register
// fingerprint are vs, read before the operation ran; without them nothing can
// tell when the answer changes and the entry is unversioned.
func (s *server) newEntry(ctx context.Context, key string, note *cacheNote, vs provenance.Versions, versioned bool, b *bufferedContext) *entry {
	e := &entry{
		key: key, header: b.header.Clone(), body: slices.Clone(b.body.Bytes()), requestID: RequestIDFrom(ctx),
		register: vs.Register, until: note.until, unversioned: !versioned || note.degraded,
	}
	seen := map[string]bool{}
	for _, f := range note.fams {
		if seen[f.Source.Family] {
			continue
		}
		seen[f.Source.Family] = true
		version := vs.Families[f.Source.Family]
		e.families = append(e.families, familyVersion{family: f.Source.Family, version: version})
		if version == "" {
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
	now := s.now()
	if !e.until.IsZero() && e.until.Sub(now) < e.maxAge {
		e.maxAge = max(e.until.Sub(now), 0)
	}
	h := sha256.New()
	h.Write([]byte(key))
	for _, f := range e.families {
		h.Write([]byte{0})
		h.Write([]byte(f.family))
		h.Write([]byte{0})
		h.Write([]byte(f.version))
	}
	h.Write([]byte{1})
	h.Write([]byte(e.register))
	for _, v := range note.shown {
		h.Write([]byte{2})
		h.Write([]byte(v))
	}
	e.etag = `W/"` + base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:18]) + `"`
	e.built, e.expires = now, now.Add(e.maxAge)
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
// the operation's own Cache-Control. An unversioned answer gets no validator
// and no-store: nothing can tell when it changes, so no one may keep it.
func setCacheHeaders(h http.Header, e *entry) {
	if e.unversioned {
		h.Set("Cache-Control", "no-store")
	} else {
		h.Set("ETag", e.etag)
		h.Set("Cache-Control", "private, max-age="+strconv.Itoa(int(e.maxAge.Seconds())))
	}
	h.Set("Vary", varyOn)
	if e.modified != nil {
		h.Set("Last-Modified", e.modified.UTC().Format(http.TimeFormat))
	}
}

// sendNotModified answers a matching validator. It carries no Age: a 304
// tells the client its copy is current now, so its max-age counts from this
// answer. An Age counted from the read the entry was built by would pass
// max-age once the stored body has expired and make every request a
// revalidation.
func (s *server) sendNotModified(ctx huma.Context, e *entry) {
	h := http.Header{}
	setCacheHeaders(h, e)
	send(ctx, http.StatusNotModified, h, nil)
}

// age is the Age header (RFC 9111, section 5.1) of a replay of e: the whole
// seconds since its answer was read. The replayed body keeps the dataAsOf,
// ageS and generatedAt of that read, so a client adds Age to ageS for the
// data's age now, and Cache-Control max-age counts from the read, not from
// the replay.
func (s *server) age(e *entry) string {
	return strconv.FormatInt(int64(max(s.now().Sub(e.built), 0)/time.Second), 10)
}

// sendEntry replays a stored answer under this request's id, with the Age
// of the read it replays, or a 304 when it matches inm.
func (s *server) sendEntry(ctx huma.Context, e *entry, inm string) {
	if !e.unversioned && etagMatch(inm, e.etag) {
		s.sendNotModified(ctx, e)
		return
	}
	h := e.header.Clone()
	setCacheHeaders(h, e)
	h.Set("Age", s.age(e))
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
