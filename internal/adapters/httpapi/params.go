package httpapi

import (
	"errors"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/number"
)

// strictQuery refuses a query parameter the operation does not declare, and
// a declared one given more than once, before the handler runs: inputs are
// held to an allow-list (NFR-SEC-002), and every violation is listed, not
// only the first (ERR-005). Lists are comma-separated (CONV-002), so a
// repeated parameter is never a list. A declared number parameter must be
// finite: NaN passes every minimum and maximum, so the schema check alone
// lets it through to the handler (#149, #150).
func strictQuery(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		declared := map[string]bool{}
		numeric := map[string]bool{}
		for _, p := range ctx.Operation().Parameters {
			if p.In == "query" {
				declared[p.Name] = true
				numeric[p.Name] = p.Schema != nil && p.Schema.Type == huma.TypeNumber
			}
		}
		u := ctx.URL()
		q := u.Query()
		names := make([]string, 0, len(q))
		for n := range q {
			names = append(names, n)
		}
		slices.Sort(names)
		var errs []error
		for _, n := range names {
			switch {
			case !declared[n] && n == access.AcceptParameter:
				// Terms are accepted per request, and only an operation
				// whose data they gate reads them: elsewhere the
				// parameter is ignored, so a caller may send it on every
				// request.
			case !declared[n]:
				errs = append(errs, &huma.ErrorDetail{Location: "query." + n, Message: "unknown parameter"})
			case len(q[n]) > 1:
				errs = append(errs, &huma.ErrorDetail{Location: "query." + n, Message: "repeated parameter"})
			case q[n][0] == "":
				// An empty value is no value: refused, as an empty at= is,
				// never read as the default (#153).
				errs = append(errs, &huma.ErrorDetail{Location: "query." + n, Message: "empty value"})
			case numeric[n] && !finite(q[n][0]):
				errs = append(errs, &huma.ErrorDetail{Location: "query." + n, Message: "expected a finite number", Value: q[n][0]})
			}
		}
		if len(errs) > 0 {
			_ = huma.WriteErr(api, ctx, http.StatusBadRequest, "invalid parameters", errs...)
			return
		}
		next(ctx)
	}
}

// finite is false for a value that reads as NaN or an infinity. A value that
// is no number at all is left to the operation's own parameter check, which
// names it an invalid float.
func finite(v string) bool {
	_, err := number.ParseFinite(v)
	return !errors.Is(err, number.ErrNotFinite)
}
