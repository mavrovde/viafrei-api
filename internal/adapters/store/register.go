package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Register reads the licence terms of every source family the read contract
// publishes: what the licence gate decides on (SEC-030), with the family's
// licence envelope: its audience, its hold and the terms a key accepts (the
// envelope's terms gate, else the register's version).
func (s *Store) Register(ctx context.Context) ([]access.Terms, error) {
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
