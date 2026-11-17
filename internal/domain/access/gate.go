package access

import (
	"errors"
	"fmt"
	"slices"
)

// Terms are the licence terms of one source family as the source register
// publishes them.
type Terms struct {
	Family string
	// Domain is the domain the family answers for, such as energy or
	// parking: the families that can answer a feature's question.
	Domain     string
	UseClasses []UseClass
	// AcceptanceRequired families are withheld from a request that has not
	// accepted their terms: Version is then their terms gate (sid-terms-1),
	// the value the request passes in accept_terms.
	AcceptanceRequired bool
	Version            string
	// Audience is who may read the family at all: AudienceOwn families
	// (licences that permit our own display only) are read by ViaFrei's own
	// keys alone. Anything but AudienceOwn is public.
	Audience Audience
	// HoldReason, when set, holds the family back from every key, whatever
	// its plan, audience or acceptances (LIC-009): the read contract states
	// why (for example no_api_terms).
	HoldReason string
}

// Audience is who a key, a source family or a row is for.
type Audience string

// The two audiences of the read contract's licence envelope.
const (
	// AudiencePublic is any key.
	AudiencePublic Audience = "public"
	// AudienceOwn is ViaFrei's own keys only (its web and MCP callers).
	AudienceOwn Audience = "own"
)

// RowPermitted is the licence envelope's rule for one row of the read
// contract, checked for a key of audience key: a row its publisher or the
// steward withholds (display class withhold), a row held back (hold reason
// set) and a row for ViaFrei's own keys read by any other key are never
// served. Any audience but AudienceOwn is public, for a row and a key alike.
func RowPermitted(displayClass string, rowAudience Audience, holdReason string, key Audience) bool {
	if displayClass == "withhold" {
		return false
	}
	if holdReason != "" {
		return false
	}
	return rowAudience != AudienceOwn || key == AudienceOwn
}

// Refusal says why the gate denies one family. The zero value is "allowed".
type Refusal string

// The reasons the gate gives.
const (
	Allowed           Refusal = ""
	NotInPlan         Refusal = "not_in_plan"
	UseClassForbidden Refusal = "use_class_forbidden"
	// Unreviewed is a family whose terms grant no use class yet: it is
	// never served, though ViaFrei's own keys see it listed (Catalogued).
	Unreviewed       Refusal = "unreviewed"
	TermsNotAccepted Refusal = "terms_not_accepted"
	Held             Refusal = "held"
	OwnAudienceOnly  Refusal = "audience_own"
	Unknown          Refusal = "unknown_family"
	Disabled         Refusal = "disabled"
)

// Decide applies the licence gate to one family for a key of plan and
// audience: it is readable only if it is not disabled, not held, for the
// key's audience, in the plan, the plan's use class is permitted by its
// terms, and the request accepted the current terms when they need
// accepting (SEC-030, SEC-034). Anything else is denied.
func Decide(t Terms, plan Plan, audience Audience, accepted []string, disabled []string) Refusal {
	// An if chain, not a switch: a mutation tool reports a switch's case
	// expressions as uncovered, and these are the rules it must mutate.
	if slices.Contains(disabled, t.Family) {
		return Disabled
	}
	if t.HoldReason != "" {
		return Held
	}
	if t.Audience == AudienceOwn && audience != AudienceOwn {
		return OwnAudienceOnly
	}
	if !slices.Contains(plan.Sources, t.Family) {
		return NotInPlan
	}
	if len(t.UseClasses) == 0 {
		return Unreviewed
	}
	if !slices.Contains(t.UseClasses, plan.UseClass) {
		return UseClassForbidden
	}
	if t.AcceptanceRequired && !slices.Contains(accepted, t.Version) {
		return TermsNotAccepted
	}
	return Allowed
}

// Gate is the result of the licence gate for one key: the families it may
// receive and why each other one is refused.
type Gate struct {
	allowed []string
	refused map[string]Refusal
	own     bool
}

// NewGate decides every family of the register for a key of plan and
// audience.
func NewGate(register []Terms, plan Plan, audience Audience, accepted []string, disabled []string) Gate {
	g := Gate{refused: map[string]Refusal{}, own: audience == AudienceOwn}
	for _, t := range register {
		if r := Decide(t, plan, audience, accepted, disabled); r != Allowed {
			g.refused[t.Family] = r
			continue
		}
		g.allowed = append(g.allowed, t.Family)
	}
	slices.Sort(g.allowed)
	return g
}

// Allowed returns the sorted families the key may receive: the set the
// storage read is restricted to (SEC-031).
func (g Gate) Allowed() []string { return slices.Clone(g.allowed) }

// Permits reports whether family may be served.
func (g Gate) Permits(family string) bool {
	_, ok := slices.BinarySearch(g.allowed, family)
	return ok
}

// Refused returns the reason a family is denied; a family missing from the
// register is denied as unknown.
func (g Gate) Refused(family string) Refusal {
	if g.Permits(family) {
		return Allowed
	}
	if r, ok := g.refused[family]; ok {
		return r
	}
	return Unknown
}

// ErrForbiddenItem is the post-check's verdict: storage returned an item of a
// family outside the allowed set (SEC-032). The answer must be aborted.
var ErrForbiddenItem = errors.New("access: an item of a family the key may not receive was read")

// Check verifies every returned item's family against the allowed set again,
// after the storage read (SEC-032).
func (g Gate) Check(families []string) error {
	for _, f := range families {
		if !g.Permits(f) {
			return fmt.Errorf("%w (%d characters)", ErrForbiddenItem, len(f))
		}
	}
	return nil
}

// ErrNonePermitted means none of the families that could answer is permitted
// (403 source-not-permitted). ErrTermsNotAccepted narrows it when acceptance
// is the only thing missing (403 terms-not-accepted).
var (
	ErrNonePermitted    = errors.New("access: no answering source family is permitted")
	ErrTermsNotAccepted = errors.New("access: the terms of the answering source family are not accepted")
)

// Answer narrows the families that could answer a question to the permitted
// ones (SEC-033): partial is set when some but not all are permitted, and an
// error is returned only when none is.
func (g Gate) Answer(answering []string) (permitted []string, partial bool, err error) {
	terms := len(answering) > 0
	for _, f := range answering {
		if g.Permits(f) {
			permitted = append(permitted, f)
			continue
		}
		terms = terms && g.Refused(f) == TermsNotAccepted
	}
	if len(permitted) > 0 {
		return permitted, len(permitted) < len(answering), nil
	}
	if terms {
		return nil, false, ErrTermsNotAccepted
	}
	return nil, false, ErrNonePermitted
}

// Source is one source family's attribution, as a keyed answer carries it.
type Source struct {
	Family          string
	Domain          string
	Publisher       string
	AttributionText string
	Licence         string
	LicenceURL      string
	ShareAlike      bool
	ShareAlikeOffer *string
	TermsURL        *string
}

// Answering returns the families of the register that answer for domain:
// the families that could answer a feature's question, sorted.
func Answering(register []Terms, domain string) []string {
	var out []string
	for _, t := range register {
		if t.Domain == domain {
			out = append(out, t.Family)
		}
	}
	slices.Sort(out)
	return out
}

// Pending returns the families among answering that are refused only because
// their terms are not accepted: such a family's items may be listed, without
// the data its terms protect (SEC-030).
func (g Gate) Pending(answering []string) []string {
	var out []string
	for _, f := range answering {
		if g.Refused(f) == TermsNotAccepted {
			out = append(out, f)
		}
	}
	return out
}

// Catalogued returns the families among answering that a source list names
// although the key may not read them: for ViaFrei's own keys, the families of
// their plan whose terms are not reviewed yet, as the own catalogue lists them
// (the platform's coverage resource). No data of theirs is served to any key
// (SEC-030); any other key gets none.
func (g Gate) Catalogued(answering []string) []string {
	var out []string
	for _, f := range answering {
		if g.own && g.Refused(f) == Unreviewed {
			out = append(out, f)
		}
	}
	return out
}
