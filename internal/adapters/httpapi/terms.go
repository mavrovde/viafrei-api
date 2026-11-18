package httpapi

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Terms acceptance is per request (owner 2026-11-16): an operation whose data
// a family's terms gate takes accept_terms; every other operation ignores it
// (strictQuery lets it through unread). Nothing is stored with the key.

// TermsAcceptance is the accept_terms parameter of an operation whose data
// terms gate.
type TermsAcceptance struct {
	AcceptTerms string `query:"accept_terms" maxLength:"200" example:"sid-terms-1" doc:"The terms the end user has accepted, comma-separated, each as meta.terms names it (accept). Pass it only after showing the user the terms' text (meta.terms) and the user accepting them, as the MCP server asks by elicitation; never on the user's behalf. Without it, the data the terms gate is withheld and meta.terms carries their text. Terms that are not current are refused (terms-not-accepted) with the current text. Nothing is stored with the key: every request states its own acceptance."`
	accepted    []string
}

// Resolve reads the accepted terms (huma calls it after the schema checks).
func (a *TermsAcceptance) Resolve(huma.Context) []error {
	v, err := access.ParseAcceptance(a.AcceptTerms)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "query." + access.AcceptParameter, Message: err.Error(), Value: a.AcceptTerms}}
	}
	a.accepted = v
	return nil
}

// with returns ctx carrying the request's acceptances for the licence gate.
func (a *TermsAcceptance) with(ctx context.Context) context.Context {
	return access.WithAccepted(ctx, a.accepted)
}

// TermsOffer is terms a request may accept: their text, verbatim as the site
// and the MCP server show it, and the value that accepts them.
type TermsOffer struct {
	Parameter        string      `json:"parameter" enum:"accept_terms" doc:"The query parameter that accepts the terms."`
	Accept           string      `json:"accept" example:"sid-terms-1" doc:"The value of the parameter that accepts these terms."`
	TermsID          string      `json:"id"`
	Version          string      `json:"version"`
	EffectiveOn      string      `json:"inForceFrom" format:"date" doc:"The day this version is in force from."`
	ProviderTermsURL string      `json:"providerTerms" format:"uri" doc:"The provider's own terms, which bind the recipient as well."`
	Texts            []TermsBody `json:"texts" doc:"The text per language; show it to the end user verbatim before accepting. The binding text is marked."`
}

// TermsBody is one language's text of terms.
type TermsBody struct {
	Lang    string `json:"lang" doc:"BCP 47 language tag."`
	Binding bool   `json:"binding" doc:"True on the binding text (German); any other is a translation."`
	Heading string `json:"heading"`
	Body    string `json:"body"`
	URL     string `json:"url" format:"uri" doc:"The page the terms are published on."`
}

// termsOffers groups the texts by terms, in the order read.
func termsOffers(texts []access.TermsText) []TermsOffer {
	var out []TermsOffer
	for _, t := range texts {
		if len(out) == 0 || out[len(out)-1].Accept != t.Gate {
			out = append(out, TermsOffer{
				Parameter: access.AcceptParameter, Accept: t.Gate, TermsID: t.ID, Version: t.Version,
				EffectiveOn: t.EffectiveOn.Format("2006-01-02"), ProviderTermsURL: t.ProviderURL, Texts: []TermsBody{},
			})
		}
		o := &out[len(out)-1]
		o.Texts = append(o.Texts, TermsBody{Lang: t.Lang, Binding: t.Binding, Heading: t.Heading, Body: t.Body, URL: t.URL})
	}
	return out
}

// termsProblem refuses accepted terms that are not current, with the current
// ones; nil for any other error.
func termsProblem(ctx context.Context, err error) *Problem {
	var r *features.TermsRefusal
	if !errors.As(err, &r) {
		return nil
	}
	p := newProblem(ctx, SlugTermsNotAccepted, "The accepted terms are not the current terms; show the user the current text (terms) and pass its accept value once the user accepts it.", 0)
	p.Terms = termsOffers(r.Texts)
	return p
}
