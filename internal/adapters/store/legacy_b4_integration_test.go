//go:build integration

package store

// The frozen view statements of the speed reads (legacyReads).
const legacySpeedNear = `st_dwithin(c.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)`

const legacySpeedColumns = `c.camera_id, c.camera_kind, c.enforces, c.street, c.location_text, c.name, c.direction,
       c.max_speed_kmh, c.max_speed_hgv_kmh, c.valid_from, c.valid_to, c.data_as_of,
       case when c.data_as_of < ($4::date - interval '12 months') then extract(year from c.data_as_of)::int end,
       st_y(c.location), st_x(c.location), c.source_ref, count(*) over ()
from contract.speed_camera c`

var legacySpeedOfficial = `select ` + legacySpeedColumns + `
where c.camera_id not like 'osm:%' and ` + legacySpeedNear + `
  and (c.camera_kind <> 'mobile_announced' or coalesce(c.valid_to, c.valid_from) >= $4::date)
  and c.source_ref = any($5::text[]) and ` + permitted("c") + `
order by array_position(array['fixed', 'section_control', 'mobile_announced', 'measuring_site'], c.camera_kind),
         c.valid_from nulls last, coalesce(c.street, c.location_text, ''), c.camera_id
limit $6::int`

func legacySpeedCellOf(point string) string {
	return `(select g.cell from cut_gemeinde g order by g.at <-> ` + point + `, g.cell limit 1)`
}

// legacySpeedOSM takes, as the named read since the cut keeps the official
// Gemeinden (carried from main), the official lists' cells as $7: they are
// read by their own statement (api_speed_official_cells), compared apart.
//
//nolint:misspell // materialized is the SQL keyword
var legacySpeedOSM = `
with cut_gemeinde as materialized (
  select p.official_key as cell, p.location::geography as at
  from contract.place p
  where p.source_ref = 'bkg_gvisys' and p.place_kind = 'municipality' and p.withdrawn_at is null
    and p.official_key ~ '^[0-9]{8}$' and ` + permitted("p") + `
    and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8 + 80000)
), official as (
  select unnest($7::text[]) as cell
), kept as (
  select c.camera_id
  from contract.speed_camera c
  where c.camera_id like 'osm:%' and ` + legacySpeedNear + `
    and c.source_ref = any($5::text[]) and ` + permitted("c") + `
    and ` + legacySpeedCellOf("c.location::geography") + ` not in (select o.cell from official o where o.cell is not null)
)
select ` + legacySpeedColumns + `
where c.camera_id in (select k.camera_id from kept k) and ` + legacySpeedNear + `
order by coalesce(c.name, ''), c.camera_id
limit $6::int`

func init() {
	legacyReads["api_speed_official"] = legacySpeedOfficial
	legacyReads["api_speed_osm"] = legacySpeedOSM
}
