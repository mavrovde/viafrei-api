// Package api holds the per-operation examples the OpenAPI document carries
// (docs/examples.md). Each file examples/<operationId>.json is one example:
// the request, the answer the service gives to it on the synthetic fixture
// set, and the attribution rule of the operation's sources. The service
// embeds them into the document it builds; an integration test runs every
// request against the fixtures and fails when an answer drifts from its
// example.
package api

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// Examples are the committed example files.
//
//go:embed examples/*.json
var Examples embed.FS

// Example is one operation's example.
type Example struct {
	// Title names what the example shows.
	Title string `json:"title"`
	// Request is the request target: path and query, without the base URL.
	Request string `json:"request"`
	// Attribution says which sources answer and how their attribution
	// reaches the client; for an operation without source data it says so.
	Attribution string `json:"attribution"`
	// Key is the key the example request presents, for a keyed operation.
	Key *Key `json:"key,omitempty"`
	// Status is the HTTP status of the answer.
	Status int `json:"status"`
	// Response is the answer's body.
	Response json.RawMessage `json:"response"`
}

// Key describes the fictional key of an example: its plan and the terms it
// has accepted.
type Key struct {
	UseClass string       `json:"useClass"`
	Scopes   []string     `json:"scopes"`
	Sources  []string     `json:"sources"`
	Accepted []Acceptance `json:"accepted,omitempty"`
}

// Acceptance is one accepted terms version.
type Acceptance struct {
	Source  string `json:"source"`
	Version string `json:"version"`
}

// ReadExamples reads every examples/*.json of fsys, keyed by operation id
// (the file name). A file that does not decode strictly, or whose response
// is not JSON, is an error naming the file.
func ReadExamples(fsys fs.FS) (map[string]Example, error) {
	// The pattern is a constant: Glob cannot fail on it.
	names, _ := fs.Glob(fsys, "examples/*.json")
	if len(names) == 0 {
		return nil, errors.New("api: no examples/*.json")
	}
	out := make(map[string]Example, len(names))
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var e Example
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !json.Valid(e.Response) {
			return nil, fmt.Errorf("%s: response is not a JSON value", name)
		}
		out[strings.TrimSuffix(path.Base(name), ".json")] = e
	}
	return out, nil
}

// Marshal writes the example in the committed layout: two-space indent,
// fields in declaration order, a final newline.
func (e Example) Marshal() ([]byte, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	_ = json.Indent(&b, raw, "", "  ") // raw is the output of json.Marshal
	b.WriteByte('\n')
	return b.Bytes(), nil
}
