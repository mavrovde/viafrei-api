// Package livesources reads the registry of live sources an on-demand
// refresh may name (G8), the live refresher's own registry.json, and checks
// and keys an ask's subject by it. The registry is configuration, never
// code: a new live source is a new entry, with no change to the API.
//
// The registry is one JSON object (fields the API does not read, such as a
// source's licence, are left alone):
//
//	{"schema": 1, "sources": [{
//	  "id": "<source id>", "status": "enabled" | ...,
//	  "subject": <JSON Schema of an object>,
//	  "subject_key": {"lookups": {"<name>": {...}}, "cases": [<case>, ...]},
//	  "freshness_s": <n>, "deadline_ms": <n>, "max_wait_s": <n>}]}
//
// An id is a lower-case letter followed by at most 63 lower-case letters,
// digits or hyphens. A source whose status is not enabled is switched off:
// an ask naming it is refused as paused. A subject schema is of type object
// and uses the JSON Schema keywords the OpenAPI document does; a $ref or an
// unknown keyword (a misspelt one, say) is refused when the registry is read.
//
// The cases build a subject's key, so that everyone asking about one subject
// shares one refresh. The first case all of whose match patterns match
// applies (a boolean is matched as true or false); its emit template is
// filled from the patterns' named groups, else from the subject's fields:
//
//	{"match": {"at": "^ags:(?<ags>[0-9]{8})$"}, "emit": "ags:{ags}"}
//	{"match": {"name": "\\S"}, "emit": "{name|trim|lookup:eva}"}
//
// A placeholder's filters run in order: trim, bool (true or false),
// grid:<step> (the number snapped to the step, four decimals at most) and
// lookup:<name> (the value resolved by a lookup the read store answers).
//
// The stored subject holds public ids only (the data-steward's privacy
// ruling): it is the one-field subject a case keys to the key itself (at
// ags:<AGS> or anchor:<BASt LCL code> for fuel, eva for a train board). A key
// no such case gives back (a cell) is refused, and a position is resolved to
// its Gemeinde, or a motorway anchor to the LCL code of its point, before it
// is keyed (features), so it never reaches the store.
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

	"github.com/mavrovde/viafrei-api/internal/domain/number"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// sourceID is the shape of a source's id, as the request table checks it.
var sourceID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// placeholder is one {name|filter|...} of an emit template.
var placeholder = regexp.MustCompile(`\{([^{}]*)\}`)

// Enabled is the status of a source that is switched on.
const Enabled = "enabled"

// Source is one registered live source.
type Source struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Subject is the JSON Schema of the source's subject object.
	Subject    *huma.Schema `json:"subject"`
	SubjectKey KeyRule      `json:"subject_key"`
	// FreshnessS is how long a settled refresh answers a new ask with no
	// new refresh, in seconds; 0 for never.
	FreshnessS int `json:"freshness_s"`
	// DeadlineMS is how long an ask waits for its refresh to settle.
	DeadlineMS int `json:"deadline_ms"`
	// MaxWaitS is how long the live refresher keeps a request queued.
	MaxWaitS int `json:"max_wait_s"`
}

// KeyRule is a source's subject_key rule.
type KeyRule struct {
	Lookups map[string]json.RawMessage `json:"lookups,omitempty"`
	Cases   []Case                     `json:"cases"`
}

// Case is one case of a key rule.
type Case struct {
	Match map[string]string `json:"match"`
	Emit  string            `json:"emit"`
	match map[string]*regexp.Regexp
}

// Registry is the registered live sources; the zero value holds none.
type Registry struct {
	sources map[string]Source
	schemas huma.Registry
}

// file is the registry's layout.
type file struct {
	Schema  int      `json:"schema"`
	Sources []Source `json:"sources"`
}

// Parse reads a registry from its JSON text.
func Parse(raw []byte) (*Registry, error) {
	var f file
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&f); err != nil {
		return nil, fmt.Errorf("livesources: %w", err)
	}
	if f.Schema != 1 {
		return nil, fmt.Errorf("livesources: schema %d, this reader knows schema 1", f.Schema)
	}
	// huma ignores a keyword it does not read; the raw text is read again
	// so that a misspelt one is refused, not silently left out.
	var rf rawFile
	_ = json.Unmarshal(raw, &rf) // the same text decoded above
	for i, s := range rf.Sources {
		if err := keywords(s.Subject, "subject"); err != nil {
			return nil, fmt.Errorf("livesources: sources[%d]: %w", i, err)
		}
	}
	out := map[string]Source{}
	for i, s := range f.Sources {
		switch {
		case !sourceID.MatchString(s.ID):
			return nil, fmt.Errorf("livesources: sources[%d]: an id is a lower-case letter and at most 63 lower-case letters, digits or hyphens", i)
		case out[s.ID].ID != "":
			return nil, fmt.Errorf("livesources: source %q: registered twice", s.ID)
		case s.Subject == nil || s.Subject.Type != huma.TypeObject:
			return nil, fmt.Errorf("livesources: source %q: subject must be the JSON Schema of an object", s.ID)
		case s.FreshnessS < 0 || s.DeadlineMS < 0 || s.MaxWaitS < 0:
			return nil, fmt.Errorf("livesources: source %q: freshness_s, deadline_ms and max_wait_s are zero or more", s.ID)
		}
		if err := plain(s.Subject, "subject"); err != nil {
			return nil, fmt.Errorf("livesources: source %q: %w", s.ID, err)
		}
		if err := s.SubjectKey.compile(); err != nil {
			return nil, fmt.Errorf("livesources: source %q: subject_key: %w", s.ID, err)
		}
		out[s.ID] = s
	}
	return &Registry{sources: out, schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)}, nil
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

// compile reads the cases: each matches at least one field, and each of its
// placeholders names a group or a field with known filters.
func (k *KeyRule) compile() error {
	if len(k.Cases) == 0 {
		return errors.New("at least one case")
	}
	for i := range k.Cases {
		c := &k.Cases[i]
		if len(c.Match) == 0 {
			return fmt.Errorf("cases[%d]: a case matches at least one field", i)
		}
		c.match = map[string]*regexp.Regexp{}
		for _, field := range slices.Sorted(maps.Keys(c.Match)) {
			re, err := regexp.Compile(c.Match[field])
			if err != nil {
				return fmt.Errorf("cases[%d].match.%s: %w", i, field, err)
			}
			c.match[field] = re
		}
		if c.Emit == "" {
			return fmt.Errorf("cases[%d]: a case emits a key", i)
		}
		for _, m := range placeholder.FindAllStringSubmatch(c.Emit, -1) {
			parts := strings.Split(m[1], "|")
			if parts[0] == "" {
				return fmt.Errorf("cases[%d]: an empty placeholder", i)
			}
			for _, f := range parts[1:] {
				if err := k.filterKnown(f); err != nil {
					return fmt.Errorf("cases[%d]: %w", i, err)
				}
			}
		}
	}
	return nil
}

func (k *KeyRule) filterKnown(f string) error {
	name, arg, _ := strings.Cut(f, ":")
	switch name {
	case "trim", "bool":
		return nil
	case "grid":
		if step, err := number.ParseFinite(arg); err != nil || step <= 0 {
			return fmt.Errorf("filter %q: a grid step is a positive number", f)
		}
		return nil
	case "lookup":
		if _, ok := k.Lookups[arg]; !ok {
			return fmt.Errorf("filter %q: no such lookup", f)
		}
		return nil
	}
	return fmt.Errorf("unknown filter %q", f)
}

// rawFile is the registry's layout with each subject schema as its text.
type rawFile struct {
	Sources []struct {
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

// Names lists the registered sources in id order.
func (r *Registry) Names() []string { return slices.Sorted(maps.Keys(r.sources)) }

// Check checks an ask: refresh.ErrSource when its source is not registered,
// refresh.ErrPaused when it is switched off, a *refresh.SubjectError naming
// the first location its source's schema refuses.
func (r *Registry) Check(a refresh.Ask) error {
	s, ok := r.sources[a.Source]
	switch {
	case !ok:
		return refresh.ErrSource
	case s.Status != Enabled:
		return refresh.ErrPaused
	}
	res := &huma.ValidateResult{}
	huma.Validate(r.schemas, s.Subject, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, subjectValue(a.Subject), res)
	if len(res.Errors) == 0 {
		return nil
	}
	// huma's validator adds every error as an *ErrorDetail.
	var d *huma.ErrorDetail
	_ = errors.As(res.Errors[0], &d)
	loc := "subject"
	if d.Location != "" {
		loc += "." + d.Location
	}
	return &refresh.SubjectError{Location: loc, Detail: d.Message}
}

// FreshFor is how long a settled refresh of the source answers a new ask;
// zero for an unknown source.
func (r *Registry) FreshFor(source string) time.Duration {
	return time.Duration(r.sources[source].FreshnessS) * time.Second
}

// Deadline is how long an ask of the source waits for its refresh to
// settle; zero for an unknown source or none set.
func (r *Registry) Deadline(source string) time.Duration {
	return time.Duration(r.sources[source].DeadlineMS) * time.Millisecond
}

// Key builds the key of a checked subject of the source.
func (r *Registry) Key(ctx context.Context, source string, subject map[string]any, lookup refresh.Lookup) (string, error) {
	s, ok := r.sources[source]
	if !ok {
		return "", refresh.ErrSource
	}
	for _, c := range s.SubjectKey.Cases {
		groups, ok := c.matches(subject)
		if !ok {
			continue
		}
		return c.emit(ctx, subject, groups, lookup)
	}
	return "", &refresh.SubjectError{Location: "subject", Detail: "no key case of the source applies"}
}

// Stored is the subject stored for key: the one-field subject a case of the
// source keys to key itself, with no lookup. A key no case gives back is
// not a public id, and is refused.
func (r *Registry) Stored(source, key string) (map[string]any, error) {
	s, ok := r.sources[source]
	if !ok {
		return nil, refresh.ErrSource
	}
	for _, c := range s.SubjectKey.Cases {
		if len(c.match) != 1 || strings.Contains(c.Emit, "lookup:") {
			continue
		}
		for field := range c.match {
			subject := map[string]any{field: key}
			groups, ok := c.matches(subject)
			if !ok {
				continue
			}
			if got, err := c.emit(context.Background(), subject, groups, nil); err == nil && got == key {
				return subject, nil
			}
		}
	}
	return nil, &refresh.SubjectError{Location: "subject", Detail: "the source keys this subject by no public id"}
}

// matches says whether every pattern of the case matches its field, with
// the patterns' named groups.
func (c Case) matches(subject map[string]any) (map[string]string, bool) {
	groups := map[string]string{}
	for field, re := range c.match {
		v, ok := text(subject[field])
		if !ok {
			return nil, false
		}
		m := re.FindStringSubmatch(v)
		if m == nil {
			return nil, false
		}
		for i, name := range re.SubexpNames() {
			if name != "" {
				groups[name] = m[i]
			}
		}
	}
	return groups, true
}

// emit fills the case's template.
func (c Case) emit(ctx context.Context, subject map[string]any, groups map[string]string, lookup refresh.Lookup) (string, error) {
	var b strings.Builder
	last := 0
	for _, loc := range placeholder.FindAllStringSubmatchIndex(c.Emit, -1) {
		b.WriteString(c.Emit[last:loc[0]])
		last = loc[1]
		parts := strings.Split(c.Emit[loc[2]:loc[3]], "|")
		v, ok := groups[parts[0]]
		if !ok {
			v, _ = text(subject[parts[0]])
		}
		for _, f := range parts[1:] {
			var err error
			if v, err = filter(ctx, f, parts[0], v, lookup); err != nil {
				return "", err
			}
		}
		b.WriteString(v)
	}
	b.WriteString(c.Emit[last:])
	return b.String(), nil
}

// filter runs one filter over the value v of the subject's field.
func filter(ctx context.Context, f, field, v string, lookup refresh.Lookup) (string, error) {
	name, arg, _ := strings.Cut(f, ":")
	switch name {
	case "trim":
		return strings.TrimSpace(v), nil
	case "bool":
		return strconv.FormatBool(v == "true"), nil
	case "grid":
		step, _ := number.ParseFinite(arg) // checked when the registry was read
		x, err := number.ParseFinite(v)
		if err != nil {
			return "", &refresh.SubjectError{Location: "subject." + field, Detail: "not a number"}
		}
		return snap(x, step), nil
	}
	// lookup, the one filter left once the registry is read
	if lookup == nil {
		return "", &refresh.SubjectError{Location: "subject." + field, Detail: "nothing answers this value"}
	}
	got, err := lookup(ctx, arg, v)
	if err != nil {
		return "", err
	}
	if got == "" {
		return "", &refresh.SubjectError{Location: "subject." + field, Detail: "nothing answers this value"}
	}
	return got, nil
}

// text is a subject value as a pattern matches it: a string as it is, a
// boolean as true or false; false for anything else or none.
func text(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

// snap is x on the grid of step, at most four decimals.
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
