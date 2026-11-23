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
	return r.join(ctx, now, read)
}

// renew reads again, or answers the read in progress as get does: the
// background's read, which the reads that follow it in that background step
// depend on.
func (r *snapshot[T]) renew(ctx context.Context, now func() time.Time, read func(context.Context) ([]T, error)) ([]T, error) {
	r.mu.Lock()
	return r.join(ctx, now, read)
}

// join starts a read, or waits for the one in progress (reading itself when
// that one fails); r.mu is held and is released.
func (r *snapshot[T]) join(ctx context.Context, now func() time.Time, read func(context.Context) ([]T, error)) ([]T, error) {
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
	s.reg.keep(ctx, func(ctx context.Context) error { return s.reg.refresh(ctx, s.nowFn, s.readRegister) }, s.log, "store: the background register read failed; the snapshot answers until it is db.register_stale_for old")
}

// keep is the loop of KeepRegister and KeepRoads: step at once and every
// ttl until ctx ends; a failed step is logged at warn with the message warn.
func (r *snapshot[T]) keep(ctx context.Context, step func(context.Context) error, log *slog.Logger, warn string) {
	tick := time.NewTicker(r.ttl)
	defer tick.Stop()
	for {
		if err := step(ctx); err != nil && ctx.Err() == nil {
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

// sourceRows are the source families the read contract publishes, as its
// source register held them: each registered source (contract_sources) with
// its register row, its domain (contract_source_domains) and its source rule
// (contract_source_rules, the first by priority); an excluded family and
// one whose licence is pending are not published. A family's use classes
// are its register's, only consumer when its rule does not give it openly.
const sourceRows = `src as (
  select s.source_ref, d.domain, s.licence_code, s.attribution_text, s.terms_url, s.publisher_name,
         case when g.share_alike or s.licence_code ~* '(BY-SA|ODbL|Open Database License)' then 'https://viafrei.de/impressum' end share_alike_offer_url,
         s.acceptance_required,
         case when coalesce(k.display_class, 'open') = 'open' then coalesce(g.use_classes, '{}')
              else array(select u.u from unnest(coalesce(g.use_classes, '{}')) u(u) where u.u = 'consumer') end use_classes,
         coalesce(g.register_version, (select max(v.register_version) from source_register v)) register_version,
         coalesce(k.audience, 'public') audience, k.terms_gate, k.hold_reason
  from contract_sources s
  left join source_register g on g.source_id = s.register_id
  left join lateral (select d.domain from contract_source_domains d where s.source_ref ~ d.pattern order by d.priority limit 1) d on true
  left join lateral (select k.excluded, k.display_class, k.audience, k.terms_gate, k.hold_reason
                       from contract_source_rules k where s.source_ref ~ k.pattern order by k.priority limit 1) k on true
  where not coalesce(k.excluded, false) and coalesce(s.licence_code, '') !~* '^pending')`

// registerRead is every published family's licence terms and envelope.
var registerRead = named("api_register", `
with `+sourceRows+`
select src.source_ref, src.domain, src.use_classes, src.acceptance_required,
       coalesce(src.terms_gate, src.register_version::text, ''), src.audience, coalesce(src.hold_reason, '')
from src order by src.source_ref`)

// sourcesRead is the attribution of the families $1: the publisher, the
// attribution text, the licence (its name, its link: the licence text's,
// the known licence's, the one terms link every family under it shares, or
// a register row's) and whether it is share-alike.
var sourcesRead = named("api_sources", `
with `+sourceRows+`
select s.source_ref, s.domain, s.publisher_name, s.attribution_text, s.licence_code,
       coalesce((select t.url from licence_texts t where t.licence_label = s.licence_code),
                case when s.licence_code ~* '^CC BY-SA 4\.0' then 'https://creativecommons.org/licenses/by-sa/4.0/'
                     when s.licence_code ~* '^CC BY 4\.0' then 'https://creativecommons.org/licenses/by/4.0/'
                     when s.licence_code ~* '^CC BY 3\.0 DE' then 'https://creativecommons.org/licenses/by/3.0/de/'
                     when s.licence_code ~* '(^CC0|Creative Commons Zero)' then 'https://creativecommons.org/publicdomain/zero/1.0/'
                     when s.licence_code ~* '(ODbL|Open Database License)' then 'https://opendatacommons.org/licenses/odbl/1-0/'
                     when s.licence_code ~* '(dl-de/zero|Datenlizenz Deutschland – Zero)' then 'https://www.govdata.de/dl-de/zero-2-0'
                     when s.licence_code ~* '(dl-de/by|Datenlizenz Deutschland – Namensnennung)' then 'https://www.govdata.de/dl-de/by-2-0'
                     when s.licence_code ~* 'GeoNutzV' then 'https://www.gesetze-im-internet.de/geonutzv/' end,
                (select min(o.terms_url) from src o where o.licence_code = s.licence_code having count(distinct o.terms_url) = 1),
                (select min(g.source_url) from source_register g where g.licence_label = s.licence_code)),
       s.licence_code ~* '(BY-SA|ODbL|Open Database License)', s.share_alike_offer_url, s.terms_url
from src s
where s.source_ref = any((select $1::text[])::text[]) and s.publisher_name is not null and s.licence_code is not null
order by s.source_ref`, "text[]")

// termsTexts are the texts of the terms gates $1, every language.
var termsTexts = named("api_terms_texts", `
select t.terms_gate, t.terms_id, t.terms_version, t.effective_on, x.lang, x.binding, x.heading, x.body, x.terms_url, t.provider_terms_url
from source_terms t
join source_terms_texts x on x.terms_gate = t.terms_gate
where t.terms_gate = any((select $1::text[])::text[])
order by t.terms_gate, x.binding desc, x.lang`, "text[]")

func (s *Store) readRegister(ctx context.Context) ([]access.Terms, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]access.Terms, error) {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := run(ctx, tx, registerRead)
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
		rows, _ := run(ctx, tx, sourcesRead, allowed)
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
		rows, _ := run(ctx, tx, termsTexts, gates)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (access.TermsText, error) {
			var v access.TermsText
			err := r.Scan(&v.Gate, &v.ID, &v.Version, &v.EffectiveOn, &v.Lang, &v.Binding, &v.Heading, &v.Body, &v.URL, &v.ProviderURL)
			return v, err
		})
	})
}
