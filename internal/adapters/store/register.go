package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Register reads the licence terms of every source family the read contract
// publishes: what the licence gate decides on (SEC-030).
func (s *Store) Register(ctx context.Context) ([]access.Terms, error) {
	var out []access.Terms
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// A failed query surfaces through CollectRows (pgx returns it from
		// rows.Err as well).
		rows, _ := tx.Query(ctx, `
select source_ref, use_classes, acceptance_required, register_version::text, review_signed_on is not null
from contract.source order by source_ref`)
		var err error
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (access.Terms, error) {
			var (
				t       access.Terms
				classes []string
			)
			err := r.Scan(&t.Family, &classes, &t.AcceptanceRequired, &t.Version, &t.Reviewed)
			for _, c := range classes {
				t.UseClasses = append(t.UseClasses, access.UseClass(c))
			}
			return t, err
		})
		return err
	})
	return out, err
}

// Sources reads the attribution of the given families only: the allowed set
// is applied inside the read, so a family outside it is never read (SEC-031).
func (s *Store) Sources(ctx context.Context, allowed []string) ([]access.Source, error) {
	var out []access.Source
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
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
		var err error
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (access.Source, error) {
			var v access.Source
			err := r.Scan(&v.Family, &v.Domain, &v.Publisher, &v.AttributionText, &v.Licence, &v.LicenceURL, &v.ShareAlike, &v.ShareAlikeOffer, &v.TermsURL)
			return v, err
		})
		return err
	})
	return out, err
}
