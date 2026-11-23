//go:build integration

package store

// The frozen view statements of the weather, rules, zone, fuel, address and
// other Set B reads (legacyReads): added here, so each family's port adds
// its own.

const legacyWarningPlaced = `from contract.weather_warning w
where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1::text[])
  and w.display_class <> 'withhold' and (w.audience = 'public' or ` + ownKeyOn + `)
  and (w.valid_to is null or w.valid_to > $2::timestamptz)
  and w.area is not null and w.area && st_setsrid(st_makepoint($4::float8, $3::float8), 4326)
  and st_intersects(w.area::geography, st_setsrid(st_makepoint($4::float8, $3::float8), 4326)::geography)`

const legacyWarningColumns = `
select w.warning_id, w.source_ref, w.hazard, w.warning_level, w.level_label, w.issuer_name, w.headline,
       w.text_lang, w.valid_from, w.valid_to, coalesce(w.area_name, ''),
       w.warn_cell_id, w.event, w.identifier, w.published_at
`

const legacyZoneAlive = `(z.checked_at > $3::timestamptz - make_interval(secs => 3 * coalesce(z.cadence_s, 2592000))
    or (z.paused and z.checked_at is not null))`

func init() {
	for name, sql := range map[string]string{
		"api_weather_warnings_page": legacyWarningColumns + legacyWarningPlaced + `
  and w.valid_from < $10::timestamptz
  and w.warning_level >= $5::int
  and (w.hazard = any($6::text[]) or ($7::bool and not w.hazard = any($8::text[])))
order by w.warning_level desc, w.valid_from, w.warning_id
limit $9::bigint`,
		"api_weather_warnings_here": `select w.warning_id, w.warning_level, w.valid_from <= $2::timestamptz,
  case when w.valid_from > $2::timestamptz then w.valid_from else w.valid_to end
` + legacyWarningPlaced,
		"api_weather_cell_at": `
with here as (select st_setsrid(st_makepoint($3::float8, $2::float8), 4326) g)
select c.warn_cell_id, c.area_name
from (select distinct on (md5(st_asbinary(w.area))) w.warn_cell_id, w.area_name, w.area, w.seen_at, w.withdrawn_at is null live
        from contract.weather_warning w, here
       where w.area is not null and left(w.warn_cell_id, 1) in ('7', '8')
         and w.source_ref = any($1::text[]) and ` + permitted("w") + `
         and w.area && here.g
       order by md5(st_asbinary(w.area)), w.withdrawn_at is null desc, w.seen_at desc) c, here
where st_intersects(c.area::geography, here.g::geography)
order by c.live desc, c.seen_at desc
limit 1`,
		"api_weather_cell_named": `select exists (select 1 from contract.weather_warning w
  where w.area_name = $2::text and w.source_ref = any($1::text[]) and ` + permitted("w") + `)`,
		"api_weather_cell_warnings": legacyWarningColumns + `from contract.weather_warning w
where (w.warn_cell_id = $2::text or w.area_name = $2::text)
  and w.source_ref = any($1::text[]) and ` + permitted("w") + `
  and w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.valid_from <= $3::timestamptz and (w.valid_to is null or w.valid_to > $3::timestamptz)
  and w.warning_level >= $4::int
order by w.warning_level desc, w.valid_from desc, w.warning_id`,
		"api_rule_topic": `
select t.topic_code, case when $2::text = 'en' then coalesce(t.title_en, t.title) else t.title end,
       coalesce(min(s.reviewed_on), t.reviewed_on),
       coalesce(jsonb_agg(jsonb_build_object(
         'id', s.statement_id,
         'text', case when $2::text = 'en' then coalesce(s.body_en, s.body) else s.body end,
         'basis', s.legal_basis, 'url', s.legal_basis_url, 'reviewed', s.reviewed_on,
         'note', nullif(btrim(case when $2::text = 'en' then coalesce(s.legal_basis_note_en, s.legal_basis_note) else s.legal_basis_note end), ''),
         'verify', nullif(btrim(case when $2::text = 'en' then coalesce(s.to_verify_en, s.to_verify) else s.to_verify end), ''))
         order by s.position, s.statement_id) filter (where s.statement_id is not null), '[]')
from contract.rule_topic t
left join contract.rule_statement s on s.topic_code = t.topic_code and ` + permitted("s") + `
where t.topic_code = $1::text and ` + permitted("t") + `
group by t.topic_code, t.title, t.title_en, t.reviewed_on`,
		"api_zones_at": `
with p as (select st_transform(st_setsrid(st_makepoint($4::float8, $5::float8), 4326), 25832) m, st_setsrid(st_makepoint($4::float8, $5::float8), 4326) g),
r as (
  select z.*, (z.zone_role <> 'exempt_road' and st_covers(z.geometry, p.g)) inside,
         case when z.zone_role <> 'exempt_road' and st_covers(z.geometry, p.g) then 0 else round(st_distance(z.geometry_m, p.m))::int end dist
  from contract.low_emission_zone z, p
  where z.source_ref = any($1::text[]) and ` + permitted("z") + ` and ` + legacyZoneAlive + `
    and z.geometry_m && st_expand(p.m, $2::float8)
    and st_dwithin(z.geometry_m, p.m, case when z.zone_role = 'exempt_road' then $6::float8 else $2::float8 end))
select r.zone_key, r.zone_id, r.zone_role, r.name, r.city, r.publisher_name, r.inside, r.dist,
       array(select k from unnest(r.same_zone_keys) k where k in (select o.zone_key from r o where o.zone_role = 'zone') and r.zone_role = 'zone'),
       r.source_ref, r.changed_at, r.recorded_at, r.source_scope, r.source_place_code, r.source_city
from r
order by r.inside desc, r.dist, r.source_ref, r.zone_id`,
		"api_zone_entries": `
select e.entry_key, e.name, e.place_codes, e.plaque, e.covered, e.lifted_on
from contract.low_emission_zone_entry e
where e.place_codes && $1::text[] and ` + permitted("e") + `
order by e.entry_key`,
		"api_zone_entry_named": `
select e.entry_key, e.name, e.place_codes, e.plaque, e.covered, e.lifted_on
from contract.low_emission_zone_entry e
where (e.entry_key = $1::text or $1::text = any(e.aliases)) and ` + permitted("e") + `
order by e.entry_key limit 1`,
	} {
		legacyReads[name] = sql
	}
}
