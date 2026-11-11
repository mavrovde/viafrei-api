package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// freshnessVersions reads, in one statement, a row per source family with its
// freshness version and one row (flagged true) carrying the register
// fingerprint: a digest of every row the licence gate, the attribution and the
// notes of an answer are read from. The register is a handful of small tables,
// never an observation, so it is read whole. One statement means one failure
// path.
const freshnessVersions = `
select true, '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.source t),
  (select string_agg(t::text, ',' order by t::text) from contract.source_note t),
  (select string_agg(t::text, ',' order by t::text) from contract.licence t),
  (select string_agg(t::text, ',' order by t::text) from contract.publisher t)))
union all
select false, source_ref::text, freshness_version::text from contract.source_freshness`

// FreshnessVersions reads the freshness version of every source family and
// the register fingerprint, in one snapshot. Both are small tables, never an
// aggregate over observations, so the HTTP cache can read them while requests
// arrive and answer If-None-Match before any feature read
// (Performance-and-Caching).
func (s *Store) FreshnessVersions(ctx context.Context) (provenance.Versions, error) {
	out := provenance.Versions{Families: map[string]string{}}
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var register bool
		var family, version string
		// A failed query surfaces through ForEachRow.
		rows, _ := tx.Query(ctx, freshnessVersions)
		_, err := pgx.ForEachRow(rows, []any{&register, &family, &version}, func() error {
			if register {
				out.Register = version
			} else {
				out.Families[family] = version
			}
			return nil
		})
		return err
	})
	return out, err
}
