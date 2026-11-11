package httpapi

import (
	"maps"

	"github.com/danielgtaylor/huma/v2"
)

// cacheDoc says what the caching headers of a data answer mean.
const cacheDoc = "Sent on a data answer built from source families: the ETag changes with the asked input, what the key may read and the version of every family read; Cache-Control is private, with the smallest max-age of the families' domains."

// cacheHeaders documents the validator and freshness headers.
func cacheHeaders() map[string]*huma.Param {
	out := map[string]*huma.Param{}
	for _, name := range []string{"ETag", "Cache-Control", "Vary", "Last-Modified"} {
		out[name] = &huma.Param{Description: cacheDoc, Schema: &huma.Schema{Type: huma.TypeString}}
	}
	return out
}

// documentCaching adds the validator and freshness headers to the 200 of
// every data operation, and the 304 it answers a matching If-None-Match
// with.
func documentCaching(api huma.API) {
	for _, item := range api.OpenAPI().Paths {
		op := item.Get
		if op == nil || !cacheable(op) {
			continue
		}
		if ok := op.Responses["200"]; ok != nil {
			if ok.Headers == nil {
				ok.Headers = map[string]*huma.Param{}
			}
			maps.Copy(ok.Headers, cacheHeaders())
		}
		op.Responses["304"] = &huma.Response{
			Description: "Not modified: If-None-Match names the answer's current ETag. Counts against the quota like any answer.",
			Headers:     cacheHeaders(),
		}
	}
}
