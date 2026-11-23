//go:build integration

package store

import "github.com/mavrovde/viafrei-api/internal/domain/places"

// legacyReads are the statements as they read the read contract's views
// before the store read the platform's tables, frozen: a parity test runs
// each beside the named statement that took its place and compares the rows.
// Test-only: the store itself never reads a contract view.
var legacyReads = map[string]string{
	"api_places_page": `
with here as (
  select case when $4::float8 is null then null
              else st_setsrid(st_makepoint($5::float8, $4::float8), 4326)::geography end g),
hit as (
  select a.place_id, min(case when a.search_fold = $2 then 0 when a.search_fold like $2 || '%' then 1 else 2 end) rank
  from contract.place_alias a
  where a.search_fold like '%' || $2 || '%' and ` + permitted("a") + `
  group by a.place_id),
found as (
  select p.place_id id, p.name, p.place_kind kind, p.location, p.region_code, p.source_ref, hit.rank, p.prominence, p.official_key,
         true aliased
  from contract.place p
  join hit on hit.place_id = p.place_id
  where p.withdrawn_at is null
    and p.location is not null
    and (p.source_ref = any($1) or (p.place_kind = 'junction' and p.source_ref = any($9)))
    and ` + permitted("p") + `
  union all
  select s.stop_place_id, s.name, 'station_area', s.location, s.region_code, s.source_ref,
         case when s.search_fold = $2 then 0 when s.search_fold like $2 || '%' then 1 else 2 end, null, null, false
  from contract.stop_place s
  where s.search_fold like '%' || $2 || '%'
    and s.withdrawn_at is null
    and s.location is not null
    and s.source_ref = any($8)
    and ` + permitted("s") + `
  union all
  select r.road_ref, r.road_ref, 'motorway', pt.location, null, r.source_ref, 0, null, null, false
  from contract.road r
  cross join lateral (
    select coalesce((select j.location from contract.junction j
                      where j.road_ref = r.road_ref and j.location is not null and j.source_ref = any($9) and ` + permitted("j") + `
                      order by j.chainage_km nulls last, j.junction_id limit 1),
                    st_pointonsurface(r.geometry)) location) pt
  where $10::text <> ''
    and r.road_ref = $10::text
    and pt.location is not null
    and r.source_ref = any($9)
    and ` + permitted("r") + `)
select f.id, f.name, f.kind, st_y(f.location), st_x(f.location), f.region_code, f.source_ref, f.dist, f.official_key,
       case when f.aliased then (select min(e.label) from contract.place_alias e
         where e.place_id = f.id and e.lang = 'en' and btrim(e.label) <> '' and e.label <> f.name and ` + permitted("e") + `) end
from (
  select f.*, case when here.g is null then null else round(st_distance(f.location::geography, here.g, false))::int end dist
  from found f
  cross join here
  where ($3::text = '' or f.kind = $3::text)
    and (here.g is null or $6::int = 0 or st_dwithin(f.location::geography, here.g, $6::int, false))
  order by f.rank, f.prominence desc nulls last, dist nulls last, f.name, f.id
  limit $7) f
order by f.rank, f.prominence desc nulls last, f.dist nulls last, f.name, f.id`,
	"api_kreis_names": `
select p.official_key, min(p.name)
from contract.place p
where p.place_kind = 'district' and p.official_key = any($1) and p.withdrawn_at is null
  and p.source_ref = '` + places.KreisFamily + `' and p.source_ref = any($2) and ` + permitted("p") + `
group by p.official_key`,
}

// LegacyRead is the frozen view statement the named statement name replaced.
func LegacyRead(name string) string { return legacyReads[name] }
