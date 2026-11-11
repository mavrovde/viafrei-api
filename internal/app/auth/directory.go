package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// KeyView is what the owner reads about a key: never the secret or a MAC.
type KeyView struct {
	ID           string
	Environment  access.Environment
	Plan         string
	Label        string
	Contact      string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	RevokeReason string
}

// AuditEntry is one audit record as the owner reads it: never a secret, a
// MAC or a client address (SEC-044).
type AuditEntry struct {
	Change
	PlanBefore, PlanAfter string
}

// Directory is the read side of the access store the owner console shows.
type Directory interface {
	// KeyList returns every key, newest first.
	KeyList(ctx context.Context) ([]KeyView, error)
	// PlanList returns every plan by name.
	PlanList(ctx context.Context) ([]access.Plan, error)
	// AuditList returns the newest limit audit records, newest first.
	AuditList(ctx context.Context, limit int) ([]AuditEntry, error)
}

// maxAuditPage bounds one read of the audit log.
const maxAuditPage = 1000

// KeyList returns every key, newest first.
func (k *Keys) KeyList(ctx context.Context) ([]KeyView, error) { return k.ledger.KeyList(ctx) }

// PlanList returns every plan by name.
func (k *Keys) PlanList(ctx context.Context) ([]access.Plan, error) { return k.ledger.PlanList(ctx) }

// AuditList returns the newest limit audit records, newest first.
func (k *Keys) AuditList(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit < 1 || limit > maxAuditPage {
		return nil, fmt.Errorf("%w: the audit page is 1 to %d records", ErrInput, maxAuditPage)
	}
	return k.ledger.AuditList(ctx, limit)
}
