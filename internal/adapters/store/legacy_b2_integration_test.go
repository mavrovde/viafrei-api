//go:build integration

package store

// The frozen view statements of the hydrogen, bay, lorry, POI and train-board
// reads (legacyReads).

const legacyHydrogenColumns = `h.station_id, h.name, h.operator_name, h.brand, st_y(h.location::geometry), st_x(h.location::geometry),
       round(st_distance(h.location, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography))::int,
       h.address, h.refill, h.opening, h.provider_updated_at, h.last_seen_at, h.source_ref
from contract.hydrogen_station h`

func legacyHydrogenVehicle(n string) string {
	return `($` + n + `::text is null or exists (select 1 from jsonb_array_elements(h.refill) p where p->>'solution' like '%' || $` + n + `::text))`
}

const legacyPoi = `select p.poi_id, p.name, p.category, p.category_value, p.brand, p.operator_name, p.street, p.house_number,
       p.postcode, p.place_name, st_y(p.location), st_x(p.location),
       case when $3::float8 is null then null
            else round(st_distance(p.location::geography, st_setsrid(st_makepoint($3::float8, $4::float8), 4326)::geography))::int end as distance_m,
       p.data_as_of, p.source_ref
from contract.poi p
where (p.name_fold like '%' || $1::text || '%' or lower(p.brand) like '%' || $1::text || '%')
  and ($2::text is null or p.place_fold = $2::text)
  and ($3::float8 is null or st_dwithin(p.location::geography, st_setsrid(st_makepoint($3::float8, $4::float8), 4326)::geography, $5::float8))
  and ($6::text is null or p.category = $6::text)
  and p.source_ref = any($7::text[]) and ` + "p.display_class <> 'withhold' and (p.audience = 'public' or " + ownKeyOn + ")" + `
order by case when p.name_fold = $1::text then 0 when p.name_fold like $1::text || '%' then 1 else 2 end,
         distance_m nulls last, similarity(p.name_fold, $1::text) desc, p.name, p.poi_id
limit $8::int`

func init() {
	for name, sql := range map[string]string{
		"api_hydrogen_near": `select ` + legacyHydrogenColumns + `
where st_dwithin(h.location, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)
  and h.source_ref = any($4::text[]) and ` + legacyHydrogenVehicle("5") + ` and ` + permitted("h") + `
order by 7, h.name nulls last, h.station_id
limit $6::int`,
		"api_hydrogen_nearest": `select ` + legacyHydrogenColumns + `
where h.source_ref = any($3::text[]) and ` + legacyHydrogenVehicle("4") + ` and ` + permitted("h") + `
order by h.location <-> st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, h.station_id
limit 1`,
		"api_bays_near": `with p as (select st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography as g),
m as (
  select b.bay_id, b.bay_kind, b.publisher_kind, b.street, b.house_number, b.postcode, b.city,
         st_y(b.location) as lat, st_x(b.location) as lon,
         round(st_distance(b.location::geography, p.g))::int as distance_m,
         case when b.spaces is null and b.state_at is not null and b.state_at > $5::timestamptz - make_interval(secs => $6::int)
              then b.state end as state,
         b.state_at, b.spaces, b.reserved_when, b.source_ref
    from contract.street_bay b, p
   where b.bay_kind = $4::text and b.source_ref = any($8::text[]) and ` + permitted("b") + `
     and st_dwithin(b.location::geography, p.g, $3::float8)
)
select m.bay_id, m.bay_kind, m.publisher_kind, m.street, m.house_number, m.postcode, m.city, m.lat, m.lon, m.distance_m,
       m.state, m.state_at, m.spaces, m.reserved_when, m.source_ref,
       count(*) over ()::int, (count(*) filter (where m.spaces is null) over ())::int,
       (count(*) filter (where m.spaces is not null) over ())::int, coalesce(sum(m.spaces) over (), 0)::int,
       (count(*) filter (where m.state = 'free') over ())::int, (count(*) filter (where m.state = 'occupied') over ())::int
  from m
 where not $9::boolean or m.state = 'free'
 order by m.distance_m, m.bay_id
 limit $7::bigint`,
		"api_lorry_rules": `select r.rule_id, r.rule_layer, r.restriction_kind, r.sign, r.unit, r.published_text, r.road_ref, r.street,
       r.municipality, r.direction, r.times, r.days, r.category, r.network_state, r.value::float8, r.exceptions, r.version_date,
       case when $4::float8 is null then null else round(st_distance(r.geometry::geography, st_setsrid(st_makepoint($4::float8, $5::float8), 4326)::geography))::int end,
       r.source_ref
from contract.lorry_rule r
where r.source_ref = any($1::text[]) and ` + permitted("r") + `
  and (case when $3::text = 'long_lorry' then r.rule_layer = 'long_lorry' else r.rule_layer <> 'long_lorry' end)
  and ($2::text = '' or upper(replace(r.road_ref, ' ', '')) = $2::text)
  and ($4::float8 is null or st_dwithin(r.geometry::geography, st_setsrid(st_makepoint($4::float8, $5::float8), 4326)::geography, $6::float8))
order by 18 nulls last, r.rule_id
limit $7::int`,
		"api_poi_match": legacyPoi,
		"api_train_board": `select t.fetched_at, t.planned_departure_at, t.changed_departure_at, t.platform, t.changed_platform, t.category, t.train_number,
       t.line, t.destination, t.cancelled
from contract.train_board t
where t.station_eva = $1::text and t.source_ref = any($2::text[]) and ` + permitted("t") + `
order by t.planned_departure_at, t.train_number nulls last`,
	} {
		legacyReads[name] = sql
	}
}
