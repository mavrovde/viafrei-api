package livesources_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/livesources"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// registry is the repository's registry file, test/live/sources.json.
func registry(t *testing.T) *livesources.Registry {
	t.Helper()
	r, err := livesources.Load(filepath.Join("..", "..", "..", "test", "live", "sources.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func lookup(_ context.Context, name, value string) (string, error) {
	switch {
	case name != "station_eva":
		return "", errors.New("unknown lookup " + name)
	case value == "Köln Hbf":
		return "8000207", nil
	case value == "broken":
		return "", errors.New("store down")
	}
	return "", nil
}

func TestRegistryFile(t *testing.T) {
	r := registry(t)
	if got := strings.Join(r.Names(), ","); got != "fuel,train_board" {
		t.Fatalf("names %s", got)
	}
	if r.FreshFor("fuel") != 5*time.Minute || r.FreshFor("train_board") != 30*time.Second || r.FreshFor("nope") != 0 {
		t.Fatal("fresh_for")
	}
}

func TestCheck(t *testing.T) {
	r := registry(t)
	for _, c := range []struct {
		ask  refresh.Ask
		want string // "" valid, "source", or the location refused
	}{
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "ags:09162000"}}, ""},
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "pt:48.137,11.575", "snap": true}}, ""},
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "pt:48.137,11.575", "snap": true, "anchor": true}}, "subject"},
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "Berlin"}}, "subject.at"},
		{refresh.Ask{Source: "fuel", Subject: nil}, "subject"},
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "ags:09162000", "x": 1.0}}, "subject"},
		{refresh.Ask{Source: "train_board", Subject: map[string]any{"eva": "8000207"}}, ""},
		{refresh.Ask{Source: "train_board", Subject: map[string]any{"eva": "8000207", "name": "Köln Hbf"}}, "subject"},
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
}

func TestKey(t *testing.T) {
	r := registry(t)
	ctx := context.Background()
	for _, c := range []struct {
		ask  refresh.Ask
		want string
	}{
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "ags:09162000"}}, "ags:09162000"},
		// The site's cell: 0.005 by 0.0075 degrees, four decimals.
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "pt:48.1371,11.5754"}}, "pt:48.135,11.5725"},
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "pt:48.1369,11.5751"}}, "pt:48.135,11.5725"},
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "pt:-0.001,0.001", "snap": true}}, "pt:0,0|snap"},
		{refresh.Ask{Source: "fuel", Subject: map[string]any{"at": "pt:52.52,13.405", "anchor": true, "snap": false}}, "pt:52.52,13.4025|anchor"},
		{refresh.Ask{Source: "train_board", Subject: map[string]any{"eva": "8000207"}}, "eva:8000207"},
		{refresh.Ask{Source: "train_board", Subject: map[string]any{"name": "Köln Hbf"}}, "eva:8000207"},
	} {
		got, err := r.Key(ctx, c.ask, lookup)
		if err != nil || got != c.want {
			t.Errorf("%v: %q, %v; want %q", c.ask.Subject, got, err, c.want)
		}
	}
	var se *refresh.SubjectError
	if _, err := r.Key(ctx, refresh.Ask{Source: "train_board", Subject: map[string]any{"name": "Nowhere"}}, lookup); !errors.As(err, &se) || se.Location != "subject.name" {
		t.Errorf("an unknown station: %v", err)
	}
	if _, err := r.Key(ctx, refresh.Ask{Source: "train_board", Subject: map[string]any{"name": "Köln Hbf"}}, nil); !errors.As(err, &se) {
		t.Errorf("no lookup: %v", err)
	}
	if _, err := r.Key(ctx, refresh.Ask{Source: "train_board", Subject: map[string]any{"name": "broken"}}, lookup); err == nil || errors.As(err, &se) {
		t.Errorf("a failing lookup is no subject error: %v", err)
	}
	if _, err := r.Key(ctx, refresh.Ask{Source: "train_board", Subject: map[string]any{}}, lookup); !errors.As(err, &se) || se.Location != "subject" {
		t.Errorf("no rule applies: %v", err)
	}
	if _, err := r.Key(ctx, refresh.Ask{Source: "nope"}, lookup); !errors.Is(err, refresh.ErrSource) {
		t.Errorf("unknown source: %v", err)
	}
	bad, err := livesources.Parse([]byte(`{"sources":{"s":{"subject":{"type":"object"},"key":[{"field":"at","match":"^pt:(.*),(.*)$","grid":[1,1]}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Key(ctx, refresh.Ask{Source: "s", Subject: map[string]any{"at": "pt:x,1"}}, nil); !errors.As(err, &se) {
		t.Errorf("a grid value that is no number: %v", err)
	}
}

func TestParseRefuses(t *testing.T) {
	const obj = `"subject":{"type":"object"}`
	const key = `"key":[{"field":"a"}]`
	for name, raw := range map[string]string{
		"not json":            `{`,
		"unknown field":       `{"sources":{},"x":1}`,
		"bad name":            `{"sources":{"Fuel":{` + obj + `,` + key + `}}}`,
		"no subject":          `{"sources":{"s":{` + key + `}}}`,
		"not an object":       `{"sources":{"s":{"subject":{"type":"string"},` + key + `}}}`,
		"ref":                 `{"sources":{"s":{"subject":{"type":"object","properties":{"a":{"$ref":"#/x"}}},` + key + `}}}`,
		"unknown keyword":     `{"sources":{"s":{"subject":{"type":"object","properties":{"a":{"type":"string","patern":"x"}}},` + key + `}}}`,
		"nested ref":          `{"sources":{"s":{"subject":{"type":"object","oneOf":[{"$ref":"#/x"}]},` + key + `}}}`,
		"items ref":           `{"sources":{"s":{"subject":{"type":"object","properties":{"a":{"type":"array","items":{"$ref":"#/x"}}}},` + key + `}}}`,
		"additional ref":      `{"sources":{"s":{"subject":{"type":"object","additionalProperties":{"$ref":"#/x"}},` + key + `}}}`,
		"not ref":             `{"sources":{"s":{"subject":{"type":"object","not":{"$ref":"#/x"}},` + key + `}}}`,
		"x- keyword":          `{"sources":{"s":{"subject":{"type":"object","x-a":1},` + key + `}}}`,
		"required undeclared": `{"sources":{"s":{"subject":{"type":"object","oneOf":[{"type":"object","required":["a"]}]},` + key + `}}}`,
		"no key":              `{"sources":{"s":{` + obj + `}}}`,
		"no field":            `{"sources":{"s":{` + obj + `,"key":[{"match":"x"}]}}}`,
		"bad match":           `{"sources":{"s":{` + obj + `,"key":[{"field":"a","match":"("}]}}}`,
		"grid and lookup":     `{"sources":{"s":{` + obj + `,"key":[{"field":"a","match":"(.)","grid":[1],"lookup":"l"}]}}}`,
		"grid count":          `{"sources":{"s":{` + obj + `,"key":[{"field":"a","match":"(.)","grid":[1,2]}]}}}`,
		"grid zero":           `{"sources":{"s":{` + obj + `,"key":[{"field":"a","match":"(.)","grid":[0]}]}}}`,
		"fresh_for":           `{"sources":{"s":{` + obj + `,` + key + `,"fresh_for":"soon"}}}`,
		"fresh_for neg":       `{"sources":{"s":{` + obj + `,` + key + `,"fresh_for":"-1s"}}}`,
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
	r, err = livesources.Load("", `{"sources":{"s":{"subject":{"type":"object"},"key":[{"field":"a"}]}}}`)
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
