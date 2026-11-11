package console

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// The audited console actions; a key change made in the console is audited
// under the key lifecycle's own action with the owner as the actor.
const (
	ActionEnrolment      = "console_enrolment"
	ActionPasskeyAdded   = "console_passkey_added"
	ActionPasskeyRemoved = "console_passkey_removed"
	ActionSignIn         = "console_sign_in"
)

// ActorPrefix marks the actor of every console action in the audit log.
const ActorPrefix = "console:"

// Passkey is an enrolled owner credential: its public key and counters,
// never a private key or a secret.
type Passkey struct {
	ID         []byte
	Owner      string
	UserHandle []byte
	PublicKey  []byte
	SignCount  uint32
	Flags      byte
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Name is the passkey's printable id: its credential id, base64url.
func (p Passkey) Name() string { return base64.RawURLEncoding.EncodeToString(p.ID) }

// Fingerprint is a short, stable handle of a credential id for the audit log.
func Fingerprint(id []byte) string {
	sum := sha256.Sum256(id)
	return hex.EncodeToString(sum[:6])
}

// Enrolment is an enrolment code's state as stored.
type Enrolment struct {
	Owner string
	// UserHandle is the owner's WebAuthn user handle when the owner already
	// has a passkey; nil otherwise.
	UserHandle []byte
	ExpiresAt  time.Time
	Used       bool
}

// The owners' refusals.
var (
	ErrCode           = errors.New("console: unknown, used or expired enrolment code")
	ErrUnknownPasskey = errors.New("console: no such passkey")
	// ErrCounter is a sign-in whose signature counter does not advance the
	// stored one: a replay, a cloned authenticator or a concurrent sign-in.
	ErrCounter = errors.New("console: the signature counter did not advance")
	ErrInput   = errors.New("console: invalid request")
)

// Store is the access store's console side. Every write and its audit record
// are one transaction (SEC-044).
type Store interface {
	OfferEnrolment(ctx context.Context, c auth.Change, codeMAC []byte, owner string, expires time.Time) error
	// Enrolment returns the code's state, or ErrCode when no such code exists.
	Enrolment(ctx context.Context, codeMAC []byte) (Enrolment, error)
	// AddPasskey stores p and marks the code used, only if it is unused and
	// unexpired at c.At; otherwise ErrCode.
	AddPasskey(ctx context.Context, c auth.Change, codeMAC []byte, p Passkey) error
	// Passkey returns one passkey, or ErrUnknownPasskey.
	Passkey(ctx context.Context, id []byte) (Passkey, error)
	// SignedIn records a sign-in: the new counter and flags, the instant.
	// The counter only moves forward (a passkey that keeps no counter stays
	// at 0); any other write, or a passkey that is gone, is ErrCounter.
	SignedIn(ctx context.Context, c auth.Change, id []byte, signCount uint32, flags byte) error
	Passkeys(ctx context.Context) ([]Passkey, error)
	// RemovePasskey removes one passkey, or answers ErrUnknownPasskey.
	RemovePasskey(ctx context.Context, c auth.Change, id []byte) error
}

// OwnerSettings are the owners' lifecycle settings.
type OwnerSettings struct {
	// Pepper MACs an enrolment code before it is stored (SEC-010).
	Pepper       []byte
	EnrolmentTTL time.Duration
}

// Owners runs the owners' passkey lifecycle.
type Owners struct {
	s     OwnerSettings
	store Store
	rand  io.Reader
	now   func() time.Time
}

// NewOwners returns the lifecycle over store. random and now default to
// crypto/rand and time.Now.
func NewOwners(s OwnerSettings, store Store, random io.Reader, now func() time.Time) *Owners {
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &Owners{s: s, store: store, rand: random, now: now}
}

var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)

// codeLabel separates enrolment-code MACs from key MACs under one pepper.
const codeLabel = "console-enrolment"

func (o *Owners) mac(code string) []byte { return access.MAC(o.s.Pepper, codeLabel, code) }

// Offer creates an enrolment code for owner and returns it: the only time it
// is shown. It is valid once, for admin.enrolment_ttl.
func (o *Owners) Offer(ctx context.Context, owner, actor string) (string, error) {
	if !ownerPattern.MatchString(owner) {
		return "", fmt.Errorf("%w: the owner name is 1 to 32 of a-z, 0-9, '.', '_' and '-', starting with a letter", ErrInput)
	}
	if actor == "" || len(actor) > 200 {
		return "", fmt.Errorf("%w: actor must be 1 to 200 characters", ErrInput)
	}
	b := make([]byte, 32)
	if _, err := io.ReadFull(o.rand, b); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	now := o.now()
	c := auth.Change{Action: ActionEnrolment, Actor: actor, Reason: "owner " + owner, At: now}
	if err := o.store.OfferEnrolment(ctx, c, o.mac(code), owner, now.Add(o.s.EnrolmentTTL)); err != nil {
		return "", err
	}
	return code, nil
}

// Enrolment returns the open enrolment of code, or ErrCode when the code is
// unknown, used or expired.
func (o *Owners) Enrolment(ctx context.Context, code string) (Enrolment, error) {
	if code == "" || len(code) > 64 {
		return Enrolment{}, ErrCode
	}
	e, err := o.store.Enrolment(ctx, o.mac(code))
	if err != nil {
		return Enrolment{}, err
	}
	if e.Used || !o.now().Before(e.ExpiresAt) {
		return Enrolment{}, ErrCode
	}
	return e, nil
}

// Enrol stores the owner's new passkey and uses up code. The owner is the
// actor.
func (o *Owners) Enrol(ctx context.Context, code string, p Passkey) error {
	now := o.now()
	p.CreatedAt = now
	c := auth.Change{Action: ActionPasskeyAdded, Actor: ActorPrefix + p.Owner, Reason: "passkey " + Fingerprint(p.ID), At: now}
	return o.store.AddPasskey(ctx, c, o.mac(code), p)
}

// Passkey returns one passkey, or ErrUnknownPasskey.
func (o *Owners) Passkey(ctx context.Context, id []byte) (Passkey, error) {
	return o.store.Passkey(ctx, id)
}

// SignedIn records a verified sign-in with the passkey's new counter and
// flags; the owner is the actor.
func (o *Owners) SignedIn(ctx context.Context, p Passkey, signCount uint32, flags byte) error {
	c := auth.Change{Action: ActionSignIn, Actor: ActorPrefix + p.Owner, Reason: "passkey " + Fingerprint(p.ID), At: o.now()}
	return o.store.SignedIn(ctx, c, p.ID, signCount, flags)
}

// Passkeys lists every enrolled passkey.
func (o *Owners) Passkeys(ctx context.Context) ([]Passkey, error) { return o.store.Passkeys(ctx) }

// PasskeysOf lists the passkeys owner holds.
func (o *Owners) PasskeysOf(ctx context.Context, owner string) ([]Passkey, error) {
	all, err := o.store.Passkeys(ctx)
	if err != nil {
		return nil, err
	}
	var held []Passkey
	for _, p := range all {
		if p.Owner == owner {
			held = append(held, p)
		}
	}
	return held, nil
}

// Remove removes the passkey named name (its base64url credential id).
func (o *Owners) Remove(ctx context.Context, name, actor string) error {
	id, err := base64.RawURLEncoding.DecodeString(name)
	if err != nil || len(id) == 0 {
		return fmt.Errorf("%w: the passkey id is the base64url credential id console passkeys prints", ErrInput)
	}
	if actor == "" || len(actor) > 200 {
		return fmt.Errorf("%w: actor must be 1 to 200 characters", ErrInput)
	}
	c := auth.Change{Action: ActionPasskeyRemoved, Actor: actor, Reason: "passkey " + Fingerprint(id), At: o.now()}
	return o.store.RemovePasskey(ctx, c, id)
}
