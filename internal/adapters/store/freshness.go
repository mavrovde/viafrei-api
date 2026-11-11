package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// registerFingerprint digests every row the licence gate, the attribution
// and the notes of an answer are read from. The register is a handful of
// small tables, never an observation, so it is read whole.
const registerFingerprint = `
select md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.source t),
  (select string_agg(t::text, ',' order by t::text) from contract.source_note t),
  (select string_agg(t::text, ',' order by t::text) from contract.licence t),
  (select string_agg(t::text, ',' order by t::text) from contract.publisher t)))`

// FreshnessVersions reads the freshness version of every source family and
// the register fingerprint, in one snapshot. Both are small tables, never an
// aggregate over observations, so the HTTP cache can read them while requests
// arrive and answer If-None-Match before any feature read
// (Performance-and-Caching).
func (s *Store) FreshnessVersions(ctx context.Context) (provenance.Versions, error) {
	out := provenance.Versions{Families: map[string]string{}}
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var family, version string
		// A failed query surfaces through ForEachRow.
		rows, _ := tx.Query(ctx, `select source_ref, freshness_version from contract.source_freshness`)
		if _, err := pgx.ForEachRow(rows, []any{&family, &version}, func() error {
			out.Families[family] = version
			return nil
		}); err != nil {
			return err
		}
		return tx.QueryRow(ctx, registerFingerprint).Scan(&out.Register)
	})
	return out, err
}
