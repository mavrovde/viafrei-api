package access

import "context"

type audienceKey struct{}

// WithAudience records the audience of the key a request is answered for, so
// the reads made for it apply the licence envelope's row rule
// (RowPermitted) for that key.
func WithAudience(ctx context.Context, a Audience) context.Context {
	return context.WithValue(ctx, audienceKey{}, a)
}

// AudienceOf is the audience WithAudience recorded. A context without one,
// or with any audience but AudienceOwn, is public: a read that was never told
// whose key it serves is answered as for any key (fail closed).
func AudienceOf(ctx context.Context) Audience {
	if a, _ := ctx.Value(audienceKey{}).(Audience); a == AudienceOwn {
		return AudienceOwn
	}
	return AudiencePublic
}
