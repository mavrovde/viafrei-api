package httpapi

import (
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
)

// strictQuery refuses a query parameter the operation does not declare, and
// a declared one given more than once, before the handler runs: inputs are
// held to an allow-list (NFR-SEC-002), and every violation is listed, not
// only the first (ERR-005). Lists are comma-separated (CONV-002), so a
// repeated parameter is never a list.
func strictQuery(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		declared := map[string]bool{}
		for _, p := range ctx.Operation().Parameters {
			if p.In == "query" {
				declared[p.Name] = true
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
			case !declared[n]:
				errs = append(errs, &huma.ErrorDetail{Location: "query." + n, Message: "unknown parameter"})
			case len(q[n]) > 1:
				errs = append(errs, &huma.ErrorDetail{Location: "query." + n, Message: "repeated parameter"})
			}
		}
		if len(errs) > 0 {
			_ = huma.WriteErr(api, ctx, http.StatusBadRequest, "invalid parameters", errs...)
			return
		}
		next(ctx)
	}
}
