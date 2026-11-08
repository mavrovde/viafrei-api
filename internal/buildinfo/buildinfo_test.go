package buildinfo_test

import (
	"os"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestDevelopmentBuildFacts(t *testing.T) {
	if got := buildinfo.Get(); got.Version == "" || got.Commit == "" {
		t.Fatalf("Get() = %+v; a build always names a version and a commit", got)
	}
}

func TestCommittedPlatformVersionParses(t *testing.T) {
	req.Covers(t, "REL-001")
	b, err := os.ReadFile("platform-version.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := buildinfo.ParsePlatform(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if got != buildinfo.Get().Platform {
		t.Fatalf("Get().Platform = %q, the file says %q", buildinfo.Get().Platform, got)
	}
}

func TestParsePlatform(t *testing.T) {
	req.Covers(t, "REL-001")
	for in, want := range map[string]string{"unsynced\n": "", "2.5.21\n": "2.5.21", " 0.0.0 ": "0.0.0", "10.20.30": "10.20.30"} {
		if got, err := buildinfo.ParsePlatform(in); err != nil || got != want {
			t.Errorf("ParsePlatform(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "v2.5.21", "2.5", "2.5.21-rc.1", "2.5.21+b", "02.5.1", "2.5.21\n2.5.22", "Unsynced"} {
		if got, err := buildinfo.ParsePlatform(in); err == nil {
			t.Errorf("ParsePlatform(%q) = %q, want a refusal", in, got)
		}
	}
}
