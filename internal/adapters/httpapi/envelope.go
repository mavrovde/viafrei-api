package httpapi

import (
	"context"
	"time"
)

// ResponseMeta is the meta member of every enveloped answer (ENV-002). Data
// members (freshness, attribution, notes, page, warnings, query) join it with
// the first data operation.
type ResponseMeta struct {
	RequestID   string    `json:"requestId" doc:"The request id, equal to the X-Request-Id header."`
	GeneratedAt time.Time `json:"generatedAt" doc:"When the service produced this body."`
	Language    string    `json:"language" doc:"The language of the texts in data (BCP 47)."`
}

// Links is the links member of every enveloped answer.
type Links struct {
	Self string `json:"self" format:"uri" doc:"This response's own URL."`
}

// defaultLanguage is the response language until negotiation arrives.
const defaultLanguage = "en"

func (s *server) envelopeMeta(ctx context.Context) ResponseMeta {
	return ResponseMeta{
		RequestID:   RequestIDFrom(ctx),
		GeneratedAt: s.now().UTC().Truncate(time.Second),
		Language:    defaultLanguage,
	}
}

func (s *server) selfLink(ctx context.Context) Links {
	return Links{Self: s.cfg.Server.PublicBaseURL + requestFrom(ctx).path}
}
