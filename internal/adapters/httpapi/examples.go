package httpapi

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/api"
)

// The documentation members of an operation (docs/examples.md). The docs
// lint (internal/testkit/docslint) requires them on every operation.
const (
	// CodeSamplesExtension holds the example request as a curl command, in
	// the shape Redoc renders.
	CodeSamplesExtension = "x-codeSamples"
	// AttributionExtension says which sources an operation answers from and
	// how their attribution reaches the client.
	AttributionExtension = "x-attribution"
	// ExampleRequestExtension is the example request's target, path and
	// query, for tools that read the document.
	ExampleRequestExtension = "x-example-request"
)

// loadExamples reads the examples the document carries, once per process:
// every handler build attaches them, and reading them again each time cost
// tests that build many handlers their time bound. Tests replace it.
var loadExamples = sync.OnceValues(func() (map[string]api.Example, error) {
	return api.ReadExamples(api.Examples)
})

// CodeSample is one entry of x-codeSamples.
type CodeSample struct {
	Lang   string `json:"lang"`
	Label  string `json:"label"`
	Source string `json:"source"`
}

// curlSample is the example request as a curl command against base; a keyed
// operation presents the key from the environment, never a literal one.
func curlSample(base, target string, keyed bool) string {
	cmd := "curl -sS " + strconv.Quote(base+target)
	if keyed {
		cmd += ` \` + "\n  -H \"Authorization: Bearer $VIAFREI_API_KEY\""
	}
	return cmd
}

// documentExamples attaches each operation's example: the request as
// x-codeSamples and x-example-request, the answer as the example of its
// status, and the attribution rule as x-attribution. An operation without an
// example file is left as it is, so the docs lint names it; an example whose
// status the operation does not document as JSON is an error.
func documentExamples(oapi *huma.OpenAPI, base string) error {
	examples, err := loadExamples()
	if err != nil {
		return err
	}
	for _, p := range slices.Sorted(maps.Keys(oapi.Paths)) {
		for _, op := range pathOperations(oapi.Paths[p]) {
			e, ok := examples[op.OperationID]
			if !ok {
				continue
			}
			resp := op.Responses[strconv.Itoa(e.Status)]
			if resp == nil || resp.Content["application/json"] == nil {
				return fmt.Errorf("examples/%s.json: status %d is not documented as application/json for %s", op.OperationID, e.Status, op.OperationID)
			}
			if op.Extensions == nil {
				op.Extensions = map[string]any{}
			}
			op.Extensions[CodeSamplesExtension] = []CodeSample{{Lang: "Shell", Label: "curl", Source: curlSample(base, e.Request, len(op.Security) > 0)}}
			op.Extensions[ExampleRequestExtension] = e.Request
			op.Extensions[AttributionExtension] = e.Attribution
			resp.Content["application/json"].Examples = map[string]*huma.Example{
				"example": {Summary: e.Title, Value: e.Response},
			}
		}
	}
	return nil
}

// pathOperations returns the operations of a path item in method order.
func pathOperations(p *huma.PathItem) []*huma.Operation {
	var out []*huma.Operation
	for _, op := range []*huma.Operation{p.Get, p.Put, p.Post, p.Delete, p.Options, p.Head, p.Patch, p.Trace} {
		if op != nil {
			out = append(out, op)
		}
	}
	return out
}
