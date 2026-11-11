package httpapi

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/api"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func withExamples(t *testing.T, fsys fstest.MapFS) {
	t.Helper()
	old := loadExamples
	loadExamples = func() (map[string]api.Example, error) { return api.ReadExamples(fsys) }
	t.Cleanup(func() { loadExamples = old })
}

// TestEveryExampleNamesAnOperation: a committed example of an operation that
// no longer exists is stale and fails here.
func TestEveryExampleNamesAnOperation(t *testing.T) {
	req.Covers(t, "DX-003")
	examples, err := api.ReadExamples(api.Examples)
	if err != nil {
		t.Fatal(err)
	}
	ids := OperationIDs()
	for id := range examples {
		found := false
		for _, known := range ids {
			found = found || known == id
		}
		if !found {
			t.Errorf("api/examples/%s.json names no operation", id)
		}
	}
}

// TestBuildRefusesABrokenExample: an unreadable example set, and an example
// whose status the operation does not document as JSON, stop the build.
func TestBuildRefusesABrokenExample(t *testing.T) {
	req.Covers(t, "DX-003")
	for name, c := range map[string]struct {
		fs   fstest.MapFS
		want string
	}{
		"no example at all":      {fstest.MapFS{}, "no examples"},
		"an undocumented status": {fstest.MapFS{"examples/getHealth.json": {Data: []byte(`{"title":"t","request":"/healthz","attribution":"none","status":201,"response":{}}`)}}, "status 201 is not documented"},
	} {
		t.Run(name, func(t *testing.T) {
			withExamples(t, c.fs)
			if _, _, err := build(Deps{Config: config.Defaults()}, nil); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("build err = %v, want %q", err, c.want)
			}
		})
	}
}

// TestCurlSample: a keyed operation's sample presents the key from the
// environment, never a literal key; an open one presents none.
func TestCurlSample(t *testing.T) {
	req.Covers(t, "DX-003", "PROT-053")
	open := curlSample("https://api.example", "/healthz", false)
	if open != `curl -sS "https://api.example/healthz"` {
		t.Errorf("open sample %q", open)
	}
	keyed := curlSample("https://api.example", "/v1/key", true)
	if !strings.Contains(keyed, `-H "Authorization: Bearer $VIAFREI_API_KEY"`) || strings.Contains(keyed, "vf_") {
		t.Errorf("keyed sample %q", keyed)
	}
}

func TestPathOperationsInMethodOrder(t *testing.T) {
	req.Covers(t, "DX-003")
	g, p := &huma.Operation{OperationID: "g"}, &huma.Operation{OperationID: "p"}
	got := pathOperations(&huma.PathItem{Post: p, Get: g})
	if len(got) != 2 || got[0] != g || got[1] != p {
		t.Fatalf("got %v", got)
	}
}
