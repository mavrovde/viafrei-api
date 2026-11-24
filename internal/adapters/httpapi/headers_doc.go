package httpapi

import "github.com/danielgtaylor/huma/v2"

// The headers most answers repeat are defined once, under
// components.headers, and every answer refers to its definition. Copied
// into every answer, seven definitions stood in about 900 places, a third
// of the document, and marshalling the copies was most of what the first
// /v1/openapi.json of a process costs (TST-012).
var sharedHeaderDocs = map[string]func() *huma.Param{
	"RateLimit":        func() *huma.Param { return stringHeader(rateLimitDoc) },
	"RateLimit-Policy": func() *huma.Param { return stringHeader(rateLimitDoc) },
	"ETag":             func() *huma.Param { return stringHeader(cacheDoc) },
	"Cache-Control":    func() *huma.Param { return stringHeader(cacheDoc) },
	"Vary":             func() *huma.Param { return stringHeader(cacheDoc) },
	"Last-Modified":    func() *huma.Param { return stringHeader(cacheDoc) },
	"Age": func() *huma.Param {
		return &huma.Param{Description: ageDoc, Schema: &huma.Schema{Type: huma.TypeInteger, Minimum: new(float64)}}
	},
}

func stringHeader(doc string) *huma.Param {
	return &huma.Param{Description: doc, Schema: &huma.Schema{Type: huma.TypeString}}
}

// sharedHeader refers to the shared definition of the header name, one of
// sharedHeaderDocs.
func sharedHeader(name string) *huma.Param {
	return &huma.Param{Ref: "#/components/headers/" + name}
}

// documentSharedHeaders defines the shared headers under components.headers.
func documentSharedHeaders(api huma.API) {
	c := api.OpenAPI().Components
	if c.Headers == nil {
		c.Headers = map[string]*huma.Header{}
	}
	for name, def := range sharedHeaderDocs {
		c.Headers[name] = def()
	}
}
