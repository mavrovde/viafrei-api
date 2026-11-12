package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// freshnessVersions reads, in one statement, a row per source family with its
// version (familyVersion) and one row (flagged true) carrying the register
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
select false, f.source_ref::text, ` + familyVersion + ` from contract.source_freshness f`

// familyVersion is the version of a family's data that the HTTP cache
// compares and an ETag is built from, over contract.source_freshness as f:
// the freshness version AND the instant the data is current to. An answer
// renders dataAsOf, ageS, stale and Last-Modified from data_as_of, so a
// moved data_as_of must end a cached answer even when the freshness version
// stayed the same (a source clock that was wrong and is corrected, say).
// Empty when the family has published nothing; Provenance reads the same
// expression, so an answer is tagged with the version this read compares.
const familyVersion = `coalesce(f.freshness_version || '@' || coalesce(extract(epoch from f.data_as_of)::text, ''), '')`

// FreshnessVersions reads the freshness version of every source family and
// the register fingerprint, in one snapshot. Both are small tables, never an
// aggregate over observations, so the HTTP cache can read them while requests
// arrive and answer If-None-Match before any feature read
// (Performance-and-Caching).
func (s *Store) FreshnessVersions(ctx context.Context) (provenance.Versions, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (provenance.Versions, error) {
		// Built inside the read and handed back only to a caller still
		// waiting: nothing the caller holds is written past its deadline.
		out := provenance.Versions{Families: map[string]string{}}
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
		return out, err
	})
}
