package store

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Register reads the licence terms of every source family the read contract
// publishes: what the licence gate decides on (SEC-030), with the family's
// licence envelope: its audience, its hold and the terms a key accepts (the
// envelope's terms gate, else the register's version).
//
// With a snapshot (db.register_snapshot_ttl), a register read within the snapshot's lifetime
// is answered from that read, without a storage read and so without a
// second admission of the request (RATE-011).
func (s *Store) Register(ctx context.Context) ([]access.Terms, error) {
	if s.reg.ttl <= 0 {
		return s.readRegister(ctx)
	}
	return s.reg.get(ctx, s.nowFn, s.readRegister)
}

// registerSnapshot is the last register read and when it was read. The
// read contract's source register is a view over the platform's register,
// catalogue and rule tables (a pattern match per family), tens of
// milliseconds a read where the contract stub's table took under one; read
// on every request, between the request's admission to its register read and
// its admission to its feature read, it put a shed request's refusal that
// much past the tier's queue_max_wait (RATE-011).
type registerSnapshot = snapshot[access.Terms]

// snapshot is the last read of a slowly changing set of rows and when it was
// read, renewed off the request path every ttl and answered while younger
// than staleFor.
type snapshot[T any] struct {
	ttl      time.Duration
	staleFor time.Duration

	mu     sync.Mutex
	items  []T
	at     time.Time
	flight *flight[T]

	// waiting, when set (tests), is called by a caller that waits for
	// another's read, before it waits.
	waiting func()
}

// flight is a read in progress, which callers arriving meanwhile wait for
// instead of reading again.
type flight[T any] struct {
	done  chan struct{}
	run   func()
	items []T
	err   error
}

// WithRegisterSnapshot answers Register from a read no older than staleFor,
// which KeepRegister renews every ttl; ttl 0 reads storage every time
// (db.register_snapshot_ttl and db.register_stale_for). A change of a
// family's terms or hold reaches answers within ttl plus one read, and never
// later than staleFor.
func (s *Store) WithRegisterSnapshot(ttl, staleFor time.Duration) *Store {
	s.reg.ttl, s.reg.staleFor = ttl, max(ttl, staleFor)
	return s
}

// RegisterSnapshot reports how often the register is read again and how
// long a read answers at most: the bound on a terms change or hold reaching
// answers.
func (s *Store) RegisterSnapshot() (ttl, staleFor time.Duration) {
	return s.reg.ttl, s.reg.staleFor
}

// get answers the snapshot while it is younger than staleFor (KeepRegister
// replaces it every ttl), so no request waits for the register. Without a
// snapshot that young the caller reads, and callers arriving during that
// read wait for it, each no longer than its own context. A failed read is
// not kept.
func (r *snapshot[T]) get(ctx context.Context, now func() time.Time, read func(context.Context) ([]T, error)) ([]T, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.items != nil {
		age := now().Sub(r.at)
		if age < r.staleFor {
			items := slices.Clone(r.items)
			r.mu.Unlock()
			return items, nil
		}
	}
	f := r.flight
	if f == nil {
		f = r.start(ctx, now, read)
		r.mu.Unlock()
		f.run()
		if f.err != nil {
			return nil, f.err
		}
		return slices.Clone(f.items), nil
	}
	r.mu.Unlock()
	if r.waiting != nil {
		r.waiting()
	}
	select {
	case <-f.done:
		if f.err != nil {
			// The read belonged to another request or to the background:
			// its failure (its own deadline, say) is not this one's, so this
			// one reads itself.
			return read(ctx)
		}
		return slices.Clone(f.items), nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrDeadlineExhausted, ctx.Err())
	}
}

// KeepRegister reads the register at once and again every ttl until ctx
// ends, so requests find a snapshot younger than ttl and none reads the
// register itself: a register read on a request's path is a second admission
// of the request after its first, and a shed request's refusal would come
// that read's time past the tier's queue_max_wait (RATE-011). A failed read
// is logged at warn and tried again at the next tick. A no-op without
// WithRegisterSnapshot.
func (s *Store) KeepRegister(ctx context.Context) {
	if s.reg.ttl <= 0 {
		return
	}
	s.reg.keep(ctx, s.nowFn, s.readRegister, s.log, "store: the background register read failed; the snapshot answers until it is db.register_stale_for old")
}

// keep is the loop of KeepRegister and KeepRoads over read; a failed read is
// logged at warn with the message warn.
func (r *snapshot[T]) keep(ctx context.Context, now func() time.Time, read func(context.Context) ([]T, error), log *slog.Logger, warn string) {
	tick := time.NewTicker(r.ttl)
	defer tick.Stop()
	for {
		if err := r.refresh(ctx, now, read); err != nil && ctx.Err() == nil {
			log.Warn(warn, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// refresh reads the register again unless a read is in progress, and
// answers that read's failure.
func (r *snapshot[T]) refresh(ctx context.Context, now func() time.Time, read func(context.Context) ([]T, error)) error {
	r.mu.Lock()
	if r.flight != nil {
		r.mu.Unlock()
		return nil
	}
	f := r.start(ctx, now, read)
	r.mu.Unlock()
	f.run()
	return f.err
}

// start registers a read as the flight; r.mu is held. The caller runs it
// (f.run) once r.mu is released.
func (r *snapshot[T]) start(ctx context.Context, now func() time.Time, read func(context.Context) ([]T, error)) *flight[T] {
	f := &flight[T]{done: make(chan struct{})}
	f.run = func() {
		at := now()
		f.items, f.err = read(ctx)
		r.mu.Lock()
		if f.err == nil {
			r.items, r.at = slices.Clone(f.items), at
		}
		r.flight = nil
		r.mu.Unlock()
		close(f.done)
	}
	r.flight = f
	return f
}

func (s *Store) readRegister(ctx context.Context) ([]access.Terms, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]access.Terms, error) {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := tx.Query(ctx, `
select source_ref, domain, use_classes, acceptance_required,
       coalesce(terms_gate, register_version::text, ''), audience, coalesce(hold_reason, '')
from contract.source order by source_ref`)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (access.Terms, error) {
			var (
				t       access.Terms
				classes []string
			)
			var audience string
			err := r.Scan(&t.Family, &t.Domain, &classes, &t.AcceptanceRequired, &t.Version, &audience, &t.HoldReason)
			t.Audience = access.Audience(audience)
			for _, c := range classes {
				t.UseClasses = append(t.UseClasses, access.UseClass(c))
			}
			return t, err
		})
	})
}

// Sources reads the attribution of the given families only: the allowed set
// is applied inside the read, so a family outside it is never read (SEC-031).
func (s *Store) Sources(ctx context.Context, allowed []string) ([]access.Source, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]access.Source, error) {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := tx.Query(ctx, `
select s.source_ref, s.domain, p.display_name, s.attribution_text, l.licence_name, l.licence_url,
       l.share_alike, s.share_alike_offer_url, s.terms_url
from contract.source s
join contract.publisher p using (publisher_id)
join contract.licence l using (licence_code)
where s.source_ref = any($1)
order by s.source_ref`, allowed)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (access.Source, error) {
			var v access.Source
			err := r.Scan(&v.Family, &v.Domain, &v.Publisher, &v.AttributionText, &v.Licence, &v.LicenceURL, &v.ShareAlike, &v.ShareAlikeOffer, &v.TermsURL)
			return v, err
		})
	})
}

// TermsTexts reads the texts of the given terms gates, every language, from
// the read contract's terms: the text a gated answer returns verbatim. The
// rows the licence envelope permits a key are read, as everywhere.
func (s *Store) TermsTexts(ctx context.Context, gates []string) ([]access.TermsText, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]access.TermsText, error) {
		rows, _ := tx.Query(ctx, `
select t.terms_gate, t.terms_id, t.terms_version, t.effective_on, t.lang, t.binding, t.heading, t.body, t.terms_url, t.provider_terms_url
from contract.terms t
where t.terms_gate = any($1) and `+permitted("t")+`
order by t.terms_gate, t.binding desc, t.lang`, gates)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (access.TermsText, error) {
			var v access.TermsText
			err := r.Scan(&v.Gate, &v.ID, &v.Version, &v.EffectiveOn, &v.Lang, &v.Binding, &v.Heading, &v.Body, &v.URL, &v.ProviderURL)
			return v, err
		})
	})
}
