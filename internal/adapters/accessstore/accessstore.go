// Package accessstore is the adapter of the access store: plans, keys, terms
// acceptances and the audit log, read and written through the API's own
// access-store role. Every lifecycle change and its audit record are written
// in one transaction (SEC-044).
package accessstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessdb "github.com/mavrovde/viafrei-api/db/access"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/invariant"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// ErrNotFound is a lifecycle action on a key or plan that does not exist.
var ErrNotFound = errors.New("accessstore: no such key or plan")

// Store is the access store.
type Store struct {
	pool *pgxpool.Pool
}

// PoolConfig translates the configuration into a pool configuration.
func PoolConfig(cfg *config.Config) (*pgxpool.Config, error) {
	pc, err := pgxpool.ParseConfig(cfg.Access.URL.Reveal())
	if err != nil {
		// Never wrap the parse error: it may quote the URL, password included.
		return nil, errors.New("accessstore: access.url is not a valid connection URL")
	}
	pc.MaxConns = int32(cfg.Access.PoolMax) // #nosec G115 -- bounded 1..8 by the declaration
	pc.MinConns = 0
	pc.ConnConfig.ConnectTimeout = cfg.DB.ConnectTimeout
	pc.ConnConfig.RuntimeParams["application_name"] = cfg.DB.ApplicationName
	return pc, nil
}

// Open creates the pool; it does not wait for the store.
func Open(ctx context.Context, cfg *config.Config) (*Store, error) {
	pc, err := PoolConfig(cfg)
	if err != nil {
		return nil, err
	}
	// NewWithConfig does not connect; it fails only on bounds PoolConfig
	// never builds.
	return &Store{pool: invariant.Must(pgxpool.NewWithConfig(ctx, pc))}, nil
}

// Close closes the pool.
func (s *Store) Close() { s.pool.Close() }

// ErrSchema is a store whose schema is not the version this build expects.
var ErrSchema = errors.New("accessstore: the access store is not at the expected schema version")

// CheckSchema verifies the store is migrated to the version this build
// expects.
func (s *Store) CheckSchema(ctx context.Context) error {
	var v int
	if err := s.pool.QueryRow(ctx, `select coalesce(max(version), 0) from access.schema_version`).Scan(&v); err != nil {
		return err
	}
	if v != accessdb.Version {
		return fmt.Errorf("%w: found %d, expected %d", ErrSchema, v, accessdb.Version)
	}
	return nil
}

const lookupSQL = `
select k.key_id, k.environment, k.mac, k.previous_mac, coalesce(k.previous_until, 'epoch'), k.expires_at, k.revoked_at,
       p.plan_name, p.use_class, p.tier, p.scopes, p.sources, p.rate_per_second, p.burst, p.quota_day, p.quota_month,
       coalesce((select array_agg(a.source_family order by a.source_family, a.terms_version) from access.terms_acceptance a where a.key_id = k.key_id), '{}'),
       coalesce((select array_agg(a.terms_version order by a.source_family, a.terms_version) from access.terms_acceptance a where a.key_id = k.key_id), '{}')
from access.api_key k join access.plan p using (plan_name)
where k.key_id = $1`

// Lookup implements auth.Store.
func (s *Store) Lookup(ctx context.Context, id string) (access.Record, error) {
	var (
		r                   access.Record
		env, useClass       string
		scopes, fams, terms []string
	)
	err := s.pool.QueryRow(ctx, lookupSQL, id).Scan(&r.ID, &env, &r.MAC, &r.PreviousMAC, &r.PreviousUntil, &r.ExpiresAt, &r.RevokedAt,
		&r.Plan.Name, &useClass, &r.Plan.Tier, &scopes, &r.Plan.Sources,
		&r.Plan.Rate, &r.Plan.Burst, &r.Plan.QuotaDay, &r.Plan.QuotaMonth, &fams, &terms)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Record{}, auth.ErrUnknown
	}
	if err != nil {
		return access.Record{}, err
	}
	r.Environment, r.Plan.UseClass = access.Environment(env), access.UseClass(useClass)
	for _, sc := range scopes {
		r.Plan.Scopes = append(r.Plan.Scopes, access.Scope(sc))
	}
	for i := range fams {
		r.Accepted = append(r.Accepted, access.Acceptance{Source: fams[i], Version: terms[i]})
	}
	return r, nil
}

// Remac implements auth.Store.
func (s *Store) Remac(ctx context.Context, id string, previous bool, mac []byte) error {
	col := "mac"
	if previous {
		col = "previous_mac"
	}
	// col is one of two constants, never caller text.
	_, err := s.pool.Exec(ctx, `update access.api_key set `+col+` = $2 where key_id = $1`, id, mac)
	return err
}

// ChangedSince implements auth.Store: keys changed after since, and every key
// of a plan changed after since.
func (s *Store) ChangedSince(ctx context.Context, since time.Time) ([]string, time.Time, error) {
	var (
		ids  []string
		mark time.Time
	)
	err := s.pool.QueryRow(ctx, `
select coalesce(array_agg(k.key_id order by k.key_id), '{}'), now()
from access.api_key k join access.plan p using (plan_name)
where k.changed_at > $1 or p.changed_at > $1`, since).Scan(&ids, &mark)
	return ids, mark, err
}

// write runs fn and the audit record of c in one transaction.
func (s *Store) write(ctx context.Context, c auth.Change, planBefore, planAfter *string, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		var key *string
		if c.KeyID != "" {
			key = &c.KeyID
		}
		var reason *string
		if c.Reason != "" {
			reason = &c.Reason
		}
		_, err := tx.Exec(ctx, `insert into access.audit (at, action, key_id, plan_before, plan_after, actor, reason) values ($1, $2, $3, $4, $5, $6, $7)`,
			c.At, c.Action, key, planBefore, planAfter, c.Actor, reason)
		return err
	})
}

func affected(n int64) error {
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

// PutPlan implements auth.Ledger.
func (s *Store) PutPlan(ctx context.Context, c auth.Change, p access.Plan) error {
	scopes := make([]string, len(p.Scopes))
	for i, sc := range p.Scopes {
		scopes[i] = string(sc)
	}
	sources := p.Sources
	if sources == nil {
		sources = []string{}
	}
	return s.write(ctx, c, nil, &p.Name, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
insert into access.plan (plan_name, use_class, tier, scopes, sources, rate_per_second, burst, quota_day, quota_month, changed_at)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
on conflict (plan_name) do update set use_class = excluded.use_class, tier = excluded.tier,
    scopes = excluded.scopes, sources = excluded.sources, rate_per_second = excluded.rate_per_second,
    burst = excluded.burst, quota_day = excluded.quota_day, quota_month = excluded.quota_month,
    changed_at = excluded.changed_at`,
			p.Name, string(p.UseClass), p.Tier, scopes, sources, p.Rate, p.Burst, p.QuotaDay, p.QuotaMonth, c.At)
		return err
	})
}

// Issue implements auth.Ledger.
func (s *Store) Issue(ctx context.Context, c auth.Change, r access.Record, label, contact string) error {
	return s.write(ctx, c, nil, &r.Plan.Name, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
insert into access.api_key (key_id, environment, plan_name, label, contact, mac, issued_at, expires_at, changed_at)
values ($1, $2, $3, $4, $5, $6, $7, $8, $7)`, r.ID, string(r.Environment), r.Plan.Name, label, contact, r.MAC, c.At, r.ExpiresAt)
		return err
	})
}

// Rotate implements auth.Ledger.
func (s *Store) Rotate(ctx context.Context, c auth.Change, mac []byte, previousUntil time.Time) error {
	return s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
update access.api_key set previous_mac = mac, previous_until = $3, mac = $2, changed_at = $4
where key_id = $1 and revoked_at is null`, c.KeyID, mac, previousUntil, c.At)
		if err != nil {
			return err
		}
		return affected(tag.RowsAffected())
	})
}

// Revoke implements auth.Ledger.
func (s *Store) Revoke(ctx context.Context, c auth.Change) error {
	return s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
update access.api_key set revoked_at = $2, revoke_reason = $3, changed_at = $2
where key_id = $1 and revoked_at is null`, c.KeyID, c.At, c.Reason)
		if err != nil {
			return err
		}
		return affected(tag.RowsAffected())
	})
}

// Accept implements auth.Ledger. The key's change instant moves too, so every
// instance drops its cached copy (SEC-034).
func (s *Store) Accept(ctx context.Context, c auth.Change, a access.Acceptance) error {
	return s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update access.api_key set changed_at = $2 where key_id = $1`, c.KeyID, c.At)
		if err != nil {
			return err
		}
		if err := affected(tag.RowsAffected()); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
insert into access.terms_acceptance (key_id, source_family, terms_version, accepted_at) values ($1, $2, $3, $4)
on conflict do nothing`, c.KeyID, a.Source, a.Version, c.At)
		return err
	})
}

// PruneAudit implements auth.Ledger.
func (s *Store) PruneAudit(ctx context.Context, c auth.Change, before time.Time) (int64, error) {
	var n int64
	err := s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select access.prune_audit($1)`, before).Scan(&n)
	})
	return n, err
}

// AddUsage implements metering.Writer: the rows are added to the stored
// usage in one statement; a row that exists is added to.
func (s *Store) AddUsage(ctx context.Context, rows []metering.Row) error {
	n := len(rows)
	keys, ops, hours := make([]string, n), make([]string, n), make([]time.Time, n)
	cols := [7][]int64{}
	for c := range cols {
		cols[c] = make([]int64, n)
	}
	for i, r := range rows {
		keys[i], hours[i], ops[i] = r.Key, r.Hour, r.Operation
		for c, v := range []int64{r.Requests, r.NotModified, r.RateLimited, r.QuotaExceeded,
			r.Overloaded, r.DeadlineExhausted, r.Storage.Milliseconds()} {
			cols[c][i] = v
		}
	}
	_, err := s.pool.Exec(ctx, addUsageSQL, keys, hours, ops, cols[0], cols[1], cols[2], cols[3], cols[4], cols[5], cols[6])
	return err
}

const addUsageSQL = `
insert into access.usage as u (key_id, hour, operation, requests, not_modified, rate_limited, quota_exceeded,
    overloaded, deadline_exhausted, storage_ms)
select * from unnest($1::text[], $2::timestamptz[], $3::text[], $4::bigint[], $5::bigint[], $6::bigint[],
    $7::bigint[], $8::bigint[], $9::bigint[], $10::bigint[])
on conflict (key_id, hour, operation) do update set
    requests = u.requests + excluded.requests, not_modified = u.not_modified + excluded.not_modified,
    rate_limited = u.rate_limited + excluded.rate_limited, quota_exceeded = u.quota_exceeded + excluded.quota_exceeded,
    overloaded = u.overloaded + excluded.overloaded, deadline_exhausted = u.deadline_exhausted + excluded.deadline_exhausted,
    storage_ms = u.storage_ms + excluded.storage_ms`

// Used implements ratelimit.UsageReader: the quota units a key used since the
// start of its quota day and of its quota month, as recorded. A unit is a
// request both key gates admitted, so the ones they refused are not counted.
func (s *Store) Used(ctx context.Context, key string, dayStart, monthStart time.Time) (day, month int64, err error) {
	err = s.pool.QueryRow(ctx, `
select coalesce(sum(requests - rate_limited - quota_exceeded) filter (where hour >= $2), 0)::bigint,
       coalesce(sum(requests - rate_limited - quota_exceeded) filter (where hour >= $3), 0)::bigint
from access.usage where key_id = $1 and hour >= least($2::timestamptz, $3::timestamptz)`,
		key, dayStart, monthStart).Scan(&day, &month)
	return day, month, err
}

// Usage implements auth.Ledger.
func (s *Store) Usage(ctx context.Context, id string, since time.Time) ([]metering.Row, error) {
	rows, err := s.pool.Query(ctx, `
select key_id, hour, operation, requests, not_modified, rate_limited, quota_exceeded, overloaded,
       deadline_exhausted, storage_ms
from access.usage where hour >= $2 and ($1 = '' or key_id = $1)
order by hour, key_id, operation`, id, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (metering.Row, error) {
		var (
			u  metering.Row
			ms int64
		)
		err := r.Scan(&u.Key, &u.Hour, &u.Operation, &u.Requests, &u.NotModified, &u.RateLimited,
			&u.QuotaExceeded, &u.Overloaded, &u.DeadlineExhausted, &ms)
		u.Hour, u.Storage = u.Hour.UTC(), time.Duration(ms)*time.Millisecond
		return u, err
	})
}

// PruneUsage implements auth.Ledger.
func (s *Store) PruneUsage(ctx context.Context, c auth.Change, keepDays int) (int64, error) {
	var n int64
	err := s.write(ctx, c, nil, nil, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select access.prune_usage($1)`, keepDays).Scan(&n)
	})
	return n, err
}

// Move implements auth.Ledger: the key's plan before and after are audited.
func (s *Store) Move(ctx context.Context, c auth.Change, plan string) error {
	var before string
	if err := s.pool.QueryRow(ctx, `select plan_name from access.api_key where key_id = $1`, c.KeyID).Scan(&before); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return s.write(ctx, c, &before, &plan, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update access.api_key set plan_name = $2, changed_at = $3 where key_id = $1`, c.KeyID, plan, c.At)
		return err
	})
}
