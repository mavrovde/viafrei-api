package oascheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// Request is one generated request.
type Request struct {
	Operation *Operation
	// Name says what the request plants, e.g. "radius_m above maximum".
	Name   string
	Target string
	// Parameter is the query parameter the request violates; empty for an
	// example request.
	Parameter string
}

// format writes a number of a schema of the given type.
func format(f float64, typ string) string {
	if typ == huma.TypeInteger {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// exampleOf returns the documented example of a parameter.
func exampleOf(p param) (string, bool) {
	switch {
	case p.Example != nil:
		return fmt.Sprint(p.Example), true
	case p.Schema != nil && len(p.Schema.Examples) > 0:
		return fmt.Sprint(p.Schema.Examples[0]), true
	}
	return "", false
}

// validOf returns a value the parameter accepts: its example, else one derived
// from its schema. A pattern-constrained string without an example has none.
func validOf(p param) (string, bool) {
	if v, ok := exampleOf(p); ok {
		return v, true
	}
	s := p.Schema
	switch {
	case s == nil:
		return "", false
	case s.Default != nil:
		return fmt.Sprint(s.Default), true
	case len(s.Enum) > 0:
		return fmt.Sprint(s.Enum[0]), true
	case s.Type == huma.TypeInteger || s.Type == huma.TypeNumber:
		if s.Minimum != nil {
			return format(*s.Minimum, s.Type), true
		}
		return "1", true
	case s.Type == huma.TypeBoolean:
		return "true", true
	case s.Type == huma.TypeString && s.Pattern == "":
		n := 1
		if s.MinLength != nil {
			n = max(n, *s.MinLength)
		}
		return strings.Repeat("a", n), true
	}
	return "", false
}

// patternMisses are tried in turn as a value outside a pattern.
var patternMisses = []string{"~", " ", "0", "a", "zz-ZZ.99"}

// violations lists one invalid value per constraint of a query parameter's
// schema, by name.
func violations(p param) ([][2]string, error) {
	s := p.Schema
	if s == nil {
		return nil, nil
	}
	var out [][2]string
	add := func(name, v string) { out = append(out, [2]string{p.Name + " " + name, v}) }
	if s.Type == huma.TypeInteger || s.Type == huma.TypeNumber || s.Type == huma.TypeBoolean {
		add("of the wrong type", "x")
	}
	if s.Minimum != nil {
		add("below minimum", format(*s.Minimum-1, s.Type))
	}
	if s.ExclusiveMinimum != nil {
		add("at the exclusive minimum", format(*s.ExclusiveMinimum, s.Type))
	}
	if s.Maximum != nil {
		add("above maximum", format(*s.Maximum+1, s.Type))
	}
	if s.ExclusiveMaximum != nil {
		add("at the exclusive maximum", format(*s.ExclusiveMaximum, s.Type))
	}
	if s.MinLength != nil && *s.MinLength > 0 {
		add("too short", strings.Repeat("a", *s.MinLength-1))
	}
	if s.MaxLength != nil {
		add("too long", strings.Repeat("a", *s.MaxLength+1))
	}
	if len(s.Enum) > 0 {
		add("outside the enumeration", "zz_not_in_enum")
	}
	if s.Pattern != "" {
		re := regexp.MustCompile(s.Pattern) // Load already compiled it: an invalid pattern fails there
		i := slices.IndexFunc(patternMisses, func(v string) bool { return !re.MatchString(v) })
		if i < 0 {
			return nil, fmt.Errorf("parameter %s: no value outside the pattern %q could be built", p.Name, s.Pattern)
		}
		add("not matching the pattern", patternMisses[i])
	}
	return out, nil
}

// target builds a request target from the operation's path, the path values
// and the query pairs, in order.
func target(op *Operation, path map[string]string, query [][2]string) string {
	p := op.Path
	for k, v := range path {
		p = strings.ReplaceAll(p, "{"+k+"}", url.PathEscape(v))
	}
	if len(query) == 0 {
		return p
	}
	parts := make([]string, len(query))
	for i, kv := range query {
		parts[i] = url.QueryEscape(kv[0]) + "=" + url.QueryEscape(kv[1])
	}
	return p + "?" + strings.Join(parts, "&")
}

// baseline returns the valid path values and the valid required query pairs
// of an operation.
func baseline(op *Operation) (map[string]string, [][2]string, error) {
	path := map[string]string{}
	var query [][2]string
	for _, p := range op.params {
		if p.In != "path" && (p.In != "query" || !p.Required) {
			continue
		}
		v, ok := validOf(p)
		if !ok {
			return nil, nil, fmt.Errorf("%s: parameter %s has no example and no value can be derived", op.ID, p.Name)
		}
		if p.In == "path" {
			path[p.Name] = v
		} else {
			query = append(query, [2]string{p.Name, v})
		}
	}
	return path, query, nil
}

func without(query [][2]string, name string) [][2]string {
	return slices.DeleteFunc(slices.Clone(query), func(kv [2]string) bool { return kv[0] == name })
}

// InvalidRequests derives the negative-direction requests of every operation.
func (d *Document) InvalidRequests() ([]Request, error) {
	var out []Request
	for _, op := range d.ops {
		path, base, err := baseline(op)
		if err != nil {
			return nil, err
		}
		out = append(out, Request{Operation: op, Name: "an unknown parameter", Parameter: UnknownParameter,
			Target: target(op, path, append(slices.Clone(base), [2]string{UnknownParameter, "1"}))})
		for _, p := range op.params {
			if p.In != "query" {
				continue
			}
			rest := without(base, p.Name)
			if p.Required {
				out = append(out, Request{Operation: op, Name: p.Name + " missing", Parameter: p.Name, Target: target(op, path, rest)})
			}
			if v, ok := validOf(p); ok {
				out = append(out, Request{Operation: op, Name: p.Name + " repeated", Parameter: p.Name,
					Target: target(op, path, append(slices.Clone(rest), [2]string{p.Name, v}, [2]string{p.Name, v}))})
			}
			vs, err := violations(p)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", op.ID, err)
			}
			for _, nv := range vs {
				out = append(out, Request{Operation: op, Name: nv[0], Parameter: p.Name,
					Target: target(op, path, append(slices.Clone(rest), [2]string{p.Name, nv[1]}))})
			}
		}
	}
	return out, nil
}

// ExampleRequests builds one request per operation from the documented
// examples of its parameters. An operation with a required query parameter
// that has no example is an error: its example request cannot be built.
func (d *Document) ExampleRequests() ([]Request, error) {
	var out []Request
	for _, op := range d.ops {
		path, _, err := baseline(op)
		if err != nil {
			return nil, err
		}
		var query [][2]string
		for _, p := range op.params {
			if p.In != "query" {
				continue
			}
			v, ok := exampleOf(p)
			if !ok && p.Required {
				return nil, fmt.Errorf("%s: required parameter %s has no example", op.ID, p.Name)
			}
			if ok {
				query = append(query, [2]string{p.Name, v})
			}
		}
		out = append(out, Request{Operation: op, Name: "the documented example", Target: target(op, path, query)})
	}
	return out, nil
}

// Refused checks the answer to an invalid request: a valid response under
// the document, 400 invalid-parameter, and the violated parameter listed in
// errors[] (ERR-005).
func (d *Document) Refused(r Request, status int, h http.Header, body []byte) error {
	if err := d.Check(r.Operation.Method, r.Target, status, h, body); err != nil {
		return fmt.Errorf("%s (%s): %w", r.Operation.ID, r.Name, err)
	}
	var p struct {
		Type   string `json:"type"`
		Errors []struct {
			Parameter string `json:"parameter"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &p) // Check proved the body is JSON
	listed := slices.ContainsFunc(p.Errors, func(e struct {
		Parameter string `json:"parameter"`
	}) bool {
		return e.Parameter == "query."+r.Parameter || e.Parameter == r.Parameter
	})
	if status != http.StatusBadRequest || !strings.HasSuffix(p.Type, "/invalid-parameter") || !listed {
		return fmt.Errorf("%s (%s): %s was answered %d %s without listing %s; want 400 invalid-parameter naming it",
			r.Operation.ID, r.Name, r.Target, status, p.Type, r.Parameter)
	}
	return nil
}

// Report is what Exercise did and found.
type Report struct {
	Invalid, Examples int
	Findings          []error
}

// String is the one-line summary of a run.
func (r Report) String() string {
	return fmt.Sprintf("%d invalid requests refused as documented, %d example requests accepted, %d finding(s)",
		r.Invalid-countPrefix(r.Findings, "invalid"), r.Examples-countPrefix(r.Findings, "example"), len(r.Findings))
}

func countPrefix(errs []error, prefix string) int {
	n := 0
	for _, e := range errs {
		if strings.HasPrefix(e.Error(), prefix+": ") {
			n++
		}
	}
	return n
}

func serve(h http.Handler, r Request) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), r.Operation.Method, r.Target, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// Exercise sends every invalid and every example request to h and checks
// each answer. A request that cannot be generated is an error.
func (d *Document) Exercise(h http.Handler) (Report, error) {
	invalid, err := d.InvalidRequests()
	if err != nil {
		return Report{}, err
	}
	examples, err := d.ExampleRequests()
	if err != nil {
		return Report{}, err
	}
	rep := Report{Invalid: len(invalid), Examples: len(examples)}
	for _, r := range invalid {
		w := serve(h, r)
		if err := d.Refused(r, w.Code, w.Header(), w.Body.Bytes()); err != nil {
			rep.Findings = append(rep.Findings, fmt.Errorf("invalid: %w", err))
		}
	}
	for _, r := range examples {
		w := serve(h, r)
		err := d.Check(r.Operation.Method, r.Target, w.Code, w.Header(), w.Body.Bytes())
		if err == nil && w.Code == http.StatusBadRequest {
			err = errors.New("answered 400")
		}
		if err != nil {
			rep.Findings = append(rep.Findings, fmt.Errorf("example: %s %s: %w", r.Operation.ID, r.Target, err))
		}
	}
	return rep, nil
}
