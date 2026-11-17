package livesources_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/livesources"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// registryFile is the repository's registry, test/live/registry.json: a
// copy of the live refresher's generated registry.json, byte for byte but
// for a lookup's table, whose platform storage name is masked (the scope
// lint; the API resolves a lookup by its name, never by its table), pinned
// by its SHA-256 (registrySHA256). A new copy is a new pin, in the same
// commit.
var registryFile = filepath.Join("..", "..", "..", "test", "live", "registry.json")

const registrySHA256 = "15f6e205b94c65432338a804fbff23ce7b7557da98974a4a4e17c1fd91b89d97"

func registry(t *testing.T) *livesources.Registry {
	t.Helper()
	r, err := livesources.Load(registryFile, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func lookup(_ context.Context, name, value string) (string, error) {
	switch {
	case name != "eva":
		return "", errors.New("unknown lookup " + name)
	case value == "Köln Hbf":
		return "8000207", nil
	case value == "broken":
		return "", errors.New("store down")
	}
	return "", nil
}

const fuel, board = "fuel-prices-de", "train-board-de"

func TestRegistryFileIsThePinnedCopy(t *testing.T) {
	raw, err := os.ReadFile(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != registrySHA256 {
		t.Fatalf("test/live/registry.json is not the pinned copy: %x", sum)
	}
	r := registry(t)
	if got := strings.Join(r.Names(), ","); got != fuel+","+board {
		t.Fatalf("names %s", got)
	}
	if r.FreshFor(fuel) != 30*time.Minute || r.FreshFor(board) != 30*time.Second || r.FreshFor("nope") != 0 {
		t.Fatal("freshness_s")
	}
	if r.Deadline(fuel) != 1200*time.Millisecond || r.Deadline(board) != 1200*time.Millisecond || r.Deadline("nope") != 0 {
		t.Fatal("deadline_ms")
	}
}

func TestCheck(t *testing.T) {
	r := registry(t)
	for _, c := range []struct {
		ask  refresh.Ask
		want string // "" valid, "source", or the location refused
	}{
		{refresh.Ask{Source: fuel, Subject: map[string]any{"at": "ags:09162000"}}, ""},
		{refresh.Ask{Source: fuel, Subject: map[string]any{"at": "pt:48.137,11.575", "snap": true}}, ""},
		{refresh.Ask{Source: fuel, Subject: map[string]any{"at": "Berlin"}}, "subject.at"},
		{refresh.Ask{Source: fuel, Subject: nil}, "subject"},
		{refresh.Ask{Source: fuel, Subject: map[string]any{"at": "ags:09162000", "x": 1.0}}, "subject"},
		{refresh.Ask{Source: board, Subject: map[string]any{"eva": "8000207"}}, ""},
		{refresh.Ask{Source: board, Subject: map[string]any{"eva": "8000207", "name": "Köln Hbf"}}, "subject"},
		{refresh.Ask{Source: "weather", Subject: map[string]any{}}, "source"},
	} {
		err := r.Check(c.ask)
		var se *refresh.SubjectError
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: %v", c.ask, err)
		case c.want == "source" && !errors.Is(err, refresh.ErrSource):
			t.Errorf("%v: %v, want ErrSource", c.ask, err)
		case c.want != "" && c.want != "source" && (!errors.As(err, &se) || !strings.HasPrefix(se.Location, c.want) || se.Error() == ""):
			t.Errorf("%v: %v, want a subject error at %s", c.ask, err, c.want)
		}
	}
	off, err := livesources.Parse([]byte(`{"schema":1,"sources":[{"id":"s","status":"draft","subject":{"type":"object"},"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a}"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := off.Check(refresh.Ask{Source: "s", Subject: map[string]any{}}); !errors.Is(err, refresh.ErrPaused) {
		t.Errorf("a source switched off: %v", err)
	}
}

func TestKey(t *testing.T) {
	r := registry(t)
	ctx := context.Background()
	for _, c := range []struct {
		source  string
		subject map[string]any
		want    string
	}{
		{fuel, map[string]any{"at": "ags:09162000"}, "ags:09162000"},
		{fuel, map[string]any{"at": "ags:09162000", "snap": true}, "ags:09162000"},
		// A motorway anchor as the API resolves it: its BASt LCL code.
		{fuel, map[string]any{"at": "anchor:12345", "anchor": true}, "anchor:12345"},
		// The registry's own cases for a position: never stored (Stored).
		{fuel, map[string]any{"at": "pt:48.1371,11.5754", "anchor": true}, "cell:48.135,11.5725:false"},
		{fuel, map[string]any{"at": "pt:48.1371,11.5754"}, "cell:48.135,11.5725:false"},
		{fuel, map[string]any{"at": "pt:-0.001,0.001", "snap": true}, "cell:0,0:true"},
		{board, map[string]any{"eva": "8000207"}, "8000207"},
		{board, map[string]any{"name": "  Köln Hbf "}, "8000207"},
	} {
		got, err := r.Key(ctx, c.source, c.subject, lookup)
		if err != nil || got != c.want {
			t.Errorf("%v: %q, %v; want %q", c.subject, got, err, c.want)
		}
	}
	var se *refresh.SubjectError
	if _, err := r.Key(ctx, board, map[string]any{"name": "Nowhere"}, lookup); !errors.As(err, &se) || se.Location != "subject.name" {
		t.Errorf("an unknown station: %v", err)
	}
	if _, err := r.Key(ctx, board, map[string]any{"name": "Köln Hbf"}, nil); !errors.As(err, &se) {
		t.Errorf("no lookup: %v", err)
	}
	if _, err := r.Key(ctx, board, map[string]any{"name": "broken"}, lookup); err == nil || errors.As(err, &se) {
		t.Errorf("a failing lookup is no subject error: %v", err)
	}
	if _, err := r.Key(ctx, board, map[string]any{"eva": 7.0}, lookup); !errors.As(err, &se) || se.Location != "subject" {
		t.Errorf("no case applies: %v", err)
	}
	if _, err := r.Key(ctx, "nope", nil, lookup); !errors.Is(err, refresh.ErrSource) {
		t.Errorf("unknown source: %v", err)
	}
	grid, err := livesources.Parse([]byte(`{"schema":1,"sources":[{"id":"s","status":"enabled","subject":{"type":"object"},"subject_key":{"cases":[{"match":{"at":"^(?<x>.*)$"},"emit":"{x|grid:1}"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grid.Key(ctx, "s", map[string]any{"at": "x"}, nil); !errors.As(err, &se) {
		t.Errorf("a grid value that is no number: %v", err)
	}
}

func TestStoredHoldsPublicIdsOnly(t *testing.T) {
	r := registry(t)
	for _, c := range []struct {
		source, key string
		want        string // the stored subject's one field=value, "" refused
	}{
		{fuel, "ags:09162000", "at=ags:09162000"},
		{board, "8000207", "eva=8000207"},
		{fuel, "anchor:12345", "at=anchor:12345"},
		{fuel, "anchor:48.1371,11.5754", ""},
		{fuel, "cell:48.135,11.5725:false", ""},
		{fuel, "pt:48.1371,11.5754", ""},
		{board, "Köln Hbf", ""},
	} {
		got, err := r.Stored(c.source, c.key)
		var se *refresh.SubjectError
		switch {
		case c.want == "" && !errors.As(err, &se):
			t.Errorf("%s %s: %v %v, want refused", c.source, c.key, got, err)
		case c.want != "":
			field, value, _ := strings.Cut(c.want, "=")
			if err != nil || len(got) != 1 || got[field] != value {
				t.Errorf("%s %s: %v %v, want %s", c.source, c.key, got, err, c.want)
			}
		}
	}
	if _, err := r.Stored("nope", "x"); !errors.Is(err, refresh.ErrSource) {
		t.Errorf("unknown source: %v", err)
	}
}

func TestParseRefuses(t *testing.T) {
	src := func(body string) string { return `{"schema":1,"sources":[{"id":"s","status":"enabled",` + body + `}]}` }
	const obj = `"subject":{"type":"object"}`
	const key = `"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a}"}]}`
	for name, raw := range map[string]string{
		"not json":            `{`,
		"no schema":           `{"sources":[]}`,
		"schema 2":            `{"schema":2,"sources":[]}`,
		"bad id":              `{"schema":1,"sources":[{"id":"Fuel",` + obj + `,` + key + `}]}`,
		"twice":               `{"schema":1,"sources":[{"id":"s",` + obj + `,` + key + `},{"id":"s",` + obj + `,` + key + `}]}`,
		"no subject":          src(key),
		"not an object":       src(`"subject":{"type":"string"},` + key),
		"ref":                 src(`"subject":{"type":"object","properties":{"a":{"$ref":"#/x"}}},` + key),
		"unknown keyword":     src(`"subject":{"type":"object","properties":{"a":{"type":"string","patern":"x"}}},` + key),
		"nested ref":          src(`"subject":{"type":"object","oneOf":[{"$ref":"#/x"}]},` + key),
		"items ref":           src(`"subject":{"type":"object","properties":{"a":{"type":"array","items":{"$ref":"#/x"}}}},` + key),
		"additional ref":      src(`"subject":{"type":"object","additionalProperties":{"$ref":"#/x"}},` + key),
		"not ref":             src(`"subject":{"type":"object","not":{"$ref":"#/x"}},` + key),
		"x- keyword":          src(`"subject":{"type":"object","x-a":1},` + key),
		"required undeclared": src(`"subject":{"type":"object","oneOf":[{"type":"object","required":["a"]}]},` + key),
		"no cases":            src(obj + `,"subject_key":{"cases":[]}`),
		"no match":            src(obj + `,"subject_key":{"cases":[{"match":{},"emit":"x"}]}`),
		"bad match":           src(obj + `,"subject_key":{"cases":[{"match":{"a":"("},"emit":"x"}]}`),
		"no emit":             src(obj + `,"subject_key":{"cases":[{"match":{"a":"."}}]}`),
		"empty placeholder":   src(obj + `,"subject_key":{"cases":[{"match":{"a":"."},"emit":"{}"}]}`),
		"unknown filter":      src(obj + `,"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a|upper}"}]}`),
		"grid zero":           src(obj + `,"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a|grid:0}"}]}`),
		"grid word":           src(obj + `,"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a|grid:x}"}]}`),
		"unknown lookup":      src(obj + `,"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a|lookup:eva}"}]}`),
		"negative freshness":  src(obj + `,` + key + `,"freshness_s":-1`),
		"negative deadline":   src(obj + `,` + key + `,"deadline_ms":-1`),
	} {
		if _, err := livesources.Parse([]byte(raw)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestLoad(t *testing.T) {
	r, err := livesources.Load("", "")
	if err != nil || len(r.Names()) != 0 {
		t.Fatalf("empty: %v %v", r, err)
	}
	r, err = livesources.Load("", `{"schema":1,"sources":[{"id":"s","status":"enabled","subject":{"type":"object"},"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a}"}]}}]}`)
	if err != nil || len(r.Names()) != 1 {
		t.Fatalf("inline: %v", err)
	}
	if _, err := livesources.Load(filepath.Join(t.TempDir(), "missing.json"), ""); err == nil {
		t.Fatal("a missing file")
	}
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := livesources.Load(p, ""); err == nil {
		t.Fatal("a bad file")
	}
}
