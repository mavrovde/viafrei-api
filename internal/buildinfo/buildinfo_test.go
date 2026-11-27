package buildinfo_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/buildinfo"
)

func TestDevelopmentBuildFacts(t *testing.T) {
	if got := buildinfo.Get(); got.Version == "" || got.Commit == "" {
		t.Fatalf("Get() = %+v; a build always names a version and a commit", got)
	}
}
