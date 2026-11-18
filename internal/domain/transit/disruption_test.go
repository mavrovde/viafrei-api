package transit_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestFoldLineAsThePlatformComparesIt(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-003")
	for in, want := range map[string]string{"RE 1": "RE1", "s1": "S1", " u\t6 ": "U6", "": ""} {
		if got := transit.FoldLine(in); got != want {
			t.Errorf("FoldLine(%q) = %q, want %q", in, got, want)
		}
	}
}
