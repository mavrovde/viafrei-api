package accessstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// KeyList implements auth.Directory: every key, newest first, without its
// MACs.
func (s *Store) KeyList(ctx context.Context) ([]auth.KeyView, error) {
	rows, err := s.pool.Query(ctx, `
select key_id, environment, plan_name, label, contact, issued_at, expires_at, revoked_at, coalesce(revoke_reason, '')
from access.api_key order by issued_at desc, key_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (auth.KeyView, error) {
		var (
			k   auth.KeyView
			env string
		)
		err := r.Scan(&k.ID, &env, &k.Plan, &k.Label, &k.Contact, &k.IssuedAt, &k.ExpiresAt, &k.RevokedAt, &k.RevokeReason)
		k.Environment = access.Environment(env)
		return k, err
	})
}

// PlanList implements auth.Directory: every plan by name.
func (s *Store) PlanList(ctx context.Context) ([]access.Plan, error) {
	rows, err := s.pool.Query(ctx, `
select plan_name, use_class, tier, scopes, sources, rate_per_second, burst, quota_day, quota_month
from access.plan order by plan_name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (access.Plan, error) {
		var (
			p      access.Plan
			use    string
			scopes []string
		)
		err := r.Scan(&p.Name, &use, &p.Tier, &scopes, &p.Sources, &p.Rate, &p.Burst, &p.QuotaDay, &p.QuotaMonth)
		p.UseClass = access.UseClass(use)
		for _, sc := range scopes {
			p.Scopes = append(p.Scopes, access.Scope(sc))
		}
		return p, err
	})
}

// AuditList implements auth.Directory: the newest limit audit records.
func (s *Store) AuditList(ctx context.Context, limit int) ([]auth.AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `
select at, action, coalesce(key_id, ''), coalesce(plan_before, ''), coalesce(plan_after, ''), actor, coalesce(reason, '')
from access.audit order by audit_id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (auth.AuditEntry, error) {
		var e auth.AuditEntry
		err := r.Scan(&e.At, &e.Action, &e.KeyID, &e.PlanBefore, &e.PlanAfter, &e.Actor, &e.Reason)
		return e, err
	})
}

// OfferEnrolment implements console.Store.
func (s *Store) OfferEnrolment(ctx context.Context, c auth.Change, codeMAC []byte, owner string, expires time.Time) error {
	return s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		// Used and expired codes are of no further use; they go with the
		// offer of a new one.
		if _, err := tx.Exec(ctx, `delete from access.console_enrolment where used_at is not null or expires_at <= $1`, c.At); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into access.console_enrolment (code_mac, owner_name, expires_at) values ($1, $2, $3)`,
			codeMAC, owner, expires)
		return err
	})
}

// Enrolment implements console.Store. The user handle is the owner's, taken
// from a passkey the owner already holds.
func (s *Store) Enrolment(ctx context.Context, codeMAC []byte) (console.Enrolment, error) {
	var e console.Enrolment
	err := s.pool.QueryRow(ctx, `
select e.owner_name, e.expires_at, e.used_at is not null,
       (select p.user_handle from access.console_passkey p where p.owner_name = e.owner_name limit 1)
from access.console_enrolment e where e.code_mac = $1`, codeMAC).Scan(&e.Owner, &e.ExpiresAt, &e.Used, &e.UserHandle)
	if errors.Is(err, pgx.ErrNoRows) {
		return console.Enrolment{}, console.ErrCode
	}
	return e, err
}

// AddPasskey implements console.Store: the code is used up and the passkey
// stored in one transaction with its audit record.
func (s *Store) AddPasskey(ctx context.Context, c auth.Change, codeMAC []byte, p console.Passkey) error {
	return s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
update access.console_enrolment set used_at = $2
where code_mac = $1 and owner_name = $3 and used_at is null and expires_at > $2`, codeMAC, c.At, p.Owner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return console.ErrCode
		}
		_, err = tx.Exec(ctx, `
insert into access.console_passkey (credential_id, owner_name, user_handle, public_key, sign_count, flags, created_at)
values ($1, $2, $3, $4, $5, $6, $7)`, p.ID, p.Owner, p.UserHandle, p.PublicKey, int64(p.SignCount), int16(p.Flags), p.CreatedAt)
		return err
	})
}

const webauthnColumns = `credential_id, owner_name, user_handle, public_key, sign_count, flags, created_at, last_used_at`

func scanPasskey(r pgx.Row) (console.Passkey, error) {
	var (
		p     console.Passkey
		count int64
		flags int16
	)
	err := r.Scan(&p.ID, &p.Owner, &p.UserHandle, &p.PublicKey, &count, &flags, &p.CreatedAt, &p.LastUsedAt)
	p.SignCount, p.Flags = uint32(count), byte(flags) // #nosec G115 -- held to their ranges by the table's checks
	return p, err
}

// Passkey implements console.Store.
func (s *Store) Passkey(ctx context.Context, id []byte) (console.Passkey, error) {
	p, err := scanPasskey(s.pool.QueryRow(ctx, `select `+webauthnColumns+` from access.console_passkey where credential_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return console.Passkey{}, console.ErrUnknownPasskey
	}
	return p, err
}

// Passkeys implements console.Store.
func (s *Store) Passkeys(ctx context.Context) ([]console.Passkey, error) {
	rows, err := s.pool.Query(ctx, `select `+webauthnColumns+` from access.console_passkey order by owner_name, created_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (console.Passkey, error) { return scanPasskey(r) })
}

// SignedIn implements console.Store.
func (s *Store) SignedIn(ctx context.Context, c auth.Change, id []byte, signCount uint32, flags byte) error {
	return s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		// The counter only moves forward; a passkey that keeps no counter
		// stays at 0. Anything else is refused, also when two sign-ins race.
		tag, err := tx.Exec(ctx, `
update access.console_passkey set sign_count = $2, flags = $3, last_used_at = $4
where credential_id = $1 and (sign_count < $2 or ($2 = 0 and sign_count = 0))`,
			id, int64(signCount), int16(flags), c.At)
		return oneRow(tag, err, console.ErrCounter)
	})
}

// RemovePasskey implements console.Store.
func (s *Store) RemovePasskey(ctx context.Context, c auth.Change, id []byte) error {
	return s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from access.console_passkey where credential_id = $1`, id)
		return oneRow(tag, err, console.ErrUnknownPasskey)
	})
}

// oneRow is the outcome of a statement on one passkey: none is an error,
// no row changed is none.
func oneRow(tag pgconn.CommandTag, err, none error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return none
	}
	return nil
}
