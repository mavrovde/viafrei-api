package parity_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/parity"
)

// TestTheLockedBundlesOfOtherRulesAreRefused holds the pinned bundles of
// test/parity to the rule set: 2.5.23 was projected with rules/3 and stays
// pinned as that version's record, so Load must refuse it rather than
// compare it, and 2.5.24 is rules/4 and loads.
func TestTheLockedBundlesOfOtherRulesAreRefused(t *testing.T) {
	fsys := os.DirFS("../../../test/parity")
	_, err := parity.Load(fsys, "2.5.23")
	if !errors.Is(err, parity.ErrMalformed) || !strings.Contains(err.Error(), `rules "viafrei.parity.rules/3"`) {
		t.Errorf("the rules/3 bundle of 2.5.23: %v", err)
	}
	if b, err := parity.Load(fsys, "2.5.24"); err != nil || b.Rules != parity.Default.ID {
		t.Errorf("the 2.5.24 bundle: %v", err)
	}
}
