package store

import (
	"context"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// TestOwnKeyFollowsTheKeyAudience: only an own key turns the setting on; a
// read told nothing, or a public key, reads as not own.
func TestOwnKeyFollowsTheKeyAudience(t *testing.T) {
	ctx := context.Background()
	for ctx, want := range map[context.Context]string{
		ctx: "off",
		access.WithAudience(ctx, access.AudiencePublic): "off",
		access.WithAudience(ctx, access.AudienceOwn):    "on",
	} {
		if got := ownKey(ctx); got != want {
			t.Errorf("ownKey(%v) = %s, want %s", access.AudienceOf(ctx), got, want)
		}
	}
	if got := held("f"); !strings.Contains(got, "f.display_class <> 'withhold'") || !strings.Contains(got, "f.hold_reason is null") ||
		!strings.Contains(got, "f.audience = 'public' or current_setting('viafrei_api.own_key', true) = 'on'") {
		t.Errorf("held(f) = %s", got)
	}
}
