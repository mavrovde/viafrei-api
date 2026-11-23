# Named statements on prod: plans and times

Each named statement of the store, proved on prod read-only (a repeatable
read, read-only transaction as the API's role reads, `jit` off, statement
timeout 15 s): prepared once, executed ten times with ten argument sets,
then `pg_prepared_statements` read for the plans taken and the last
execution explained with `analyze, buffers`. "Before" is the view statement
it replaced, as `pg_stat_statements` held it on prod the same day (mean per
call). Times are per execution, after the first.

## Set B

| statement | before (view) | after | plans (generic/custom) | plan of the last execution |
|-|-|-|-|-|
| api_weather_warnings_page | 421 ms mean, up to 1.4 s | 1.6-2.5 ms | 5 / 5 | bitmap AND of the open rows' area index (`weather_warnings_live_area_geom`) and the area index |
| api_weather_warnings_here | (same read as the page) | 1.2-2.8 ms | 5 / 5 | the same bitmap AND |
| api_weather_cell_at | 1077 ms mean | 2-3.3 ms | 5 / 5 | bitmap scan of the area index (geography), every row withdrawn included |
| api_weather_cell_warnings | not separately held | 53-150 ms | 5 / 5 | bitmap OR on `weather_warnings_live_cell`; the name branch has no index (db item: an index on `area_desc` of the open rows) |
| api_weather_cell_named | 346 ms mean | 160-225 ms (1 s cold) | 5 / 5 | sequential scan: no index on `area_desc` (db item: an index on `area_desc`) |
| api_rule_topic | under 1 ms | 0.3-1.8 ms | 15 / 5 | sequential scans of the two small rule tables |
| api_zones_at | under 1 ms (fixture-size view) | 0.4-8 ms | 5 / 5 | index scan of `lez_zones_open_geom_m` |
| api_zone_entries | 1 ms | 0.13-0.3 ms | 5 / 5 | sequential scan of the curated list (about 100 rows) |
| api_zone_entry_named | under 1 ms | 0.08-0.2 ms | 5 / 5 | the same |
| api_fuel_near | 116 ms mean | 5-80 ms (own key) | 5 / 5 | index scan of `fuel_stations_geom_gist`, then the newest price per grade by the price key |
| api_fuel_coverage | 120 ms mean | 5-58 ms (own key) | 5 / 5 | three index scans of `fuel_stations_geom_gist` (the held stations inlined, not materialised) |
| api_fuel_by_id | under 1 ms | 1.6-12 ms (own key, cold) | 5 / 5 | the station key, then the price key |
| api_address_exact | 1 ms mean | 0.3-400 ms (a common street in a large town the slowest) | 5 / 5 | index scan of `osm_addresses_street_number_place` |
| api_coverage_kinds | (the place coverage, one read) | 270-680 ms | 5 / 5 | sequential scan of the gazetteer: a count of every place of the families, by design |
| api_coverage_lands | under 1 ms | 0.2-2.5 ms | 5 / 5 | bitmap index scan of `gazetteer_places_kind_rank` |
| api_coverage_addresses | over 15 s | over 15 s (one execution, cancelled at the 15 s timeout) | - | the skip scan over the Land is an index scan of `osm_addresses_open_land`; each Land's newest extract is a bitmap heap scan of every open address of the Land and a sort by `extract_date` |

`api_coverage_addresses`, root cause: no index holds the open addresses by
Land and extract date, so the newest extract of a Land reads every address
of the Land (19.4 M rows, 14 GB in all). The statement already reads in
that order (`order by a.extract_date desc nulls last limit 1` under
`a.land = l.land`); viafrei-database item: an index on `osm_addresses
(land, extract_date desc nulls last) where closed_at is null`, which turns
each Land's probe into the first entry of its range.

The view cast the area to `geometry(MultiPolygon,4326)`, an expression no
index holds, so the box test read every warning's area; the statements test
the box on `area::geometry` (the open rows' index) or on the geography
`area` (every row's index) instead.
