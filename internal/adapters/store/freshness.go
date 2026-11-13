package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// freshnessVersions reads, in one statement, a row per source family with its
// version (kind f, familyVersion) and with the version of its newest
// confirmation (kind c, confirmationVersion); one row (kind r) carrying the
// register fingerprint: a digest of every row the licence gate, the
// attribution and the notes of an answer are read from; and one row (kind l)
// carrying the fingerprint of the road lamp's reference rows: which families
// cover each state's traffic messages, and each junction's state; and one row
// (kind u) carrying the fingerprint of the curated rule rows, every topic and
// every rule. The register and the reference rows are small tables, never an
// observation, so they are read whole. One statement means one failure path.
const freshnessVersions = `
select 'r', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.source t),
  (select string_agg(t::text, ',' order by t::text) from contract.source_note t),
  (select string_agg(t::text, ',' order by t::text) from contract.licence t),
  (select string_agg(t::text, ',' order by t::text) from contract.publisher t)))
union all
select 'l', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.message_coverage t),
  (select string_agg(j.junction_id || '=' || coalesce(j.state_code, ''), ',' order by j.junction_id) from contract.junction j)))
union all
select 'u', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.rule_topic t),
  (select string_agg(t::text, ',' order by t::text) from contract.rule_statement t)))
union all
select 'f', f.source_ref::text, ` + familyVersion + ` from contract.source_freshness f
union all
select 'c', f.source_ref::text, ` + confirmationVersion + ` from contract.source_freshness f`

// confirmationVersion is the version of a family's newest confirmation, over
// contract.source_freshness as f: the instant, the interval and whether its
// updates are paused. The road lamp judges a family's sight by them, so a
// confirmation that recovers a family, or a changed interval, ends a cached
// lamp; a confirmation lapsing with the clock alone is an instant the answer
// notes instead. Kept apart from familyVersion, so an answer that does not
// judge confirmations is not ended by every one.
const confirmationVersion = `coalesce(extract(epoch from f.confirmed_at)::text, '') || '@' || coalesce(f.confirm_interval_s::text, '') || '@' || f.updates_paused::text`

// familyVersion is the version of a family's data that the HTTP cache
// compares and an ETag is built from, over contract.source_freshness as f:
// the freshness version AND the instant the data is current to. An answer
// renders dataAsOf, ageS, stale and Last-Modified from data_as_of, so a
// moved data_as_of must end a cached answer even when the freshness version
// stayed the same (a source clock that was wrong and is corrected, say).
// Empty when the family has published nothing; Provenance reads the same
// expression, so an answer is tagged with the version this read compares.
const familyVersion = `coalesce(f.freshness_version || '@' || coalesce(extract(epoch from f.data_as_of)::text, ''), '')`

// FreshnessVersions reads the freshness version and the confirmation version
// of every source family, the register fingerprint and the lamp's reference
// fingerprint, in one snapshot. Both are small tables, never an
// aggregate over observations, so the HTTP cache can read them while requests
// arrive and answer If-None-Match before any feature read
// (Performance-and-Caching).
func (s *Store) FreshnessVersions(ctx context.Context) (provenance.Versions, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (provenance.Versions, error) {
		// Built inside the read and handed back only to a caller still
		// waiting: nothing the caller holds is written past its deadline.
		out := provenance.Versions{Families: map[string]string{}, Confirmations: map[string]string{}}
		var kind, family, version string
		// A failed query surfaces through ForEachRow.
		rows, _ := tx.Query(ctx, freshnessVersions)
		_, err := pgx.ForEachRow(rows, []any{&kind, &family, &version}, func() error {
			switch kind {
			case "r":
				out.Register = version
			case "l":
				out.LampReference = version
			case "u":
				out.RulesReference = version
			case "f":
				out.Families[family] = version
			default:
				out.Confirmations[family] = version
			}
			return nil
		})
		return out, err
	})
}
