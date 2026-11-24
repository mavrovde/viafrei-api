//go:build integration

package store

// legacyWarningText are the text columns the view never carried (#222), read
// from the warning's own row: the frozen statements gain them as the named
// ones did, so the replay still compares every other column.
const legacyWarningText = `left join lateral (select x.id, x.description, x.instruction, x.urgency, x.certainty, x.official_sha256
  from weather_warnings x where 'dwd:' || x.id = w.warning_id) x on true
left join lateral (select e.en_headline, e.en_description, e.en_instruction, e.en_sha256 from weather_warning_english(x.id) e
  where 'dwd_warnings_en' = any($1::text[])) en on true
`

// legacyFacilitySeen is what the view never carried of a facility (#222),
// read from its own row: when its source last restated its state (seen),
// and the DB equipment number an OpenStation unit carries (eno), which
// pairs it with DB FaSta's row of the same unit.
const legacyFacilitySeen = `left join lateral (select coalesce((select st.last_seen_at from station_equipment_state st where 'equipment:' || st.equipment_id = f.facility_id),
                                     (select e.last_seen_at from station_equipment e where 'equipment:' || e.equipment_id = f.facility_id),
                                     (select d.last_seen_at from db_facilities d where 'fasta:' || d.equipment_no = f.facility_id)) seen,
                            (select e.equipment_no from station_equipment e where 'equipment:' || e.equipment_id = f.facility_id) eno) x on true`

// The road-surface forecast had no view (#222): its frozen statement is the
// platform's own read of it (its weather warnings check), the stations of
// the families, then the hours while the forecast's family is read.
func init() {
	legacyReads["api_road_stations_loaded"] = `
select 'dwd_road_forecast' = any($1::text[]) and exists (select 1 from road_weather_stations where closed_at is null and source_feed = any($1::text[])) as loaded`
	legacyReads["api_road_forecast_near"] = `
with pt as (select st_setsrid(st_makepoint($3::float8, $2::float8), 4326)::geography as g),
near as (
  select s.station_id, s.name, s.segment_type, s.position_source, s.source_feed,
         st_y(s.geom::geometry) as lat, st_x(s.geom::geometry) as lon,
         st_distance(s.geom, pt.g) as distance_m
    from road_weather_stations s, pt
   where s.closed_at is null and not s.inactive and s.source_feed = any($1::text[])
     and st_dwithin(s.geom, pt.g, $4::float8)
   order by s.geom <-> pt.g
   limit $5::int
)
select n.station_id, n.name, n.segment_type::int, n.position_source, n.source_feed, n.lat, n.lon, round(n.distance_m)::int,
       f.valid_at, f.run_at, f.rc::int, f.ts::numeric::float8
  from near n
  join road_weather_forecast f on f.station_id = n.station_id
 where 'dwd_road_forecast' = any($1::text[]) and f.valid_at >= $6::timestamptz and f.valid_at <= $7::timestamptz
 order by n.distance_m, n.station_id, f.valid_at`
}
