package api_test

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mavrovde/viafrei-api/api"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestCommittedExamplesRead: every committed example decodes strictly and is
// in the layout record mode writes, byte for byte.
func TestCommittedExamplesRead(t *testing.T) {
	req.Covers(t, "DX-003")
	got, err := api.ReadExamples(api.Examples)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no example read")
	}
	for id, e := range got {
		raw, err := api.Examples.ReadFile("examples/" + id + ".json")
		if err != nil {
			t.Fatal(err)
		}
		out, err := e.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(raw) {
			t.Errorf("examples/%s.json is not in the layout record mode writes; re-record it (docs/examples.md)", id)
		}
	}
}

func TestReadExamplesRefusals(t *testing.T) {
	req.Covers(t, "DX-003")
	for name, c := range map[string]struct {
		fs   fstest.MapFS
		want string
	}{
		"no file":          {fstest.MapFS{}, "no examples"},
		"an unknown field": {fstest.MapFS{"examples/a.json": {Data: []byte(`{"title":"t","extra":1}`)}}, "a.json"},
		"not JSON":         {fstest.MapFS{"examples/b.json": {Data: []byte(`{`)}}, "b.json"},
		"no response":      {fstest.MapFS{"examples/c.json": {Data: []byte(`{"title":"t"}`)}}, "c.json: response is not a JSON value"},
		"unreadable":       {fstest.MapFS{"examples/d.json": {Mode: fs.ModeDir}}, "d.json"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := api.ReadExamples(c.fs)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one naming %q", err, c.want)
			}
		})
	}
}

func TestMarshalRefusesAResponseThatIsNoJSON(t *testing.T) {
	req.Covers(t, "DX-003")
	if _, err := (api.Example{Response: json.RawMessage(`{`)}).Marshal(); err == nil {
		t.Fatal("an invalid response was marshalled")
	}
}
