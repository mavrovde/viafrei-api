//go:build integration

package store

import (
	"regexp"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The frozen view statements of the location, lookup, nearby, state and
// road reads, beside legacyReads (merged into it when the package loads).

// legacyStatedM is statedM as the view statements measured it.
func legacyStatedM(col string) string {
	d := `st_distance(` + col + `::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, false)::numeric`
	return `(case when ` + d + ` < 50 then round(` + d + `) else round(` + d + ` / 100) * 100 end)::int`
}

// legacyNeed is the view statement of the need with its kinds, its
// parameters those of the need's named statement.
func legacyNeed(need string, kinds []string) string {
	return `select p.poi_id, p.name, p.category || '=' || p.category_value, p.street, p.house_number,
       st_y(p.location), st_x(p.location),
       round(st_distance(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int, p.source_ref
from contract.poi p
where p.category || '=' || p.category_value = any(array['` + strings.Join(kinds, "','") + `']) and p.name is not null
  and not ('` + need + `' = 'money' and p.category_value = 'post_office' and p.name ~* '\m(dpd|hermes|gls|ups|fedex)\M')
  and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
  and p.source_ref = any($4) and ` + permitted("p") + `
order by p.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, p.poi_id
limit $5`
}

// legacyLamp is the view statement of the lamp's warnings under cond.
func legacyLamp(cond string) string {
	return `select w.source_ref, w.warning_level, w.valid_from <= $2::timestamptz,
  case when w.valid_from > $2::timestamptz then w.valid_from else w.valid_to end from contract.weather_warning w
where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1::text[])
  and w.display_class <> 'withhold' and (w.audience = 'public' or ` + ownKeyOn + `)
  and (w.valid_to is null or w.valid_to > $2::timestamptz)
  and ` + cond + `
order by w.warning_level desc, w.warning_id`
}

func init() {
	legacyReads["api_lamp_warnings_road"] = legacyLamp(`exists (select 1 from contract.junction j
               where upper(replace(j.road_ref, ' ', '')) = $3::text and ` + permitted("j") + `
                 and st_intersects(w.area, j.location))`)
	legacyReads["api_lamp_warnings_near"] = legacyLamp(`w.area is not null and st_dwithin(w.area::geography, st_setsrid(st_makepoint($4::float8, $3::float8), 4326)::geography, $5::float8)`)
	legacyReads["api_lamp_warnings_state"] = legacyLamp(`($3::text = 'de' or (left(w.warn_cell_id, 1) in ('7', '8') and substr(w.warn_cell_id, 2, 2) = $3::text))`)
	for need, kinds := range places.NeedKinds {
		legacyReads["api_nearby_need_"+need] = legacyNeed(need, kinds)
	}
	for name, sql := range map[string]string{
		"api_location_place": `select p.place_id, p.name, p.place_kind, st_y(p.location), st_x(p.location), p.region_code, p.source_ref,
       ` + legacyStatedM("p.location") + `, p.official_key
from contract.place p
where p.place_kind = $3 and p.withdrawn_at is null and p.location is not null
  and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $4)
  and p.source_ref = any($5) and ` + permitted("p") + `
order by p.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, p.place_id
limit 1`,
		"api_location_sectors": `select w.i::int, exists (select 1 from contract.address a
  where a.source_ref = any($2) and ` + permitted("a") + ` and st_intersects(a.location::geography, st_geogfromtext(w.wkt)))
from unnest($1::text[]) with ordinality as w(wkt, i)
order by w.i`,
		"api_location_abroad_family": `select a.source_ref from contract.address a
where a.source_ref = any($3) and ` + permitted("a") + `
order by a.location <-> st_setsrid(st_makepoint($1, $2), 4326), a.address_id
limit 1`,
		"api_location_state": `select p.place_id, p.name, p.place_kind, st_y(p.location), st_x(p.location), p.region_code, p.source_ref, null::int, p.official_key
from contract.place p
where p.place_kind = 'state' and p.withdrawn_at is null and p.location is not null and p.region_code = left($1, 2)
  and p.source_ref = any($2) and ` + permitted("p") + `
order by p.place_id
limit 1`,
		"api_location_address": `select a.address_id, a.street, a.house_number, a.postcode, a.place_name, a.land_name,
       st_y(a.location), st_x(a.location), a.data_as_of, a.source_ref,
       round(st_distance(a.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography))::int
from contract.address a
where st_dwithin(a.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
  and a.source_ref = any($4) and ` + permitted("a") + `
order by a.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, a.address_id
limit 1`,
		"api_lookup_anchor": `select split_part(j.junction_id, ':', 3) from contract.junction j
where j.source_ref = 'bast_lcl' and j.location is not null and ` + permitted("j") + `
  and split_part(j.junction_id, ':', 3) ~ '^[0-9]{1,6}$'
  and st_dwithin(j.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
order by j.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, j.junction_id
limit 1`,
		"api_lookup_eva": `select s.eva_no from contract.stop_place s
where s.withdrawn_at is null and s.eva_no is not null
  and ` + permitted("s") + `
  and s.search_fold like $1::text || '%'
order by s.search_fold = $1::text desc, length(s.name), s.name, s.stop_place_id
limit 1`,
		"api_lookup_ags": `select p.official_key from contract.place p
where p.source_ref = 'bkg_gvisys' and p.place_kind = 'municipality' and p.withdrawn_at is null
  and p.official_key ~ '^[0-9]{8}$' and ` + permitted("p") + `
  and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)
order by p.location::geography <-> st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, p.official_key
limit 1`,
		"api_nearby_junctions": `select j.junction_id, j.road_ref, j.number, j.name, st_y(j.location), st_x(j.location),
       round(st_distance(j.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography))::int, j.source_ref
from contract.junction j
where j.name is not null and st_dwithin(j.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)
  and j.source_ref = any($4::text[]) and ` + permitted("j") + `
order by 7, j.junction_id
limit $5::int8`,
		"api_state_warnings": `
select w.event, max(w.warning_level)::int, count(distinct w.identifier)::int, count(distinct w.warn_cell_id)::int,
       max(w.valid_to), array_agg(distinct w.source_ref order by w.source_ref)
from contract.weather_warning w
where w.source_ref = any($1) and ` + permitted("w") + `
  and w.withdrawn_at is null and w.area is not null
  and (w.valid_to is null or w.valid_to > $2::timestamptz) and w.valid_from <= $2::timestamptz
  and left(w.warn_cell_id, 1) in ('7', '8') and substring(w.warn_cell_id from 2 for 2) = $3
  and w.warning_level >= 1
group by w.event
order by 2 desc, 1`,
		//nolint:misspell // materialized is the SQL keyword
		"api_state_alerts": `
with f as materialized (
  select a.alert_id, a.header, a.active_to, coalesce(a.line_names, '{}') as line_names, a.dedupe_key, a.source_ref,
         array(select distinct left(k, 2) from unnest(a.district_codes) k where k ~ '^[0-9]{5}$' order by 1) as lands
  from contract.service_alert a
  where a.source_ref = any($1) and ` + permitted("a") + `
    and cardinality(a.district_codes) > 0 and ` + alertLive("a", "$2") + `
    and (jsonb_array_length(coalesce(a.active_periods, '[]'::jsonb)) = 0
         or exists (select 1 from jsonb_array_elements(a.active_periods) p
                     where (p->>'start' is null or (p->>'start')::timestamptz <= $2::timestamptz)
                       and (p->>'end' is null or (p->>'end')::timestamptz > $2::timestamptz)))
), k as (
  select o.alert_id, o.header, o.active_to, o.line_names, o.lands,
         coalesce((select g.alert_id from f g where g.dedupe_key = o.dedupe_key
                    order by (g.source_ref <> 'delfi_rt_sa'), g.source_ref, g.alert_id limit 1), o.alert_id) as message_key,
         coalesce((select array_agg(distinct g.source_ref order by g.source_ref) from f g where g.dedupe_key = o.dedupe_key),
                  array[o.source_ref]) as carried_by
  from f o
)
select k.alert_id, k.header, k.active_to, k.line_names, k.message_key, k.carried_by, k.lands
from k
where k.message_key in (select m.message_key from k m where $3 = any(m.lands))
order by k.message_key, k.alert_id`,
		"api_road_shapes": `
select r.road_ref, r.length_km::float8, coalesce(r.states, '{}'), r.geometry::text
from contract.road r
order by r.road_ref`,
		"api_road_junction_lands": `
select j.junction_id, j.state_code from contract.junction j where j.road_ref = $1::text order by j.junction_id`,
		"api_road": `
select road_ref, road_class, length_km::float8, states
from contract.road r
where upper(replace(road_ref, ' ', '')) = $1::text and ` + permitted("r") + `
order by road_ref
limit 1`,
		"api_road_coverage": `select state_code, source_ref, coverage_role, current_only
from contract.message_coverage
where state_code = any($1::text[]) and source_ref = any($2::text[])
  and display_class <> 'withhold' and (audience = 'public' or ` + ownKeyOn + `)
order by state_code, source_ref`,
		"api_motorway_exits": `
select j.junction_id, j.name, j.junction_kind, j.number, j.state_code, st_y(j.location), st_x(j.location)
from contract.junction j
where j.road_ref = $1::text and ` + permitted("j") + `
order by nullif(regexp_replace(coalesce(j.number, ''), '[^0-9].*$', ''), '')::int nulls last, j.number, j.junction_id`,
		"api_motorway_exits_warm": `
select j.junction_id, j.name, j.junction_kind, j.number, null::text, st_y(j.location), st_x(j.location)
from contract.junction j
where j.road_ref = $1::text and ` + permitted("j") + `
order by nullif(regexp_replace(coalesce(j.number, ''), '[^0-9].*$', ''), '')::int nulls last, j.number, j.junction_id`,
		"api_road_states": `select q.ref, coalesce(array_agg(distinct j.state_code order by j.state_code) filter (where j.state_code is not null), '{}')
from unnest($1::text[]) q(ref)
join contract.junction j on upper(replace(j.road_ref, ' ', '')) = q.ref and ` + permitted("j") + `
group by q.ref`,
		"api_road_lamp_warnings": `select q.ref, w.source_ref, w.warning_level
from unnest($3::text[]) q(ref)
cross join lateral (select w.source_ref, w.warning_level, w.warning_id from contract.weather_warning w
where w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.source_ref = any($1::text[])
  and w.display_class <> 'withhold' and (w.audience = 'public' or ` + ownKeyOn + `)
  and (w.valid_to is null or w.valid_to > $2::timestamptz)
  and w.valid_from <= $2::timestamptz
  and exists (select 1 from contract.junction j
              where upper(replace(j.road_ref, ' ', '')) = q.ref and ` + permitted("j") + `
                and st_intersects(w.area, j.location))) w
order by q.ref, w.warning_level desc, w.warning_id`,
		"api_states_road": `select coalesce(array_agg(distinct state_code order by state_code) filter (where state_code is not null), '{}')
from (select j.state_code from contract.junction j
where upper(replace(j.road_ref, ' ', '')) = $1::text and ` + permitted("j") + `) j`,
		"api_states_near": `select coalesce(array_agg(distinct state_code order by state_code) filter (where state_code is not null), '{}')
from (with here as (select st_setsrid(st_makepoint($2::float8, $1::float8), 4326) g),
near as (select j.state_code from contract.junction j, here
         where st_dwithin(j.location::geography, here.g::geography, $3::float8) and ` + permitted("j") + `)
select state_code from near
union all
select (select j.state_code from contract.junction j, here where ` + permitted("j") + ` order by j.location <-> here.g, j.junction_id limit 1)
where not exists (select 1 from near)) j`,
		"api_states_state": `select case when $1::text = 'de' then (select coalesce(array_agg(r.region_code order by r.region_code), '{}') from contract.admin_region r where r.region_level = 'state') else array[$1::text] end`,
	} {
		legacyReads[name] = sql
	}
}

// legacyOpaque is a parameter as opaqueNamed reads it.
var legacyOpaque = regexp.MustCompile(`\(\(select (\$[0-9]+)::[a-z0-9_ \[\]]+\)::[a-z0-9_ \[\]]+\)`)

// legacyEvents is the named statement name as the view statement read it:
// its parameters plain, its situations the read contract's.
func legacyEvents(name string) string {
	sql := legacyOpaque.ReplaceAllString(namedStmts[name].SQL, "$1")
	for _, in := range []string{"$3", "$8", ""} {
		sql = strings.ReplaceAll(sql, trafficEvents(in), "contract.traffic_event")
	}
	return sql
}

func init() {
	for _, name := range []string{"api_area_situations", "api_state_situations", "api_road_messages", "api_area_messages",
		"api_state_messages", "api_state_placed", "api_situation", "api_road_covering", "api_road_lamp_messages"} {
		legacyReads[name] = legacyEvents(name)
	}
	onRoad := legacyEvents("api_road_situations")
	legacyReads["api_road_situations"] = onRoad[:strings.Index(onRoad, "with road as (")] + `with road as (
  select k.road_ref from contract.road k
  where upper(replace(k.road_ref, ' ', '')) = $8 and ` + permitted("k") + ` order by k.road_ref limit 1),
parts as (
  select r.road_ref, d.path[1] n, d.geom g, st_length(d.geom::geography) len, r.length_km::float8 km
  from contract.road r, st_dump(r.geometry) d
  where r.road_ref = (select road_ref from road)),
` + onRoad[strings.Index(onRoad, "line as ("):]
	legacyReads["api_road_sources"] = `select s.source_ref, s.stale_after_s, f.data_as_of,
       f.source_ref is not null, f.confirmed_at, f.confirm_interval_s::float8, coalesce(f.updates_paused, false)
from contract.source s
left join contract.source_freshness f using (source_ref)
where s.source_ref = any($1)
order by s.source_ref`
	legacyReads["api_motorway_families"] = `
select coalesce(array_agg(distinct e.source_ref order by e.source_ref), '{}')
from contract.traffic_event e
join contract.road r on r.road_ref = e.road_ref and ` + permitted("r") + `
where e.source_ref = any($1) and ` + eventPermitted + `
  and e.withdrawn_at is null and e.valid_from <= $2 and (e.valid_to is null or e.valid_to > $2)`
	motorways := func(inLand string) string {
		return `
with es as (
  select e.road_ref, count(*)::int as n, array_agg(distinct e.source_ref order by e.source_ref) as sources
  from contract.traffic_event e
  where e.source_ref = any($1) and ` + eventPermitted + `
    and e.withdrawn_at is null and e.valid_from <= $5 and (e.valid_to is null or e.valid_to > $5)
  group by e.road_ref)
select r.road_ref, r.name,
       (select count(*) from contract.junction j where j.road_ref = r.road_ref)::int,
       coalesce(es.n, 0),
       r.source_ref,
       coalesce(es.sources, '{}'),
       array(select distinct j.source_ref from contract.junction j where j.road_ref = r.road_ref order by 1)
from contract.road r
left join es on es.road_ref = r.road_ref
where ` + permitted("r") + `
  and ($2::text = '' or r.road_ref = any($6::text[])` + inLand + `)
  and ($3::text = '' or r.road_ref = $3::text)
order by length(r.road_ref), r.road_ref
limit $4`
	}
	legacyReads["api_motorways"] = motorways(``)
	legacyReads["api_motorways_live_land"] = motorways(`
       or exists (select 1 from contract.junction j where j.road_ref = r.road_ref and j.state_code = $2::text)`)
}

// The families of each dataset as the read contract's views held them,
// verbatim from before the views were replaced.
func init() {
	legacyReads["api_feeding"] = `select distinct 'alerts'::text, source_ref from contract.service_alert
union
select distinct 'events'::text, source_ref from contract.traffic_event
union
select distinct 'fuel'::text, source_ref from contract.fuel_station
union
select distinct 'lorry'::text, source_ref from contract.lorry_rule
union
select distinct 'sharing'::text, source_ref from contract.sharing_station
union
select distinct 'sharing'::text, source_ref from contract.sharing_vehicle
union
select distinct 'speed_cameras'::text, source_ref from contract.speed_camera
union
select distinct 'warnings'::text, source_ref from contract.weather_warning
union
select distinct 'zones'::text, source_ref from contract.low_emission_zone
order by 1, 2`
}
