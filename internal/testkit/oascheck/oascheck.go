// Package oascheck holds the service to its OpenAPI document in both
// directions (TEST-003, TST-015):
//
//   - Check validates one response against the document: the status is
//     declared for the operation, every header is declared (or is one of the
//     transport headers below), the media type is declared, and the body is
//     valid against the schema for that status. A 4xx or 5xx answer is
//     application/problem+json unless the document declares another media
//     type for that exact status (ERR-001). Every valid response marks its
//     operation and documented status as covered.
//   - InvalidRequests derives one invalid request per constraint of every
//     query parameter (wrong type, below minimum, above maximum, too short,
//     too long, pattern mismatch, outside the enumeration, missing required,
//     repeated) plus one unknown parameter per operation; Refused checks that
//     the answer is 400 invalid-parameter naming that parameter (ERR-005).
//   - ExampleRequests builds one request per operation from the documented
//     parameter examples; each must be answered with a documented status
//     other than 400.
//   - Coverage prints the operation × documented status table.
//
// Path and header parameters are filled with valid values and not varied:
// the router answers a malformed path segment, and no operation declares a
// header parameter yet.
package oascheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
)

// ProblemMediaType is the media type of every error answer (ERR-001).
const ProblemMediaType = "application/problem+json"

// UnknownParameter is the name used to plant an undeclared query parameter.
const UnknownParameter = "zz_undeclared"

// TransportHeaders may appear on any response without being declared per
// operation, each for the reason given.
var TransportHeaders = map[string]string{
	"Content-Type":                 "the media type, checked against the declared content",
	"Content-Length":               "set by the HTTP layer",
	"Date":                         "set by the HTTP layer",
	"X-Request-Id":                 "every answer carries the request id (ERR-001 requestId)",
	"Strict-Transport-Security":    "security header on every answer",
	"X-Content-Type-Options":       "security header on every answer",
	"Content-Security-Policy":      "security header on every answer",
	"Referrer-Policy":              "security header on every answer",
	"Cross-Origin-Resource-Policy": "security header on every answer",
}

// ProblemHeaders may also appear on a problem response.
var ProblemHeaders = map[string]string{
	"Cache-Control": "a problem is never stored",
	"Retry-After":   "equal to the problem's retryAfter where it has one",
}

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type param struct {
	Name     string       `json:"name"`
	In       string       `json:"in"`
	Required bool         `json:"required"`
	Schema   *huma.Schema `json:"schema"`
	Example  any          `json:"example"`
}

type schemaHolder struct {
	Schema *huma.Schema `json:"schema"`
}

type response struct {
	Headers map[string]schemaHolder `json:"headers"`
	Content map[string]schemaHolder `json:"content"`
}

// Operation is one operation of the document.
type Operation struct {
	ID, Method, Path string
	params           []param
	responses        map[string]response
}

// Statuses returns the documented status keys of the operation, sorted.
func (o *Operation) Statuses() []string { return slices.Sorted(maps.Keys(o.responses)) }

// Document is a loaded OpenAPI document and the coverage recorded against it.
// It is safe for concurrent use.
type Document struct {
	ops     []*Operation
	reg     huma.Registry
	problem *huma.Schema

	mu      sync.Mutex
	covered map[string]bool
}

// Load reads an OpenAPI 3 document. It fails closed: an unparsable document,
// one without operations, an operation without an id or responses, or a
// document without a Problem schema is an error (TST-011).
func Load(raw []byte) (*Document, error) {
	var doc struct {
		OpenAPI    string                                `json:"openapi"`
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]*huma.Schema `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("oascheck: the document is not JSON: %w", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		return nil, fmt.Errorf("oascheck: not an OpenAPI 3 document (openapi %q)", doc.OpenAPI)
	}
	d := &Document{reg: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer), covered: map[string]bool{}}
	for name, s := range doc.Components.Schemas {
		d.reg.Map()[name] = s
	}
	if d.problem = d.reg.Map()["Problem"]; d.problem == nil {
		return nil, errors.New("oascheck: the document declares no Problem schema")
	}
	for _, path := range slices.Sorted(maps.Keys(doc.Paths)) {
		for _, m := range methods {
			rawOp, ok := doc.Paths[path][m]
			if !ok {
				continue
			}
			var op struct {
				OperationID string              `json:"operationId"`
				Parameters  []param             `json:"parameters"`
				Responses   map[string]response `json:"responses"`
			}
			if err := json.Unmarshal(rawOp, &op); err != nil {
				return nil, fmt.Errorf("oascheck: %s %s: %w", m, path, err)
			}
			if op.OperationID == "" || len(op.Responses) == 0 {
				return nil, fmt.Errorf("oascheck: %s %s has no operation id or no response", m, path)
			}
			d.ops = append(d.ops, &Operation{ID: op.OperationID, Method: strings.ToUpper(m), Path: path, params: op.Parameters, responses: op.Responses})
		}
	}
	if len(d.ops) == 0 {
		return nil, errors.New("oascheck: the document declares no operation")
	}
	return d, nil
}

// Operations returns the document's operations in path order.
func (d *Document) Operations() []*Operation { return d.ops }

func (d *Document) match(method, path string) *Operation {
	got := strings.Split(path, "/")
	for _, op := range d.ops {
		want := strings.Split(op.Path, "/")
		if op.Method != method || len(want) != len(got) {
			continue
		}
		ok := true
		for i := range want {
			if strings.HasPrefix(want[i], "{") {
				ok = ok && got[i] != ""
			} else {
				ok = ok && want[i] == got[i]
			}
		}
		if ok {
			return op
		}
	}
	return nil
}

// pick returns the documented response for a status: the exact code, then
// its range, then "default". The default response is the error answer, so it
// documents 4xx and 5xx only: a success status must be declared by itself.
func pick(op *Operation, status int) (string, response, bool) {
	code := strconv.Itoa(status)
	keys := []string{code, code[:1] + "XX"}
	if status >= 400 {
		keys = append(keys, "default")
	}
	for _, key := range keys {
		if r, ok := op.responses[key]; ok {
			return key, r, true
		}
	}
	return "", response{}, false
}

// validate checks v against s and returns every violation.
func (d *Document) validate(s *huma.Schema, v any) error {
	res := &huma.ValidateResult{}
	huma.Validate(d.reg, s, huma.NewPathBuffer([]byte{}, 0), huma.ModeReadFromServer, v, res)
	errs := make([]error, len(res.Errors))
	copy(errs, res.Errors)
	return errors.Join(errs...)
}

// headerValue reads a header as the type its schema declares, so a numeric
// header is checked as a number.
func headerValue(s *huma.Schema, v string) any {
	if s != nil && (s.Type == huma.TypeInteger || s.Type == huma.TypeNumber) {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return v
}

// Check validates one response to method target against the document and
// records the coverage. A response on a path the document does not hold must
// be a problem.
func (d *Document) Check(method, target string, status int, h http.Header, body []byte) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("oascheck: target %q: %w", target, err)
	}
	op := d.match(method, u.Path)
	key, resp := "", response{Content: map[string]schemaHolder{ProblemMediaType: {Schema: d.problem}}}
	name := method + " " + u.Path
	if op != nil {
		var ok bool
		if key, resp, ok = pick(op, status); !ok {
			return fmt.Errorf("%s: status %d is not documented for %s", name, status, op.ID)
		}
		name = op.ID
	} else if status < 400 {
		return fmt.Errorf("%s: status %d on a path the document does not hold", name, status)
	}

	mediaType, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	problem := mediaType == ProblemMediaType
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(h)) {
		hdr, declared := resp.Headers[k]
		for name, v := range resp.Headers {
			if !declared && http.CanonicalHeaderKey(name) == k {
				hdr, declared = v, true
			}
		}
		switch {
		case declared:
			if err := d.validate(hdr.Schema, headerValue(hdr.Schema, h.Get(k))); err != nil {
				errs = append(errs, fmt.Errorf("%s: header %s: %w", name, k, err))
			}
		case TransportHeaders[k] != "", problem && ProblemHeaders[k] != "":
		default:
			errs = append(errs, fmt.Errorf("%s: header %s is not documented for status %d", name, k, status))
		}
	}

	content, declared := resp.Content[mediaType]
	switch {
	case len(resp.Content) == 0 && len(body) == 0:
	case len(resp.Content) == 0:
		errs = append(errs, fmt.Errorf("%s: status %d documents no body, %d bytes sent", name, status, len(body)))
	case !declared:
		errs = append(errs, fmt.Errorf("%s: media type %q is not documented for status %d", name, mediaType, status))
	default:
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			errs = append(errs, fmt.Errorf("%s: the body is not JSON: %w", name, err))
		} else if err := d.validate(content.Schema, v); err != nil {
			errs = append(errs, fmt.Errorf("%s: the body does not match the schema for %d: %w", name, status, err))
		}
	}
	if status >= 400 && !problem && op != nil && key != strconv.Itoa(status) {
		errs = append(errs, fmt.Errorf("%s: a %d answer is %q, not %s (ERR-001)", name, status, mediaType, ProblemMediaType))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if op != nil {
		d.mu.Lock()
		d.covered[op.ID+" "+key] = true
		d.mu.Unlock()
	}
	return nil
}

// Coverage returns the operation × documented status table and the number of
// pairs no valid response has covered yet.
func (d *Document) Coverage() (string, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var b strings.Builder
	b.WriteString("| Operation | Method and path | Status | Validated response |\n|---|---|---|---|\n")
	pairs, missing := 0, 0
	for _, op := range d.ops {
		for _, st := range op.Statuses() {
			mark := "yes"
			if !d.covered[op.ID+" "+st] {
				mark, missing = "no", missing+1
			}
			pairs++
			fmt.Fprintf(&b, "| %s | %s %s | %s | %s |\n", op.ID, op.Method, op.Path, st, mark)
		}
	}
	fmt.Fprintf(&b, "\n%d operations, %d documented statuses, %d covered, %d not covered\n", len(d.ops), pairs, pairs-missing, missing)
	return b.String(), missing
}
