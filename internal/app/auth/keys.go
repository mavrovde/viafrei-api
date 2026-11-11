package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Change is one lifecycle action as the access store records it: the change
// and its audit record are written in one transaction (SEC-044). An audit
// record never holds the secret, the MAC or a client address.
type Change struct {
	Action string
	KeyID  string
	Actor  string
	Reason string
	At     time.Time
}

// The lifecycle actions.
const (
	ActionPlan    = "plan_put"
	ActionMove    = "plan_move"
	ActionIssue   = "issue"
	ActionRotate  = "rotate"
	ActionRevoke  = "revoke"
	ActionAccept  = "accept_terms"
	ActionPruned  = "audit_pruned"
	ActionUsage   = "usage_pruned"
	maxTextLength = 200
	// maxLimit bounds a plan's rate and burst, as the access store does.
	maxLimit = 100000
)

// Ledger is the write side of the access store the lifecycle needs.
type Ledger interface {
	Store
	Directory
	PutPlan(ctx context.Context, c Change, p access.Plan) error
	Issue(ctx context.Context, c Change, rec access.Record, label, contact string) error
	Rotate(ctx context.Context, c Change, mac []byte, previousUntil time.Time) error
	Revoke(ctx context.Context, c Change) error
	Move(ctx context.Context, c Change, plan string) error
	Accept(ctx context.Context, c Change, a access.Acceptance) error
	PruneAudit(ctx context.Context, c Change, before time.Time) (int64, error)
	// PruneUsage removes usage records older than keepDays days, counted
	// by the store's own clock so no clock skew can cross its floor.
	PruneUsage(ctx context.Context, c Change, keepDays int) (int64, error)
	// Usage reads the usage records from since on, of one key or (id
	// empty) of every key, oldest first (RATE-014).
	Usage(ctx context.Context, id string, since time.Time) ([]metering.Row, error)
}

// Keys runs the key lifecycle.
type Keys struct {
	s      Settings
	ledger Ledger
	rand   io.Reader
	now    func() time.Time
}

// NewKeys returns the lifecycle over ledger. random and now default to
// crypto/rand and time.Now.
func NewKeys(s Settings, ledger Ledger, random io.Reader, now func() time.Time) *Keys {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = cryptoRand
	}
	return &Keys{s: s, ledger: ledger, rand: random, now: now}
}

// ErrInput is a lifecycle request the operator must correct.
var ErrInput = errors.New("auth: invalid request")

func text(name, v string) error {
	if strings.TrimSpace(v) == "" || len(v) > maxTextLength {
		return fmt.Errorf("%w: %s must be 1 to %d characters", ErrInput, name, maxTextLength)
	}
	return nil
}

// PutPlan creates or changes a plan. A change reaches keys of the plan within
// auth.revocation_propagation (SEC-022).
func (k *Keys) PutPlan(ctx context.Context, p access.Plan, actor string) error {
	if err := text("actor", actor); err != nil {
		return err
	}
	if err := text("plan", p.Name); err != nil {
		return err
	}
	if !slices.Contains(access.UseClasses, p.UseClass) {
		return fmt.Errorf("%w: use class %q is not one of %v", ErrInput, p.UseClass, access.UseClasses)
	}
	if !slices.Contains(access.Tiers, p.Tier) {
		return fmt.Errorf("%w: tier %q is not one of %v", ErrInput, p.Tier, access.Tiers)
	}
	for _, s := range p.Scopes {
		if !slices.Contains(access.Scopes, s) {
			return fmt.Errorf("%w: scope %q is not one of %v", ErrInput, s, access.Scopes)
		}
	}
	if p.Rate < 1 || p.Rate > maxLimit || p.Burst < 1 || p.Burst > maxLimit {
		return fmt.Errorf("%w: rate and burst must be 1 to %d", ErrInput, maxLimit)
	}
	if p.QuotaDay < 0 || p.QuotaMonth < 0 {
		return fmt.Errorf("%w: a quota is 0 (none) or more", ErrInput)
	}
	return k.ledger.PutPlan(ctx, Change{Action: ActionPlan, Actor: actor, At: k.now()}, p)
}

// Issue creates a key and returns it in full: the only time it is ever shown
// (SEC-012). expiry 0 means auth.default_expiry; the lifetime is held to
// 1 095 days (SEC-040).
func (k *Keys) Issue(ctx context.Context, plan, label, contact string, expiry time.Duration, actor string) (access.Key, error) {
	for _, f := range []struct{ name, v string }{{"actor", actor}, {"plan", plan}, {"label", label}, {"contact", contact}} {
		if err := text(f.name, f.v); err != nil {
			return access.Key{}, err
		}
	}
	if expiry == 0 {
		expiry = k.s.DefaultExpiry
	}
	if expiry < k.s.MinExpiry || expiry > k.s.MaxExpiry {
		return access.Key{}, fmt.Errorf("%w: expiry must be between %s and %s", ErrInput, k.s.MinExpiry, k.s.MaxExpiry)
	}
	key, err := access.Generate(k.rand, k.s.Environment, k.s.Prefixes)
	if err != nil {
		return access.Key{}, err
	}
	now := k.now()
	rec := access.Record{
		ID: key.ID, Environment: k.s.Environment, Plan: access.Plan{Name: plan},
		MAC: access.MAC(k.s.Peppers.Current, key.ID, key.Secret), ExpiresAt: now.Add(expiry),
	}
	if err := k.ledger.Issue(ctx, Change{Action: ActionIssue, KeyID: key.ID, Actor: actor, At: now}, rec, label, contact); err != nil {
		return access.Key{}, err
	}
	return key, nil
}

// Rotate gives the key a new secret under the same id; the old one stays
// valid for auth.rotation_overlap (SEC-041).
func (k *Keys) Rotate(ctx context.Context, id, actor string) (access.Key, error) {
	if err := text("actor", actor); err != nil {
		return access.Key{}, err
	}
	rec, err := k.ledger.Lookup(ctx, id)
	if err != nil {
		return access.Key{}, err
	}
	if rec.RevokedAt != nil || !k.now().Before(rec.ExpiresAt) {
		return access.Key{}, fmt.Errorf("%w: a revoked or expired key cannot be rotated", ErrInput)
	}
	key, err := access.NewSecret(k.rand, rec.Environment, k.s.Prefixes, id)
	if err != nil {
		return access.Key{}, err
	}
	now := k.now()
	mac := access.MAC(k.s.Peppers.Current, id, key.Secret)
	if err := k.ledger.Rotate(ctx, Change{Action: ActionRotate, KeyID: id, Actor: actor, At: now}, mac, now.Add(k.s.RotationOverlap)); err != nil {
		return access.Key{}, err
	}
	return key, nil
}

// Revoke ends a key at once, with a reason (SEC-042).
func (k *Keys) Revoke(ctx context.Context, id, reason, actor string) error {
	for _, f := range []struct{ name, v string }{{"actor", actor}, {"reason", reason}} {
		if err := text(f.name, f.v); err != nil {
			return err
		}
	}
	return k.ledger.Revoke(ctx, Change{Action: ActionRevoke, KeyID: id, Actor: actor, Reason: reason, At: k.now()})
}

// Accept records that the key holder accepted version of a source family's
// terms (SEC-034).
func (k *Keys) Accept(ctx context.Context, id, family, version, actor string) error {
	for _, f := range []struct{ name, v string }{{"actor", actor}, {"source", family}, {"version", version}} {
		if err := text(f.name, f.v); err != nil {
			return err
		}
	}
	return k.ledger.Accept(ctx, Change{Action: ActionAccept, KeyID: id, Actor: actor, At: k.now()}, access.Acceptance{Source: family, Version: version})
}

// Prune removes audit records older than audit.retention_days (SEC-045).
func (k *Keys) Prune(ctx context.Context, actor string) (int64, error) {
	if err := text("actor", actor); err != nil {
		return 0, err
	}
	now := k.now()
	return k.ledger.PruneAudit(ctx, Change{Action: ActionPruned, Actor: actor, At: now}, now.Add(-k.s.AuditRetention))
}

// PruneUsage removes usage records older than metering.retention_days; the
// removal is audited.
func (k *Keys) PruneUsage(ctx context.Context, actor string) (int64, error) {
	if err := text("actor", actor); err != nil {
		return 0, err
	}
	now := k.now()
	return k.ledger.PruneUsage(ctx, Change{Action: ActionUsage, Actor: actor, At: now}, k.s.UsageRetentionDays)
}

// Usage reads the usage of the last window, of one key or (id empty) of
// every key: what the maintainers read the metering by (RATE-014).
func (k *Keys) Usage(ctx context.Context, id string, window time.Duration) ([]metering.Row, error) {
	if window <= 0 {
		return nil, fmt.Errorf("%w: the window must be longer than zero", ErrInput)
	}
	return k.ledger.Usage(ctx, id, k.now().Add(-window).UTC().Truncate(time.Hour))
}

// Move puts a key on another plan; the change reaches every instance within
// auth.revocation_propagation (SEC-022).
func (k *Keys) Move(ctx context.Context, id, plan, actor string) error {
	for _, f := range []struct{ name, v string }{{"actor", actor}, {"plan", plan}} {
		if err := text(f.name, f.v); err != nil {
			return err
		}
	}
	return k.ledger.Move(ctx, Change{Action: ActionMove, KeyID: id, Actor: actor, At: k.now()}, plan)
}
