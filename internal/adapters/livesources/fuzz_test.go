package livesources_test

import (
	"os"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/livesources"
)

// FuzzParse (TEST-006): a registry document either parses to a registry
// whose every source has a freshness window and a deadline of no less than zero, or is
// refused with no registry. Seed corpus: testdata/fuzz/FuzzParse
// (test/fuzz/targets.tsv).
func FuzzParse(f *testing.F) {
	raw, err := os.ReadFile("../../../test/live/registry.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	for _, s := range []string{"", "{}", `{"schema":1,"sources":[]}`, `{"schema":1,"sources":[{"id":"x","subject":{"type":"object"},"subject_key":{"cases":[{"match":{"a":"."},"emit":"{a}"}]}}]}`, `{"schema":1,"sources":[{"id":"x","subject":{"typo":1}}]}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		reg, err := livesources.Parse(raw)
		if err != nil {
			if reg != nil {
				t.Fatalf("refused with %v but returned a registry", err)
			}
			return
		}
		for _, name := range reg.Names() {
			if reg.FreshFor(name) < 0 || reg.Deadline(name) < 0 {
				t.Fatalf("%s: a negative freshness window or deadline", name)
			}
		}
	})
}
