package access

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Terms acceptance is given per request, never stored on a key (owner
// 2026-11-16): a caller shows a family's terms to its end user and passes
// accept_terms only after that person has accepted them, as the MCP server
// asks its user by elicitation. A request without it is answered without
// the data the terms gate.
//
// A family that needs accepting names its terms by their gate, the terms id
// and version joined by a hyphen (sid-terms-1): the value a request passes.

// AcceptParameter is the query parameter that carries a request's
// acceptances: comma-separated terms gates.
const AcceptParameter = "accept_terms"

// gateForm is the form of a terms gate: the terms id, a hyphen, the version.
var gateForm = regexp.MustCompile(`^[a-z0-9-]+-[0-9]+$`)

// ErrTermsVersion means a request accepted terms that are not the current
// terms of a family that needs accepting: an older version, an unknown
// terms id, or terms no family needs. The request is refused, and the
// refusal names the current terms (TermsVersionError.Current).
var ErrTermsVersion = errors.New("access: the accepted terms are not the current terms")

// TermsVersionError is ErrTermsVersion with the current terms gates of every
// family that needs accepting.
type TermsVersionError struct {
	Current []string
}

func (e *TermsVersionError) Error() string { return ErrTermsVersion.Error() }

// Unwrap makes errors.Is(err, ErrTermsVersion) hold.
func (e *TermsVersionError) Unwrap() error { return ErrTermsVersion }

// ParseAcceptance reads an accept_terms value: comma-separated terms gates,
// each once. Empty is no acceptance.
func ParseAcceptance(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	for _, g := range strings.Split(raw, ",") {
		if !gateForm.MatchString(g) {
			return nil, fmt.Errorf("%q is not a terms id and version, such as sid-terms-1", g)
		}
		if slices.Contains(out, g) {
			return nil, fmt.Errorf("%s is accepted twice", g)
		}
		out = append(out, g)
	}
	return out, nil
}

// TermsGates returns the sorted current terms gates of the register's
// families that need accepting, each once.
func TermsGates(register []Terms) []string {
	var out []string
	for _, t := range register {
		if t.AcceptanceRequired && !slices.Contains(out, t.Version) {
			out = append(out, t.Version)
		}
	}
	slices.Sort(out)
	return out
}

// CheckAcceptance refuses acceptances that are not the current terms of a
// family of the register that needs accepting (ErrTermsVersion, as a
// *TermsVersionError naming the current terms).
func CheckAcceptance(register []Terms, accepted []string) error {
	current := TermsGates(register)
	for _, a := range accepted {
		if !slices.Contains(current, a) {
			return &TermsVersionError{Current: current}
		}
	}
	return nil
}

type acceptedKey struct{}

// WithAccepted returns ctx carrying a request's acceptances.
func WithAccepted(ctx context.Context, accepted []string) context.Context {
	return context.WithValue(ctx, acceptedKey{}, accepted)
}

// AcceptedFrom returns the acceptances ctx carries; none when it carries
// none.
func AcceptedFrom(ctx context.Context) []string {
	a, _ := ctx.Value(acceptedKey{}).([]string)
	return a
}

// TermsText is one language's text of terms a request accepts, verbatim as
// the read contract holds it: the same text the site and the MCP server show.
type TermsText struct {
	// Gate is the value a request passes in accept_terms to accept them.
	Gate        string
	ID, Version string
	EffectiveOn time.Time
	Lang        string
	// Binding is set on the binding language's text (German).
	Binding       bool
	Heading, Body string
	// URL is the page the terms are published on; ProviderURL the
	// provider's own terms, which bind the recipient as well.
	URL, ProviderURL string
}
