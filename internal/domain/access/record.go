package access

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"slices"
	"time"
)

// MAC is the value stored in place of a secret: HMAC-SHA-256 under the
// server-side pepper over the key id and the secret (SEC-010).
func MAC(pepper []byte, id, secret string) []byte {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(id))
	m.Write([]byte{0})
	m.Write([]byte(secret))
	return m.Sum(nil)
}

// Peppers are the current pepper and, during a pepper rotation, the
// previous one (SEC-011).
type Peppers struct {
	Current, Previous []byte
}

// Scope is a feature family an operation needs (SEC-020).
type Scope string

// Scopes lists every scope.
var Scopes = []Scope{"places", "roads", "energy", "parking", "transit", "accessibility", "weather", "rules", "history"}

// UseClass is the kind of use a plan is for; source terms permit use classes.
type UseClass string

// UseClasses lists every use class.
var UseClasses = []UseClass{"consumer", "non_commercial", "commercial"}

// Tiers lists every priority tier.
var Tiers = []string{"critical", "standard", "bulk"}

// Plan is what a key may call and read. Plans are data in the access store
// (SEC-021).
type Plan struct {
	Name     string
	UseClass UseClass
	Tier     string
	Scopes   []Scope
	// Sources are the source families the plan includes.
	Sources []string
	// Rate and Burst are the plan's token bucket: Rate requests a second,
	// Burst at once (RATE-002).
	Rate, Burst int
	// QuotaDay and QuotaMonth are the units a key may use per quota day
	// and month; 0 means no quota (RATE-003).
	QuotaDay, QuotaMonth int64
}

// Has reports whether the plan grants scope.
func (p Plan) Has(scope Scope) bool { return slices.Contains(p.Scopes, scope) }

// Acceptance is a recorded acceptance of one source family's terms in one
// version (SEC-034).
type Acceptance struct {
	Source, Version string
}

// Record is a stored key: never the secret, only its MACs.
type Record struct {
	ID          string
	Environment Environment
	Plan        Plan
	MAC         []byte
	// PreviousMAC is the secret before the last rotation, valid until
	// PreviousUntil (SEC-041).
	PreviousMAC   []byte
	PreviousUntil time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	Accepted      []Acceptance
}

// The refusals of a key check. ErrInvalid covers an unknown id, a bad
// checksum and a wrong secret alike (SEC-003).
var (
	ErrMissing = errors.New("access: no key presented")
	ErrInvalid = errors.New("access: invalid key")
	ErrExpired = errors.New("access: key expired")
	ErrRevoked = errors.New("access: key revoked")
)

// Match is the result of comparing a presented secret with a record.
type Match struct {
	// Remac is set when the secret matched under the previous pepper: the
	// value to store under the current one, and which stored value it
	// replaces.
	Remac    []byte
	Previous bool
}

// Verify compares secret with the record's MACs at now. Every candidate is
// computed and compared in constant time whatever matches, so the time taken
// does not tell which comparison succeeded (SEC-003).
func Verify(rec Record, secret string, p Peppers, now time.Time) (Match, error) {
	type cand struct {
		stored   []byte
		pepper   []byte
		current  bool // the pepper is the current one
		previous bool // the stored value is the pre-rotation secret
	}
	cands := []cand{{rec.MAC, p.Current, true, false}, {rec.MAC, p.Previous, false, false}}
	overlap := rec.PreviousMAC != nil && now.Before(rec.PreviousUntil)
	cands = append(cands, cand{rec.PreviousMAC, p.Current, true, true}, cand{rec.PreviousMAC, p.Previous, false, true})
	var found *cand
	for i := range cands {
		c := &cands[i]
		ok := len(c.pepper) > 0 && hmac.Equal(MAC(c.pepper, rec.ID, secret), c.stored)
		if ok && (!c.previous || overlap) && found == nil {
			found = c
		}
	}
	if found == nil {
		return Match{}, ErrInvalid
	}
	if rec.RevokedAt != nil {
		return Match{}, ErrRevoked
	}
	if !now.Before(rec.ExpiresAt) {
		return Match{}, ErrExpired
	}
	m := Match{Previous: found.previous}
	if !found.current {
		m.Remac = MAC(p.Current, rec.ID, secret)
	}
	return m, nil
}
