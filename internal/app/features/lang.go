package features

import "context"

// Languages are the languages an answer's texts can be asked in: German,
// the platform's own, and English.
var Languages = []string{"de", "en"}

type langKey struct{}

// WithLanguage asks the answers read under ctx in lang, one of Languages,
// as the platform's tools take their language argument per call.
func WithLanguage(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey{}, lang)
}

// LanguageOf is the language asked under ctx with WithLanguage, if any.
func LanguageOf(ctx context.Context) (string, bool) {
	l, ok := ctx.Value(langKey{}).(string)
	return l, ok && l != ""
}

// lang is the language the answer read under ctx speaks: the one asked
// with WithLanguage, else the instance's own.
func (s *Service) lang(ctx context.Context) string {
	if l, ok := LanguageOf(ctx); ok {
		return l
	}
	return s.set.Language
}
