//go:build integration

package store

// The frozen view statements of the register reads (legacyReads).

func init() {
	legacyReads["api_register"] = `
select source_ref, domain, use_classes, acceptance_required,
       coalesce(terms_gate, register_version::text, ''), audience, coalesce(hold_reason, '')
from contract.source order by source_ref`
	legacyReads["api_sources"] = `
select s.source_ref, s.domain, p.display_name, s.attribution_text, l.licence_name, l.licence_url,
       l.share_alike, s.share_alike_offer_url, s.terms_url
from contract.source s
join contract.publisher p using (publisher_id)
join contract.licence l using (licence_code)
where s.source_ref = any($1::text[])
order by s.source_ref`
	legacyReads["api_terms_texts"] = `
select t.terms_gate, t.terms_id, t.terms_version, t.effective_on, t.lang, t.binding, t.heading, t.body, t.terms_url, t.provider_terms_url
from contract.terms t
where t.terms_gate = any($1::text[]) and ` + permitted("t") + `
order by t.terms_gate, t.binding desc, t.lang`
}
