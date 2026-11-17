package httpapi

import (
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
)

// The language of an answer's texts, asked per request as the platform's
// tools take their language argument per call (G1): the provenance notes,
// the rules texts and the notices an answer carries.

const langParameter = "lang"

func langParam() *huma.Param {
	enum := make([]any, len(features.Languages))
	for i, l := range features.Languages {
		enum[i] = l
	}
	return &huma.Param{
		Name: langParameter, In: "query", Example: "en",
		Description: "The language of the answer's texts (" + strings.Join(features.Languages, ", ") + "): the source notes, rules texts and notices. Default: the instance's language, German.",
		Schema:      &huma.Schema{Type: huma.TypeString, Enum: enum},
	}
}

// documentLang declares the lang parameter on a keyed read.
func documentLang(o *huma.Operation) {
	if o != nil {
		o.Parameters = append(o.Parameters, langParam())
	}
}

// askLanguage reads the lang parameter into the request's context, and
// refuses a language no answer is written in.
func askLanguage(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		l := ctx.Query(langParameter)
		if l == "" {
			next(ctx)
			return
		}
		if !slices.Contains(features.Languages, l) {
			_ = huma.WriteErr(api, ctx, http.StatusBadRequest, "invalid parameters",
				&huma.ErrorDetail{Location: "query." + langParameter, Message: "expected one of " + strings.Join(features.Languages, ", "), Value: l})
			return
		}
		next(huma.WithContext(ctx, features.WithLanguage(ctx.Context(), l)))
	}
}
