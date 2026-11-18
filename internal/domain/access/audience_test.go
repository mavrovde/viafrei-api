package access

import (
	"context"
	"testing"
)

func TestAudienceOfFailsClosed(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		ctx  context.Context
		want Audience
	}{
		"none":    {ctx, AudiencePublic},
		"public":  {WithAudience(ctx, AudiencePublic), AudiencePublic},
		"own":     {WithAudience(ctx, AudienceOwn), AudienceOwn},
		"unknown": {WithAudience(ctx, Audience("staff")), AudiencePublic},
		"empty":   {WithAudience(ctx, ""), AudiencePublic},
	} {
		if got := AudienceOf(c.ctx); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
