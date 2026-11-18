package roads_test

import (
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The § 23 Abs. 1c StVO notice is English for "en" and the German original
// for every other language.
func TestSpeedNoticeByLanguage(t *testing.T) {
	en, de := roads.SpeedNotice("en"), roads.SpeedNotice("de")
	if !strings.HasPrefix(en, "Note on use (§ 23 Abs. 1c StVO)") || !strings.HasSuffix(en, "Not legal advice.") ||
		!strings.HasSuffix(de, "Keine Rechtsberatung.") || roads.SpeedNotice("fr") != de || en == de {
		t.Fatalf("en %q, de %q", en, de)
	}
}
