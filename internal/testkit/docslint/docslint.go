// Package docslint is the documentation lint of the OpenAPI document
// (docs/examples.md): every operation must carry a description, an example
// request, an example answer and its sources' attribution. A data operation
// (one that needs a scope) must also show, in its example answer, the
// attribution object a client has to pass on: at least one entry with a
// publisher, a licence and an attribution text.
//
// The lint reads the document only. That the examples are true answers of
// the service is proved by internal/testkit/examplerun, which runs each one
// against the fixtures.
package docslint

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The members the lint requires; they are written by the HTTP adapter
// (internal/adapters/httpapi/examples.go).
const (
	codeSamples = "x-codeSamples"
	attribution = "x-attribution"
	scope       = "x-required-scope"
)

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type mediaType struct {
	Example  json.RawMessage `json:"example"`
	Examples map[string]struct {
		Value json.RawMessage `json:"value"`
	} `json:"examples"`
}

type operation struct {
	OperationID string                                            `json:"operationId"`
	Description string                                            `json:"description"`
	CodeSamples []struct{ Source string }                         `json:"x-codeSamples"`
	Attribution *string                                           `json:"x-attribution"`
	Scope       *string                                           `json:"x-required-scope"`
	Responses   map[string]struct{ Content map[string]mediaType } `json:"responses"`
}

// Result is the outcome of one lint run.
type Result struct {
	// Operations is the number of operations read.
	Operations int
	// Findings are the violations, one line each, sorted.
	Findings []string
}

// Lint checks raw, an OpenAPI document. A document that does not parse, or
// holds no operation, is an error: a lint that read nothing never passes.
func Lint(raw []byte) (Result, error) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Result{}, fmt.Errorf("docslint: %w", err)
	}
	var r Result
	for path, item := range doc.Paths {
		for _, m := range methods {
			rawOp, ok := item[m]
			if !ok {
				continue
			}
			var op operation
			if err := json.Unmarshal(rawOp, &op); err != nil {
				return Result{}, fmt.Errorf("docslint: %s %s: %w", strings.ToUpper(m), path, err)
			}
			r.Operations++
			r.Findings = append(r.Findings, check(strings.ToUpper(m)+" "+path, op)...)
		}
	}
	if r.Operations == 0 {
		return Result{}, errors.New("docslint: the document holds no operation")
	}
	slices.Sort(r.Findings)
	return r, nil
}

func check(name string, op operation) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf("%s (%s): ", name, op.OperationID)+fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(op.Description) == "" {
		add("no description")
	}
	if !slices.ContainsFunc(op.CodeSamples, func(c struct{ Source string }) bool { return strings.TrimSpace(c.Source) != "" }) {
		add("no example request (%s)", codeSamples)
	}
	if op.Attribution == nil || strings.TrimSpace(*op.Attribution) == "" {
		add("no attribution of its sources (%s)", attribution)
	}
	answers := successExamples(op)
	if len(answers) == 0 {
		add("no example answer on a 2xx response")
		return out
	}
	if op.Scope != nil {
		for _, a := range answers {
			if problem := attributionProblem(a); problem != "" {
				add("the example answer of a data operation (%s) %s", scope, problem)
			}
		}
	}
	return out
}

// successExamples are the example values of every 2xx response.
func successExamples(op operation) []json.RawMessage {
	var out []json.RawMessage
	for status, resp := range op.Responses {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		for _, mt := range resp.Content {
			if len(mt.Example) > 0 {
				out = append(out, mt.Example)
			}
			for _, e := range mt.Examples {
				if len(e.Value) > 0 {
					out = append(out, e.Value)
				}
			}
		}
	}
	return out
}

// attributionProblem says what is missing from an example answer's
// meta.attribution, or "" when it is complete.
func attributionProblem(answer json.RawMessage) string {
	var body struct {
		Meta struct {
			Attribution []map[string]any `json:"attribution"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(answer, &body); err != nil {
		return "is not an enveloped object"
	}
	if len(body.Meta.Attribution) == 0 {
		return "carries no meta.attribution entry"
	}
	for i, a := range body.Meta.Attribution {
		for _, member := range []string{"publisher", "licence", "text"} {
			if s, _ := a[member].(string); strings.TrimSpace(s) == "" {
				return fmt.Sprintf("has meta.attribution[%d] without %s", i, member)
			}
		}
	}
	return ""
}
