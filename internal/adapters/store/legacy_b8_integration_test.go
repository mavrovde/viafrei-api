//go:build integration

package store

// The frozen view statements of the freshness and provenance reads
// (legacyReads).

func init() {
	// The junctions' part of the 'l' row fingerprints what their states are
	// derived from (junctionLandInputs), not each derived state: on purpose,
	// the derived states took the read to 0.6 to 1.5 s on prod (fault 2).
	legacyReads["api_freshness_versions"] = `
select 'r', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.source t),
  (select string_agg(t::text, ',' order by t::text) from contract.source_note t),
  (select string_agg(t::text, ',' order by t::text) from contract.licence t),
  (select string_agg(t::text, ',' order by t::text) from contract.publisher t)))
union all
select 'l', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.message_coverage t),
  ` + junctionLandInputs + `))
union all
select 'u', '', md5(concat_ws('|',
  (select string_agg(t::text, ',' order by t::text) from contract.rule_topic t),
  (select string_agg(t::text, ',' order by t::text) from contract.rule_statement t)))
union all
select 'f', f.source_ref::text, ` + familyVersion + ` from contract.source_freshness f
union all
select 'c', f.source_ref::text, ` + confirmationVersion + ` from contract.source_freshness f`
	legacyReads["api_provenance"] = `
select s.source_ref, s.domain, p.display_name, s.attribution_text, l.licence_name, l.licence_url,
       l.share_alike, s.share_alike_offer_url, s.terms_url, p.website, s.stale_after_s, f.data_as_of, f.confirmed_at,
       ` + familyVersion + `,
       array(select n.note_kind from contract.source_note n
             where n.source_ref = s.source_ref and n.lang = $2::text order by n.note_kind, n.body),
       array(select n.body from contract.source_note n
             where n.source_ref = s.source_ref and n.lang = $2::text order by n.note_kind, n.body)
from contract.source s
join contract.publisher p using (publisher_id)
join contract.licence l using (licence_code)
left join contract.source_freshness f using (source_ref)
where s.source_ref = any($1::text[])
order by s.source_ref`
}
