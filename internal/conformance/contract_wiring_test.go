package conformance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var jobKey = regexp.MustCompile(`(?m)^  [a-z][a-z0-9_-]*:\s*$`)

// jobBlock returns the text of one job of a workflow, up to the next job.
func jobBlock(workflow, name string) (string, bool) {
	start := strings.Index(workflow, "\n  "+name+":\n")
	if start < 0 {
		return "", false
	}
	rest := workflow[start+1:]
	if next := jobKey.FindAllStringIndex(rest, 2); len(next) == 2 {
		rest = rest[:next[1][0]]
	}
	return rest, true
}

// TestReleasedDocumentIsCompared holds TEST-004 to its wiring: the OpenAPI
// job compares the generated document with the last released one, with a
// full history so the last release tag can be found, and the script exists
// and is executable.
func TestReleasedDocumentIsCompared(t *testing.T) {
	req.Covers(t, "TEST-004")
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	job, ok := jobBlock(string(ci), "openapi")
	if !ok {
		t.Fatal("ci.yml has no openapi job")
	}
	for _, want := range []string{"fetch-depth: 0", "run: scripts/openapi-released.sh api/openapi.json"} {
		if !strings.Contains(job, want) {
			t.Errorf("the openapi job lacks %q", want)
		}
	}
	info, err := os.Stat(filepath.Join(root, "scripts", "openapi-released.sh"))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("scripts/openapi-released.sh is missing or not executable: %v", err)
	}
}
