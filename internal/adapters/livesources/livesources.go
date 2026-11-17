// Package livesources reads the registry of live sources an on-demand
// refresh may name (G8) and checks an ask's subject against its source's
// JSON Schema. The registry is configuration, never code: a new live source
// is a new entry, with no change to the API.
//
// The registry is one JSON object:
//
//	{"sources": {"<name>": {
//	  "description": "...",
//	  "subject": <JSON Schema of an object>,
//	  "key": [<rule>, ...],
//	  "flags": ["<boolean property>", ...],
//	  "fresh_for": "<duration>"}}}
//
// A name is a lower-case letter followed by at most 31 lower-case letters,
// digits or underscores. A subject schema is of type object and uses the
// JSON Schema keywords the OpenAPI document does; a $ref or an unknown
// keyword (a misspelt one, say) is refused when the registry is read.
//
// The key rules build a subject's canonical key, so that everyone asking
// about one subject shares one refresh. The first rule whose field the
// subject holds as a string matching its pattern applies:
//
//	{"field": "at", "match": "^pt:(-?[0-9.]+),(-?[0-9.]+)$", "grid": [0.005, 0.0075], "prefix": "pt:"}
//	    each capture group snapped to its grid step, four decimals at most;
//	{"field": "name", "lookup": "station_eva", "prefix": "eva:"}
//	    the value resolved by a lookup the read store answers;
//	{"field": "eva", "match": "^([0-9]{7})$", "prefix": "eva:"}
//	    the first capture group, or the whole value without one.
//
// Then "|<flag>" is appended for each flag property the subject sets true.
// fresh_for is how long a settled refresh of a subject answers a new ask
// about it with no new refresh (a Go duration; empty for never).
package livesources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// sourceName is the shape of a source's name.
var sourceName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Source is one registered live source.
type Source struct {
	// Description says what the source refreshes.
	Description string `json:"description"`
	// Subject is the JSON Schema of the source's subject object.
	Subject *huma.Schema `json:"subject"`
	// Key holds the rules building the subject's canonical key.
	Key []Rule `json:"key"`
	// Flags are the boolean properties a key states when true.
	Flags []string `json:"flags,omitempty"`
	// FreshFor is how long a settled refresh answers a new ask.
	FreshFor string `json:"fresh_for,omitempty"`
	freshFor time.Duration
}

// Rule is one key rule.
type Rule struct {
	Field  string    `json:"field"`
	Match  string    `json:"match,omitempty"`
	Grid   []float64 `json:"grid,omitempty"`
	Lookup string    `json:"lookup,omitempty"`
	Prefix string    `json:"prefix,omitempty"`
	match  *regexp.Regexp
}

// Registry is the registered live sources; the zero value holds none.
type Registry struct {
	sources map[string]Source
	schemas huma.Registry
}

// file is the registry's layout.
type file struct {
	Sources map[string]Source `json:"sources"`
}

// Parse reads a registry from its JSON text.
func Parse(raw []byte) (*Registry, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f file
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("livesources: %w", err)
	}
	// huma ignores a keyword it does not read; the raw text is read again
	// so that a misspelt one is refused, not silently left out.
	var rf rawFile
	_ = json.Unmarshal(raw, &rf) // the same text decoded above
	for _, name := range slices.Sorted(maps.Keys(rf.Sources)) {
		if err := keywords(rf.Sources[name].Subject, "subject"); err != nil {
			return nil, fmt.Errorf("livesources: source %q: %w", name, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(f.Sources)) {
		s := f.Sources[name]
		switch {
		case !sourceName.MatchString(name):
			return nil, fmt.Errorf("livesources: source %q: a name is a lower-case letter and at most 31 lower-case letters, digits or underscores", name)
		case s.Subject == nil || s.Subject.Type != huma.TypeObject:
			return nil, fmt.Errorf("livesources: source %q: subject must be the JSON Schema of an object", name)
		}
		if err := plain(s.Subject, "subject"); err != nil {
			return nil, fmt.Errorf("livesources: source %q: %w", name, err)
		}
		if err := s.compile(); err != nil {
			return nil, fmt.Errorf("livesources: source %q: %w", name, err)
		}
		f.Sources[name] = s
	}
	return &Registry{sources: f.Sources, schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)}, nil
}

// Load reads the registry: from the file at path when path is set, else from
// the JSON text inline, else an empty registry.
func Load(path, inline string) (*Registry, error) {
	switch {
	case path != "":
		raw, err := os.ReadFile(path) // #nosec G304 -- the operator's configured registry file
		if err != nil {
			return nil, fmt.Errorf("livesources: %w", err)
		}
		return Parse(raw)
	case inline != "":
		return Parse([]byte(inline))
	}
	return &Registry{}, nil
}

// compile reads the key rules and the freshness window.
func (s *Source) compile() error {
	if len(s.Key) == 0 {
		return errors.New("key: at least one rule")
	}
	for i := range s.Key {
		r := &s.Key[i]
		re, err := regexp.Compile(r.Match)
		switch {
		case r.Field == "":
			return fmt.Errorf("key[%d]: a rule names its field", i)
		case err != nil:
			return fmt.Errorf("key[%d]: %w", i, err)
		case r.Lookup != "" && len(r.Grid) > 0:
			return fmt.Errorf("key[%d]: a rule either looks up or snaps to a grid", i)
		case len(r.Grid) > 0 && re.NumSubexp() != len(r.Grid):
			return fmt.Errorf("key[%d]: a grid step for each capture group", i)
		case slices.ContainsFunc(r.Grid, func(g float64) bool { return g <= 0 }):
			return fmt.Errorf("key[%d]: a grid step is positive", i)
		}
		r.match = re
	}
	if s.FreshFor != "" {
		d, err := time.ParseDuration(s.FreshFor)
		if err != nil || d < 0 {
			return fmt.Errorf("fresh_for: %q is no duration of zero or more", s.FreshFor)
		}
		s.freshFor = d
	}
	return nil
}

// rawFile is the registry's layout with each subject schema as its text.
type rawFile struct {
	Sources map[string]struct {
		Subject json.RawMessage `json:"subject"`
	} `json:"sources"`
}

// schemaWords are the JSON Schema keywords huma reads (huma.Schema's
// UnmarshalJSON); valueWords hold values, not schemas.
var (
	schemaWords = []string{"type", "title", "description", "format", "contentEncoding", "items", "additionalProperties",
		"properties", "minimum", "exclusiveMinimum", "maximum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength",
		"pattern", "patternDescription", "minItems", "maxItems", "uniqueItems", "required", "dependentRequired",
		"minProperties", "maxProperties", "readOnly", "writeOnly", "deprecated", "oneOf", "anyOf", "allOf", "not"}
	valueWords = []string{"default", "examples", "enum", "const"}
)

// keywords refuses a keyword huma does not read, anywhere in the schema
// text raw; a $ref or a structural error is left to plain and huma.
func keywords(raw json.RawMessage, at string) error {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k != "$ref" && !slices.Contains(schemaWords, k) && !slices.Contains(valueWords, k) {
			return fmt.Errorf("%s: unknown keyword %s", at, k)
		}
	}
	var subs []struct {
		at  string
		raw json.RawMessage
	}
	add := func(a string, r json.RawMessage) {
		subs = append(subs, struct {
			at  string
			raw json.RawMessage
		}{a, r})
	}
	for _, k := range []string{"items", "additionalProperties", "not"} {
		if r, ok := m[k]; ok {
			add(at+"."+k, r)
		}
	}
	var props map[string]json.RawMessage
	_ = json.Unmarshal(m["properties"], &props)
	for _, k := range slices.Sorted(maps.Keys(props)) {
		add(at+".properties."+k, props[k])
	}
	for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
		var list []json.RawMessage
		_ = json.Unmarshal(m[kw], &list)
		for i, r := range list {
			add(fmt.Sprintf("%s.%s[%d]", at, kw, i), r)
		}
	}
	for _, sub := range subs {
		if err := keywords(sub.raw, sub.at); err != nil {
			return err
		}
	}
	return nil
}

// plain refuses a schema using a $ref or a keyword huma does not read, and
// precomputes the messages of every schema it holds.
func plain(s *huma.Schema, at string) error {
	if s == nil {
		return nil
	}
	if s.Ref != "" {
		return fmt.Errorf("%s: $ref is not supported", at)
	}
	// huma checks a required name only among the declared properties.
	for _, req := range s.Required {
		if _, ok := s.Properties[req]; !ok {
			return fmt.Errorf("%s: required %q is not among its properties", at, req)
		}
	}
	s.PrecomputeMessages()
	if a, ok := s.AdditionalProperties.(*huma.Schema); ok {
		if err := plain(a, at+".additionalProperties"); err != nil {
			return err
		}
	}
	for _, k := range slices.Sorted(maps.Keys(s.Properties)) {
		if err := plain(s.Properties[k], at+".properties."+k); err != nil {
			return err
		}
	}
	for kw, list := range map[string][]*huma.Schema{"oneOf": s.OneOf, "anyOf": s.AnyOf, "allOf": s.AllOf} {
		for i, sub := range list {
			if err := plain(sub, fmt.Sprintf("%s.%s[%d]", at, kw, i)); err != nil {
				return err
			}
		}
	}
	if err := plain(s.Items, at+".items"); err != nil {
		return err
	}
	return plain(s.Not, at+".not")
}

// Names lists the registered sources in name order.
func (r *Registry) Names() []string { return slices.Sorted(maps.Keys(r.sources)) }

// Check checks an ask: refresh.ErrSource when its source is not registered,
// a *refresh.SubjectError naming the first location its source's schema
// refuses.
func (r *Registry) Check(a refresh.Ask) error {
	s, ok := r.sources[a.Source]
	if !ok {
		return refresh.ErrSource
	}
	res := &huma.ValidateResult{}
	huma.Validate(r.schemas, s.Subject, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, subjectValue(a.Subject), res)
	if len(res.Errors) == 0 {
		return nil
	}
	// huma's validator adds every error as an *ErrorDetail.
	d, _ := res.Errors[0].(*huma.ErrorDetail)
	loc := "subject"
	if d.Location != "" {
		loc += "." + d.Location
	}
	return &refresh.SubjectError{Location: loc, Detail: d.Message}
}

// FreshFor is how long a settled refresh of the source answers a new ask;
// zero for an unknown source.
func (r *Registry) FreshFor(source string) time.Duration { return r.sources[source].freshFor }

// Key builds the canonical key of a checked ask's subject.
func (r *Registry) Key(ctx context.Context, a refresh.Ask, lookup refresh.Lookup) (string, error) {
	s, ok := r.sources[a.Source]
	if !ok {
		return "", refresh.ErrSource
	}
	for _, rule := range s.Key {
		v, _ := a.Subject[rule.Field].(string)
		m := rule.match.FindStringSubmatch(v)
		if v == "" || m == nil {
			continue
		}
		key, err := rule.build(ctx, v, m, lookup)
		if err != nil {
			return "", err
		}
		if key == "" {
			return "", &refresh.SubjectError{Location: "subject." + rule.Field, Detail: "nothing answers this value"}
		}
		for _, f := range s.Flags {
			if a.Subject[f] == true {
				key += "|" + f
			}
		}
		return key, nil
	}
	return "", &refresh.SubjectError{Location: "subject", Detail: "no key rule of the source applies"}
}

// build is the key part of one rule over the matched value v; "" when the
// lookup finds nothing.
func (r Rule) build(ctx context.Context, v string, m []string, lookup refresh.Lookup) (string, error) {
	switch {
	case r.Lookup != "":
		if lookup == nil {
			return "", nil
		}
		got, err := lookup(ctx, r.Lookup, v)
		if err != nil || got == "" {
			return "", err
		}
		return r.Prefix + got, nil
	case len(r.Grid) > 0:
		parts := make([]string, len(r.Grid))
		for i, step := range r.Grid {
			x, err := strconv.ParseFloat(m[i+1], 64)
			if err != nil {
				return "", &refresh.SubjectError{Location: "subject." + r.Field, Detail: "not a number: " + m[i+1]}
			}
			parts[i] = snap(x, step)
		}
		return r.Prefix + strings.Join(parts, ","), nil
	case len(m) > 1:
		return r.Prefix + m[1], nil
	}
	return r.Prefix + v, nil
}

// snap is x on the grid of step, at most four decimals, as the site rounds
// a position to its cell.
func snap(x, step float64) string {
	v := math.Round(math.Round(x/step)*step*1e4) / 1e4
	if v == 0 {
		v = 0 // no negative zero
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// subjectValue is the subject as huma validates a decoded JSON object.
func subjectValue(m map[string]any) any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
