# Named statements on prod: plans and times

Each named statement of the store, proved on prod read-only (a repeatable
read, read-only transaction as the API's role reads, `jit` off, statement
timeout 15 s): prepared once, executed ten times with ten argument sets,
then `pg_prepared_statements` read for the plans taken and the last
execution explained with `analyze, buffers`. "Before" is the view statement
it replaced, as `pg_stat_statements` held it on prod the same day (mean per
call). Times are per execution, after the first.

## Set C

Proved 2026-11-23 the same way, with a statement timeout of 20 s; each
"before" also measured in the same transaction (the frozen view statement,
prepared and executed with the same arguments), beside the
`pg_stat_statements` figure. p50 and max over the executions.

| statement | before (view) | after | plans (generic/custom) | plan of the last execution | twin |
|-|-|-|-|-|-|
| api_location_address | 1.1 ms mean, up to 15 s (timeouts); measured: every execution cancelled at 20 s | 8.6 ms p50, 74 ms max | 5 / 5 | `osm_addresses_geom_gist` nearest-neighbour scan for the nearest distance, then the ties within a metre of it | gazetteer/search.ts |
| api_location_sectors | up to 15 s (timeouts); measured: cancelled at 20 s | 33 ms p50, 237 ms max | 5 / 5 | one `osm_addresses_geom_gist` probe per wedge | gazetteer/search.ts |
| api_location_abroad_family | up to 15 s (timeouts); measured: cancelled at 20 s | 4.5 ms p50, 149 ms max | 5 / 5 | `osm_addresses_geom_gist` nearest-neighbour scan, then the ties | gazetteer/search.ts |
| api_location_place | 290 ms mean, up to 801 ms; measured 50 ms p50, 1.5 s max | 4.4 ms p50, 48 ms max | 25 / 5 | municipality: `gazetteer_places_geom_gist` within the reach; district and Land: `gazetteer_places_kind_rank` (one branch per kind, only the asked one runs) | places-near.sql.ts |
| api_location_state | 129 ms mean; measured 25 ms p50, 54 ms max | 0.4 ms p50, 3.2 ms max | 5 / 5 | bitmap AND of `gazetteer_places_kind_rank` and `gazetteer_places_admin_code`: the Land by its own key (a Regierungsbezirk's three-digit key began as its Land's and could answer for it) | state-pages.sql.ts |
| api_lookup_ags | 59 ms mean; measured 96 ms p50, 150 ms max | 4.3 ms p50, 23 ms max | 5 / 5 | `gazetteer_places_bkg_city_geom_gist` (the BKG municipality points' partial index) | places-near.sql.ts |
| api_lookup_anchor | 0.9 ms mean; measured 1.1 ms p50 | 1.1 ms p50, 27 ms max (first) | 5 / 5 | `tmc_locations_contract_junction_location` | motorway.sql.ts |
| api_lookup_eva | 14 ms mean; measured 8 ms p50, 40 ms max | 12.5 ms p50, 34 ms max | 5 / 5 | `stops_contract_search_fold` trigram bitmap, the DB station by `db_stations_contract_ifopt`: the same plan as the view's | gazetteer/search.ts |
| api_nearby_junctions | 1-2 ms mean; measured 1.0 ms p50 | 1.5 ms p50, 40 ms max (first) | 5 / 5 | `tmc_locations_contract_junction_location` | motorway.sql.ts |
| api_nearby_need_food | 387 ms mean, up to 1.6 s; measured 730 ms p50, 2.1 s max | 2.2 ms p50, 35 ms max | 5 / 5 | `osm_pois_need_food_geom` nearest-neighbour scan for the fifth distance, then the places within it | places-near.sql.ts |
| api_nearby_need_health | (the same read) measured 361 ms p50, 471 ms max | 1.7 ms p50, 27 ms max | 5 / 5 | `osm_pois_need_health_geom`, the same | places-near.sql.ts |
| api_nearby_need_money | (the same read) measured 177 ms p50, 339 ms max | 3.1 ms p50, 37 ms max | 5 / 5 | `osm_pois_need_money_geom`, the same | places-near.sql.ts |
| api_state_warnings | 7.8 ms mean, up to 76 ms; measured 120 ms p50, 460 ms max | 108 ms p50, 176 ms max | 10 / 5 | bitmap OR on `weather_warnings_live_cell` (the open warnings), the Land by the cell id's digits as before | state-pages.sql.ts |
| api_state_alerts | 7 ms mean; measured 58 ms p50, 349 ms max | 67 ms p50, 93 ms max | 10 / 5 | bitmap scan of `delfi_service_alerts_open`, each publication's check by its key | state-pages.sql.ts |
| api_road_junction_lands | measured 16 ms p50, 104 ms max | 9.8 ms p50, 31 ms max | 5 / 5 | bitmap scan of `tmc_locations_road_number`, each point's Land by `contract_junction_land` | motorway.sql.ts |
| api_road | measured 276 ms p50, 480 ms max | 10.5 ms p50, 16 ms max | 5 / 5 | `autobahn_roads_pkey`, then the shape of that one road: the view drew every road's shape before it filtered | road-pages.sql.ts |
| api_road_coverage | 0.9 ms mean; measured 0.2 ms p50 | 0.2 ms p50, 0.3 ms max | 5 / 5 | sequential scan of `message_coverage_sources` (a few dozen rows) | road-pages.sql.ts |
| api_motorway_exits | measured 11 ms p50, 52 ms max | 8.5 ms p50, 16 ms max | 5 / 5 | bitmap scan of `tmc_locations_road_number`, each exit's Land by `contract_junction_land` | motorway.sql.ts |
| api_motorway_exits_warm | measured 1.6 ms p50, 17 ms max | 1.4 ms p50, 2.3 ms max | 5 / 5 | the same, no Land | motorway.sql.ts |
| api_road_states | measured 63-73 ms p50, 112-147 ms max (ten roads) | 62-79 ms p50, 72-86 ms max | 5 / 5 | the roads' points by `tmc_locations_road_number` (the reference as written, no longer folded); the cost is `contract_junction_land`, once a junction, as before: on a par | road-pages.sql.ts |
| api_lamp_warnings_road | measured 2824 ms p50, 8075 ms max (custom plans only) | 8.3 ms p50, 27 ms max | 5 / 5 | the road's points by `tmc_locations_road_number`, each point's open warnings through `weather_warnings_live_area_geom`: the view joined every junction to every warning | road-pages.sql.ts |
| api_lamp_warnings_near | measured 769 ms p50, 1074 ms max (custom plans only) | 27 ms p50, 108 ms max | 5 / 5 | `weather_warnings_area_gist` on the warning's own geography | road-pages.sql.ts |
| api_lamp_warnings_state | measured 47 ms p50, 280 ms max (custom plans only) | 63 ms p50, 121 ms max | 5 / 5 | bitmap scans of `weather_warnings_live_cell`; the median is a few ms behind, the tail less than half | road-pages.sql.ts |
| api_road_lamp_warnings | measured 20 s, every run cancelled at the statement timeout (ten roads, custom plans only) | 49 ms p50, 59 ms max | 5 / 5 | per road its points by `tmc_locations_road_number`, each point's open warnings through `weather_warnings_live_area_geom`, then the warning by `weather_warnings_pkey` | motorway.sql.ts |
| api_states_road | measured 14.5 ms p50, 63 ms max | 7.2 ms p50, 16 ms max | 5 / 5 | the road's points by `tmc_locations_road_number`, each point's Land by `contract_junction_land` | road-pages.sql.ts |
| api_states_near | measured 8.5 ms p50, 38 ms max | 7.9 ms p50, 11 ms max | 5 / 5 | `tmc_locations_contract_junction_location` within the radius, the nearest point when none | road-pages.sql.ts |
| api_states_state | measured 0.2 ms p50, 2.4 ms max (custom plans only) | 0.2 ms p50, 0.5 ms max | 5 / 5 | the Länder of the region register for de, else the code itself | road-pages.sql.ts |
| api_road_situations | measured 59 ms p50, 129 ms max | 37 ms p50, 78 ms max | 5 / 5 | the road by its reference and its one shape, not every road's; the situations without a Land lookup | road-pages.sql.ts |
| api_area_situations | measured 143 ms p50, 180 ms max (custom plans only) | 76 ms p50, 193 ms max | 5 / 5 | no Land lookup for the situations near the point | road-pages.sql.ts |
| api_state_situations | measured 2221 ms p50, 2883 ms max | 904 ms p50 (Land, 10 codes) | 5 / 5 | only the situations inside the Land's municipalities' box widened by 22 km are read and placed, by the Land rule inlined on the municipalities' GiST; the large Länder stay slow (the nearest-municipality lookup per situation) until the Land is stored | road-pages.sql.ts |
| api_road_messages | measured 42 ms p50, 88 ms max | 46 ms p50, 82 ms max | 5 / 5 | on a par | road-pages.sql.ts |
| api_area_messages | measured 55 ms p50, 98 ms max (custom plans only) | 45 ms p50, 77 ms max | 5 / 5 | no Land lookup | road-pages.sql.ts |
| api_state_messages | measured 1872 ms p50, 2545 ms max | 466 ms p50 (Bayern 950 ms, small Länder 190-400 ms) | 5 / 5 | the same | road-pages.sql.ts |
| api_state_placed | measured 1303 ms p50, 1441 ms max (custom plans only) | 270 ms p50, 898 ms max | 5 / 5 | the same | road-pages.sql.ts |
| api_situation | measured 18 ms p50, 52 ms max | 11 ms p50, 26 ms max | 5 / 5 | `road_events_contract_event_id` for a traffic message; no Land lookup | road-pages.sql.ts |
| api_road_covering | measured 47 ms p50, 247 ms max (custom plans only) | 56 ms p50, 251 ms max | 5 / 5 | on a par | road-pages.sql.ts |
| api_road_lamp_messages | measured 206 ms p50, 395 ms max (ten roads) | 150 ms p50, 262 ms max | 5 / 5 | no Land lookup | motorway.sql.ts |
| api_road_sources | measured 4.9 ms p50, 11 ms max | 3.4 ms p50, 4.2 ms max | 5 / 5 | the registered families by their key, the data status row of each (read as the owner until the API role's grant, database 0337) | road-pages.sql.ts |
| api_motorway_families | measured 369 ms p50, 446 ms max | 307 ms p50, 459 ms max | 5 / 5 | the register by its key, not the road view's shapes | motorway.sql.ts |
| api_motorways | measured 389 ms p50, 532 ms max | 399 ms p50, 616 ms max | 5 / 5 | on a par: the situations in force grouped by road | motorway.sql.ts |
| api_motorways_live_land | measured 643 ms p50, 1104 ms max | 538 ms p50, 708 ms max | 5 / 5 | a road's junction Land by `contract_junction_land`, only for the roads in question | motorway.sql.ts |
| api_road_shapes | 2.4 s (the road list's shape read); measured 481 ms p50, 1.3 s max | 264 ms p50, 445 ms max | no parameters | `autobahn_roads_pkey`, `contract_road_shape` once a road | road-pages.sql.ts |

The state and road rows were measured with the view statement run first in
the transaction (the first statement run warms the pages both read); run
second, each view statement was as fast as the named one or faster, so
`api_state_warnings` and `api_state_alerts` are even with their views, not
faster: their cost is the open warnings and alerts they aggregate, read
through the same index as before. The `pg_stat_statements` means of the
state reads (7-8 ms) lie well below both measurements here; why is not
known yet.

The need read named its kinds as a parameter, which no partial index can
match in a plan made before the values are known; each need is now its own
statement with its kinds written out, so each plan reads that need's own
partial GiST. The other needs (groceries, garage, lodging, police, toilets)
are the same statement over their own index.

The address reads ordered by the distance and the id: the second key turned
the nearest-neighbour scan into a sort of every address within the reach
(28 000 in a city, 155 ms); they now read the nearest distance by the
index's own order and then only the addresses at that distance, which
answers the same row.
