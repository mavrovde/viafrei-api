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

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
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
	e := s.newEntry(context.Background(), "k", []provenance.Family{f, f}, &bufferedContext{header: http.Header{}})
	if len(e.families) != 1 || e.maxAge != time.Minute {
		t.Fatalf("families %v, max-age %s", e.families, e.maxAge)
	}
}
