package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// docAPI is the part of huma.API documentCaching reads.
type docAPI struct {
	huma.API
	doc *huma.OpenAPI
}

func (d docAPI) OpenAPI() *huma.OpenAPI { return d.doc }

func TestDocumentCachingTouchesOnlyDataGets(t *testing.T) {
	scoped := map[string]any{scopeExtension: "energy"}
	data := &huma.Operation{Metadata: scoped, Responses: map[string]*huma.Response{
		"200": {Headers: map[string]*huma.Param{"X-Count": {}}}}}
	bare := &huma.Operation{Metadata: scoped, Responses: map[string]*huma.Response{"200": {}}}
	noOK := &huma.Operation{Metadata: scoped, Responses: map[string]*huma.Response{}}
	plain := &huma.Operation{Responses: map[string]*huma.Response{"200": {}}}
	post := &huma.Operation{Metadata: scoped, Responses: map[string]*huma.Response{}}
	documentCaching(docAPI{doc: &huma.OpenAPI{Paths: map[string]*huma.PathItem{
		"/a": {Get: data}, "/b": {Get: bare}, "/c": {Get: noOK}, "/d": {Get: plain}, "/e": {Post: post},
	}}})
	if data.Responses["200"].Headers["ETag"] == nil || data.Responses["200"].Headers["X-Count"] == nil ||
		bare.Responses["200"].Headers["Cache-Control"] == nil || data.Responses["304"] == nil ||
		noOK.Responses["304"] == nil || noOK.Responses["200"] != nil {
		t.Fatal("a data GET is not documented as cacheable")
	}
	if plain.Responses["304"] != nil || plain.Responses["200"].Headers != nil || post.Responses["304"] != nil {
		t.Fatal("an operation without a scope, or not a GET, was documented as cacheable")
	}
}

// sendContext records what send writes.
type sendContext struct {
	humaContext
	header http.Header
	status int
	body   bytes.Buffer
}

func (s *sendContext) SetHeader(n, v string)    { s.header.Set(n, v) }
func (s *sendContext) AppendHeader(n, v string) { s.header.Add(n, v) }
func (s *sendContext) SetStatus(c int)          { s.status = c }
func (s *sendContext) BodyWriter() io.Writer    { return &s.body }

func TestSendWritesHeadersThenStatus(t *testing.T) {
	ctx := &sendContext{header: http.Header{"Vary": {"Accept-Encoding"}}}
	send(ctx, 0, http.Header{"A": {"1", "2"}, "Vary": {"Accept"}}, nil)
	if ctx.status != http.StatusOK || ctx.header.Values("A")[1] != "2" ||
		strings.Join(ctx.header.Values("Vary"), ",") != "Accept-Encoding,Accept" || ctx.body.Len() != 0 {
		t.Fatalf("send: %d %v", ctx.status, ctx.header)
	}
	b := &bufferedContext{header: http.Header{}}
	b.SetStatus(http.StatusTeapot)
	if b.Status() != http.StatusTeapot {
		t.Fatal("the buffered status is not read back")
	}
}

func TestAnAnswerReadTwiceFromOneFamilyCountsItOnce(t *testing.T) {
	s := &server{cfg: config.Defaults(), now: time.Now}
	f := provenance.Family{Source: access.Source{Family: "f", Domain: "energy"}, Version: "1"}
	vs := provenance.Versions{Families: map[string]string{"f": "1"}, Register: "r"}
	e := s.newEntry(context.Background(), "k", &cacheNote{fams: []provenance.Family{f, f}}, vs, true, &bufferedContext{header: http.Header{}})
	if len(e.families) != 1 || e.maxAge != time.Minute || e.unversioned {
		t.Fatalf("families %v, max-age %s", e.families, e.maxAge)
	}
}

func TestAnEntryIsTaggedWithTheVersionsReadBeforeIt(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	s := &server{cfg: config.Defaults(), now: time.Now}
	// The operation read newer data (version 2) than the versions read
	// before it (version 1): the entry carries 1, so the next check, which
	// reads 2, finds it not current.
	f := provenance.Family{Source: access.Source{Family: "f", Domain: "energy"}, Version: "2"}
	before := provenance.Versions{Families: map[string]string{"f": "1"}}
	e := s.newEntry(context.Background(), "k", &cacheNote{fams: []provenance.Family{f}}, before, true, &bufferedContext{header: http.Header{}})
	if e.families[0].version != "1" || e.current(provenance.Versions{Families: map[string]string{"f": "2"}}, time.Now()) {
		t.Fatalf("tagged %v", e.families)
	}
	if e := s.newEntry(context.Background(), "k", &cacheNote{fams: []provenance.Family{f}}, provenance.Versions{}, false, &bufferedContext{header: http.Header{}}); !e.unversioned {
		t.Fatal("an answer with no versions read before it was versioned from its own read")
	}
}

func TestAFreeMinAnswerEndsWhenAKeptReportAges(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-PARK-001")
	observed := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)
	a := features.Answer[parking.Site]{
		Items: []parking.Site{
			{ID: "p1", Occupancy: &parking.Occupancy{ObservedAt: observed, Family: "f"}},
			{ID: "p2", Occupancy: &parking.Occupancy{ObservedAt: observed.Add(time.Minute), Family: "f"}},
		},
		Families: []provenance.Family{{Source: access.Source{Family: "f"}, StaleAfter: 10 * time.Minute}},
	}
	note := &cacheNote{}
	ctx := context.WithValue(context.Background(), cacheNoteKey{}, note)
	noteFreeMin(ctx, a, 1)
	if want := observed.Add(10*time.Minute + time.Microsecond); !note.until.Equal(want) || len(note.shown) != 2 {
		t.Fatalf("until %s (want %s), shown %v", note.until, want, note.shown)
	}
	none := &cacheNote{}
	noteFreeMin(context.WithValue(context.Background(), cacheNoteKey{}, none), a, 0)
	if !none.until.IsZero() || len(none.shown) != 0 {
		t.Fatal("an answer without free_min does not change with the clock")
	}
}
