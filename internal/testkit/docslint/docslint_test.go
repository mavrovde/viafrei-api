package docslint_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/docslint"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func committed(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestDocsLint is the docs lint over the committed document (make
// docs-lint): every operation has a description, an example request and
// answer, and its sources' attribution.
func TestDocsLint(t *testing.T) {
	req.Covers(t, "DX-003", "LIC-007")
	r, err := docslint.Lint(committed(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Findings {
		t.Error(f)
	}
	t.Logf("docs lint read %d operation(s), %d finding(s)", r.Operations, len(r.Findings))
}

// mutate removes or changes one member of one operation of the committed
// document and returns the result.
func mutate(t *testing.T, path, method string, change func(op map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(committed(t), &doc); err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)[path].(map[string]any)[method].(map[string]any)
	change(op)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func okExample(op map[string]any) map[string]any {
	return op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
}

// TestDocsLintCanSayNo: each missing piece fails the lint and is named.
func TestDocsLintCanSayNo(t *testing.T) {
	req.Covers(t, "DX-003")
	const data, svc = "/v1/charging-options", "/healthz"
	firstAttribution := func(op map[string]any) map[string]any {
		ex := okExample(op)["examples"].(map[string]any)["example"].(map[string]any)["value"].(map[string]any)
		return ex["meta"].(map[string]any)["attribution"].([]any)[0].(map[string]any)
	}
	for name, c := range map[string]struct {
		path   string
		change func(map[string]any)
		want   string
	}{
		"no description":         {svc, func(op map[string]any) { op["description"] = " " }, "no description"},
		"no example request":     {svc, func(op map[string]any) { delete(op, "x-codeSamples") }, "no example request"},
		"an empty code sample":   {svc, func(op map[string]any) { op["x-codeSamples"] = []any{map[string]any{"source": ""}} }, "no example request"},
		"no attribution":         {svc, func(op map[string]any) { delete(op, "x-attribution") }, "no attribution"},
		"an empty attribution":   {data, func(op map[string]any) { op["x-attribution"] = "" }, "no attribution"},
		"no example answer":      {svc, func(op map[string]any) { delete(okExample(op), "examples") }, "no example answer"},
		"an empty example value": {svc, func(op map[string]any) { okExample(op)["examples"] = map[string]any{"e": map[string]any{}} }, "no example answer"},
		"no meta.attribution": {data, func(op map[string]any) {
			ex := okExample(op)["examples"].(map[string]any)["example"].(map[string]any)["value"].(map[string]any)
			ex["meta"].(map[string]any)["attribution"] = []any{}
		}, "carries no meta.attribution entry"},
		"an attribution without text":      {data, func(op map[string]any) { delete(firstAttribution(op), "text") }, "meta.attribution[0] without text"},
		"an attribution without publisher": {data, func(op map[string]any) { firstAttribution(op)["publisher"] = "" }, "without publisher"},
		"a data answer that is no envelope": {data, func(op map[string]any) {
			okExample(op)["examples"] = map[string]any{"e": map[string]any{"value": []any{1}}}
		}, "is not an enveloped object"},
		"a single example instead of examples, without attribution": {data, func(op map[string]any) {
			delete(okExample(op), "examples")
			okExample(op)["example"] = map[string]any{"meta": map[string]any{}}
		}, "carries no meta.attribution entry"},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := docslint.Lint(mutate(t, c.path, "get", c.change))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Findings) != 1 || !strings.Contains(r.Findings[0], c.want) || !strings.Contains(r.Findings[0], "GET "+c.path) {
				t.Fatalf("findings %q, want one naming GET %s and %q", r.Findings, c.path, c.want)
			}
		})
	}
}

// TestDocsLintFailsClosed: a document that does not parse, an operation that
// does not parse and a document without operations are errors, not a pass.
func TestDocsLintFailsClosed(t *testing.T) {
	req.Covers(t, "DX-003")
	for name, raw := range map[string]string{
		"not JSON":              `{`,
		"no operation":          `{"paths":{}}`,
		"only path members":     `{"paths":{"/x":{"parameters":[]}}}`,
		"a malformed operation": `{"paths":{"/x":{"get":{"description":1}}}}`,
	} {
		if _, err := docslint.Lint([]byte(raw)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestNonSuccessExamplesDoNotCount: an example on an error response is not
// the operation's example answer.
func TestNonSuccessExamplesDoNotCount(t *testing.T) {
	req.Covers(t, "DX-003")
	raw := `{"paths":{"/x":{"get":{"operationId":"x","description":"d","x-attribution":"none","x-codeSamples":[{"source":"curl"}],
		"responses":{"404":{"content":{"application/json":{"example":{"a":1}}}}}}}}}`
	r, err := docslint.Lint([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0], "no example answer") {
		t.Fatalf("findings %q", r.Findings)
	}
}
