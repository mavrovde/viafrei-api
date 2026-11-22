package store

import (
	"errors"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// TestLevelOf takes a stored level of 0 (an advance notice) to 4, and
// refuses one outside, naming the warning, rather than re-grading it.
func TestLevelOf(t *testing.T) {
	if l, err := levelOf("w", 0); err != nil || l != 0 {
		t.Errorf("level 0: %d %v", l, err)
	}
	if _, err := levelOf("w", 5); !errors.Is(err, weather.ErrLevel) || err.Error() != "weather warning w, level 5: "+weather.ErrLevel.Error() {
		t.Errorf("level 5: %v", err)
	}
}
