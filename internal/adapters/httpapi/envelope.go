package httpapi

import (
	"context"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
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

// defaultLanguage is the language of an answer that asks none (lang).
const defaultLanguage = "en"

// answerLanguage is the language the texts of the answer under ctx are
// written in: the one its lang parameter asked, else defaultLanguage.
func answerLanguage(ctx context.Context) string {
	if l, ok := features.LanguageOf(ctx); ok {
		return l
	}
	return defaultLanguage
}

func (s *server) envelopeMeta(ctx context.Context) ResponseMeta {
	return ResponseMeta{
		RequestID:   RequestIDFrom(ctx),
		GeneratedAt: s.at().UTC().Truncate(time.Second),
		Language:    answerLanguage(ctx),
	}
}

func (s *server) selfLink(ctx context.Context) Links {
	return Links{Self: s.cfg.Server.PublicBaseURL + requestFrom(ctx).path}
}
