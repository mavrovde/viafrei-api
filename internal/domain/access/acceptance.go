package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Terms acceptance is given per request, never stored on a key (owner
// 2026-11-16): a caller shows a family's terms to its end user and passes
// accept_terms only after that person has accepted them, as the MCP server
// asks its user by elicitation. A request without it is answered without
// the data the terms gate.

// AcceptParameter is the query parameter that carries a request's
// acceptances.
const AcceptParameter = "accept_terms"

// ErrTermsVersion means a request accepted terms that are not the current
// terms of a family that needs accepting: an older version, an unknown
// version, or a family that needs no acceptance. The request is refused, and
// the refusal names the current terms (CurrentTerms).
var ErrTermsVersion = errors.New("access: the accepted terms are not the current terms")

// TermsVersionError is ErrTermsVersion with the current terms of every
// family the request named.
type TermsVersionError struct {
	Current []Terms
}

func (e *TermsVersionError) Error() string { return ErrTermsVersion.Error() }

// Unwrap makes errors.Is(err, ErrTermsVersion) hold.
func (e *TermsVersionError) Unwrap() error { return ErrTermsVersion }

// TermsValue is the accept_terms value that accepts t: family:version.
func TermsValue(t Terms) string { return t.Family + ":" + t.Version }

// ParseAcceptance reads an accept_terms value: comma-separated family:version
// pairs, each family once. Empty is no acceptance.
func ParseAcceptance(raw string) ([]Acceptance, error) {
	if raw == "" {
		return nil, nil
	}
	var out []Acceptance
	for _, part := range strings.Split(raw, ",") {
		family, version, ok := strings.Cut(part, ":")
		if !ok || family == "" || version == "" {
			return nil, fmt.Errorf("%q is not family:version", part)
		}
		if slices.ContainsFunc(out, func(a Acceptance) bool { return a.Source == family }) {
			return nil, fmt.Errorf("%s is accepted twice", family)
		}
		out = append(out, Acceptance{Source: family, Version: version})
	}
	return out, nil
}

// CheckAcceptance refuses acceptances that are not the current terms of a
// family of the register that needs accepting (ErrTermsVersion, as a
// *TermsVersionError naming the current terms of each such family it knows).
func CheckAcceptance(register []Terms, accepted []Acceptance) error {
	var current []Terms
	wrong := false
	for _, a := range accepted {
		i := slices.IndexFunc(register, func(t Terms) bool { return t.Family == a.Source && t.AcceptanceRequired })
		if i < 0 {
			wrong = true
			continue
		}
		current = append(current, register[i])
		wrong = wrong || register[i].Version != a.Version
	}
	if wrong {
		return &TermsVersionError{Current: current}
	}
	return nil
}

type acceptedKey struct{}

// WithAccepted returns ctx carrying a request's acceptances.
func WithAccepted(ctx context.Context, accepted []Acceptance) context.Context {
	return context.WithValue(ctx, acceptedKey{}, accepted)
}

// AcceptedFrom returns the acceptances ctx carries; none when it carries
// none.
func AcceptedFrom(ctx context.Context) []Acceptance {
	a, _ := ctx.Value(acceptedKey{}).([]Acceptance)
	return a
}
