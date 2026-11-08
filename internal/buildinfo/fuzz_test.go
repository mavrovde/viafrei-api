package buildinfo_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/buildinfo"
)

// FuzzParsePlatform (TEST-006, TST-016): platform-version.txt is written by
// the version sync. Any content either parses or is refused, never a crash;
// what parses is unsynced ("") or a version that parses back to itself
// (round trip), with no surrounding space.
// Seed corpus: testdata/fuzz/FuzzParsePlatform (test/fuzz/targets.tsv).
func FuzzParsePlatform(f *testing.F) {
	for _, s := range []string{"unsynced\n", "2.5.21\n", " 0.0.0 ", "v2.5.21", "2.5", "2.5.21-rc.1", "02.5.1", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := buildinfo.ParsePlatform(s)
		if err != nil {
			if v != "" {
				t.Fatalf("%q refused but returned %q", s, v)
			}
			return
		}
		if v == "" {
			return
		}
		back, err := buildinfo.ParsePlatform(v)
		if err != nil || back != v {
			t.Fatalf("%q parsed to %q, which parses back to %q, %v", s, v, back, err)
		}
	})
}
