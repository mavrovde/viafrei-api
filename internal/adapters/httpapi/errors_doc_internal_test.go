package httpapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestErrorPageListsTheCatalogue: docs/errors.md, the page every problem type
// URI points at, has one section per catalogued type, headed by its code and
// stating its status, title and retryability as the code does; nothing more,
// nothing less (ERR-002, FR-SVC-006).
func TestErrorPageListsTheCatalogue(t *testing.T) {
	req.Covers(t, "ERR-002", "FR-SVC-006", "ERR-003")
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "errors.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	seen := map[string]bool{}
	for i, l := range lines {
		slug, ok := strings.CutPrefix(l, "### ")
		if !ok {
			continue
		}
		kind, known := catalogue[slug]
		if !known {
			t.Errorf("docs/errors.md documents %q, which is not a problem type", slug)
			continue
		}
		if seen[slug] {
			t.Errorf("docs/errors.md documents %q twice", slug)
		}
		seen[slug] = true
		retry := "not retryable"
		if kind.retryable {
			retry = "retryable"
		}
		want := fmt.Sprintf("Status `%d` · %s · %s.", kind.status, kind.title, retry)
		if i+1 >= len(lines) || lines[i+1] != want {
			t.Errorf("docs/errors.md %s: the line under the heading must read %q", slug, want)
		}
	}
	for slug := range catalogue {
		if !seen[slug] {
			t.Errorf("docs/errors.md has no section for %q", slug)
		}
	}
	if len(seen) == 0 {
		t.Fatal("docs/errors.md has no section at all")
	}
}
