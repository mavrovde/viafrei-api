//go:build integration

package store

// The frozen view statements of the weather, rules, zone, fuel, address and
// other Set B reads (legacyReads): added here, so each family's port adds
// its own.

const legacyWarningPlaced = `from contract.weather_warning w
` + legacyWarningText + `where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1::text[])
  and w.display_class <> 'withhold' and (w.audience = 'public' or ` + ownKeyOn + `)
  and (w.valid_to is null or w.valid_to > $2::timestamptz)
  and w.area is not null and w.area && st_setsrid(st_makepoint($4::float8, $3::float8), 4326)
  and st_intersects(w.area::geography, st_setsrid(st_makepoint($4::float8, $3::float8), 4326)::geography)`

const legacyWarningColumns = `
select w.warning_id, w.source_ref, w.hazard, w.warning_level, w.level_label, w.issuer_name, w.headline,
       w.text_lang, w.valid_from, w.valid_to, coalesce(w.area_name, ''),
       w.warn_cell_id, w.event, w.identifier, w.published_at,
       x.description, x.instruction, x.urgency, x.certainty, en.en_headline, en.en_description, en.en_instruction
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
` + legacyWarningText + `where (w.warn_cell_id = $2::text or w.area_name = $2::text)
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
		"api_fuel_near": `
with here as (select st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography g)
select s.station_id, s.name, s.brand, st_y(s.location), st_x(s.location),
       round(st_distance(s.location::geography, here.g))::int, s.open_state, s.source_ref, s.whole_day, s.opening_times,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}')
from contract.fuel_station s
cross join here
left join lateral (
  select array_agg(l.product_code order by l.product_code) products,
         array_agg(l.price::float8 order by l.product_code) prices,
         array_agg(l.observed_at order by l.product_code) observed
  from (select distinct on (o.product_code) o.product_code, o.price, o.observed_at
          from contract.fuel_price_observation o
         where o.station_id = s.station_id and o.price is not null
           and o.source_ref = any($4::text[]) and ` + permitted("o") + `
         order by o.product_code, o.observed_at desc) l) p on true
where s.withdrawn_at is null
  and s.source_ref = any($4::text[])
  and ` + permitted("s") + `
  and st_dwithin(s.location::geography, here.g, $3::float8)
order by st_distance(s.location::geography, here.g), s.station_id
limit $5::bigint`,
		"api_fuel_coverage": `
with here as (select st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography g),
held as (
  select s.station_id, s.location::geography loc
  from contract.fuel_station s
  where s.withdrawn_at is null and s.source_ref = any($4::text[]) and ` + permitted("s") + `)
select
  (select count(*)::int from held, here where st_dwithin(held.loc, here.g, $3::float8)),
  (select min(st_distance(held.loc, here.g)) from held, here
    where st_dwithin(held.loc, here.g, $6::float8)
      and exists (select 1 from contract.fuel_price_observation o
                   where o.station_id = held.station_id and o.price is not null and ($5::text is null or o.product_code = $5::text)
                     and o.source_ref = any($4::text[]) and ` + permitted("o") + `)),
  case when $5::text is null then 0 else
  (select count(*)::int from held, here where st_dwithin(held.loc, here.g, $3::float8)
     and not exists (select 1 from contract.fuel_price_observation o
                      where o.station_id = held.station_id and o.price is not null and o.product_code = $5::text
                        and o.observed_at > $8::timestamptz - make_interval(secs => $7::int)
                        and o.source_ref = any($4::text[]) and ` + permitted("o") + `)) end`,
		"api_fuel_by_id": `
select s.station_id, s.name, s.brand, st_y(s.location), st_x(s.location), s.open_state, s.source_ref, s.whole_day, s.opening_times,
       s.street, s.house_number, s.postcode, s.city,
       coalesce(p.products, '{}'), coalesce(p.prices, '{}'), coalesce(p.observed, '{}')
from contract.fuel_station s
left join lateral (
  select array_agg(l.product_code order by l.product_code) products,
         array_agg(l.price::float8 order by l.product_code) prices,
         array_agg(l.observed_at order by l.product_code) observed
  from (select distinct on (o.product_code) o.product_code, o.price, o.observed_at
          from contract.fuel_price_observation o
         where o.station_id = s.station_id and o.price is not null
           and o.observed_at > $4::timestamptz - make_interval(secs => $3::int) and o.observed_at <= $4::timestamptz
           and o.source_ref = any($2::text[]) and ` + permitted("o") + `
         order by o.product_code, o.observed_at desc) l) p on true
where s.station_id = $1::text
  and s.withdrawn_at is null
  and s.source_ref = any($2::text[])
  and ` + permitted("s"),
		"api_coverage_kinds": `
select p.source_ref, p.place_kind, count(*)::int, max(p.changed_at),
       count(*) filter (where exists (select 1 from contract.place_alias a
                                       where a.place_id = p.place_id and a.lang = 'en' and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `))::int
from contract.place p
where p.withdrawn_at is null and p.source_ref = any($1::text[]) and ` + "p.display_class <> 'withhold' and (p.audience = 'public' or " + ownKeyOn + ")" + `
group by 1, 2
order by 1, 2`,
		"api_coverage_lands": `
select p.official_key, p.name, p.source_ref
from contract.place p
where p.place_kind = 'state' and p.official_key is not null and p.withdrawn_at is null and p.source_ref = any($1::text[])
  and ` + "p.display_class <> 'withhold' and (p.audience = 'public' or " + ownKeyOn + ")" + `
order by 1, 3`,
		"api_coverage_addresses": `
with recursive lands(land) as (
  (select a.land_name from contract.address a
    where a.land_name is not null and a.source_ref = any($1::text[]) order by 1 limit 1)
  union all
  select (select a.land_name from contract.address a
           where a.land_name > l.land and a.source_ref = any($1::text[]) order by 1 limit 1)
    from lands l where l.land is not null)
select l.land, x.data_as_of, x.source_ref
from lands l
cross join lateral (select a.data_as_of, a.source_ref from contract.address a
                     where a.land_name = l.land and a.source_ref = any($1::text[]) and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `
                     order by a.data_as_of desc nulls last limit 1) x
where l.land is not null
union all
select null, x.data_as_of, x.source_ref
from (select a.data_as_of, a.source_ref from contract.address a
       where a.land_name is null and a.source_ref = any($1::text[]) and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `
       order by a.data_as_of desc nulls last limit 1) x
order by 1 nulls last`,
		"api_address_exact": `select a.address_id, a.street, a.house_number, a.postcode, a.place_name, a.land_name,
       st_y(a.location), st_x(a.location), a.data_as_of, a.source_ref
from contract.address a
where a.street_fold = $1::text and a.house_number_fold = $2::text
  and (($3::text is not null and a.postcode = $3::text)
       or ($4::text is not null and a.place_fold = $4::text and ($3::text is null or a.postcode is null or a.postcode = $3::text)))
  and a.source_ref = any($5::text[]) and ` + permitted("a") + `
order by a.postcode is not distinct from $3::text desc, a.address_id
limit $6::int`,
	} {
		legacyReads[name] = sql
	}
}
