# SQL plans: the API's statements on the platform's tables

The API reads the platform's base tables only; no contract view remains.
Each read is a named prepared statement (`api_<what>`) with typed
parameters, prepared once per connection. Every parameter is read through
a subquery (`(select $n::type)`), so a custom plan sees no more than the
generic one and the plan cache settles on its generic plan after the five
custom plans; `plan_cache_mode` stays `auto`.

How each statement is proved:

- **Rows.** In every integration test each named statement first runs
  beside the view statement it replaced, in a savepoint of the same
  transaction, under the custom and under the generic plan; the suite fails
  on other rows, and a full run fails on a statement no test ran. The tuned
  statements were also run beside their view statements on prod, in one
  snapshot (same rows: affected stations, affected places, facilities,
  station search, the energy and road summaries).
- **Plans.** On prod, read only, with the host load at or under 20:
  `PREPARE`, eight `EXECUTE`s, `pg_prepared_statements` (generic/custom
  plans), `EXPLAIN (ANALYZE, BUFFERS)`, `DEALLOCATE`, one transaction per
  statement. `3/5` below means the generic plan was chosen from the sixth
  run: settled.

## Statements (lane A: stations, disruptions, national, readiness)

Times in ms on prod, p50/max of eight runs at host load 8–14 (Berlin
evening, 2026-11-23). "View" is the view statement this one replaced, from
the earlier replay of the same arguments (another hour, another load);
equal buffers mean the same plan, and a difference in time is the host.

| Statement | New p50/max | Plans g/c | Buffers | View p50/max | What changed | Platform twin |
|-|-|-|-|-|-|-|
| api_places_page | 10–40 | settled | small | — | parameters opaque, motorway source gate | site place search |
| api_kreis_names | < 5 | settled | small | — | mechanical | site place search |
| api_station_by_id | 2.0/8.1 | 3/5 | 30 | 1.4/2.0 | mechanical | station page |
| api_stations_page_NN (text) | 51/156 | 3/5 | 375 | 14.9/8194 (never settled, 0/8) | one statement per search shape | site station search |
| api_stations_page_NN (near) | 52/112 | 3/5 | 10 117 | (as above) | per shape | site nearby |
| api_stations_page_NN (near, railway) | 126/571 | 3/5 | 35 170 | (as above) | per shape | site nearby |
| api_stations_page_NN (code) | 454/588 | 4/5 | 14 192 | 6 464 (per shape, before) | per shape, code narrowed by the stops' id index and the EVA register | station page by code |
| api_board_calls | 122/200 (three stations, one to three hours) | 4/5 | 58 684 | over 60 s (a live window; 19 ms only on a day with no calls) | the trip's other calls and its last stop read on the stop times' trip key: the view statement hashed every scheduled call of the network | station board |
| api_station_facilities | 214/356 | 3/5 | 5 863 | 981/1 433 | only the refs of the station's DHID | station lifts |
| api_affected_alerts | 22/27 | 3/5 | 1 100 | 5.7/15 | mechanical (same buffers) | station disruptions |
| api_affected_lifts | 72/129 | 3/5 | 9 248 | 72/146 | mechanical | station lifts |
| api_affected_calls | 2 495/4 663 | 3/5 | 110 562 | 7 293/11 597 | recently checked sources read once; sums and sources in one aggregate per station (a join of two aggregates looped, 9 s under load) | station disruptions |
| api_affected_places | 430/1 263 | 3/5 | 16 786 | 5 668/6 873 | refs found through the stops' indexes | station disruptions |
| api_service_alerts | 42/112 | 3/5 | 352 | — | mechanical | region alerts |
| api_alerts_summary | 21/49 | 3/5 | 369 | — | mechanical | region alerts |
| api_area_punctuality | 4.3/8.8 | 3/5 | 0 | 4.8/20.3 | mechanical | region punctuality |
| api_area_trips | 2.2/2.7 | 3/5 | 0 | 1.3/2.1 | mechanical | region punctuality |
| api_line_in_timetable | 37/59 | 3/5 | 368 | 69/97 | mechanical | line pages |
| api_national_energy | 1 309/1 509 | 3/5 | 246 135 | 4 547/8 445 (675 045; 7 268 inlined, same hour) | source rules once per family; points counted per site first (A/B in one session: 1 901 against 2 265) | national page |
| api_national_roads | 1 757/2 355 | 3/5 | 118 008 | 3 938/6 511 (235 971, the view inlined, same hour) | events in force read once for both parts; source rules matched once per family (A/B in one session: 1 561 against 1 811) | national page |
| api_national_parking | 176/253 | 3/5 | 1 406 | 109/152 | mechanical (same buffers) | national page |
| api_national_accessibility | 439/472 | 3/5 | 43 299 | 1 325/1 491 (785/941 earlier) | each station ref mapped to its stop place once, not per facility | national page |
| api_national_weather | 225/352 (288 beside the view's 398, one snapshot) | 3/5 | 19 267 | 27/40 at an hour with few warnings; 398 with 20 535 in force | mechanical | national page |
| api_national_transit | 6.8/63 | 3/5 | 0 | 2.2/4.2 | mechanical | national page |
| api_storage_answers | < 1 | — | 0 | — | readiness: storage answers; the contract version is the build's | — |

The other families are listed by their lanes below: lane B (features,
fuel, hydrogen, sharing, bays, speed, lorry, POI, address, weather,
coverage, freshness, register, train board) and lane C (locations, states,
lookup, nearby, catalogue, roads, road shapes).

## Statements (lane B: features, fuel, hydrogen, sharing, bays, speed, lorry, POI, address, weather, coverage, freshness, register, train board)

Each named statement of the store, proved on prod read-only (a repeatable
read, read-only transaction as the API's role reads, `jit` off, statement
timeout 15 s): prepared once, executed ten times with ten argument sets,
then `pg_prepared_statements` read for the plans taken and the last
execution explained with `analyze, buffers`. "Before" is the view statement
it replaced, as `pg_stat_statements` held it on prod the same day (mean per
call). Times are per execution, after the first.


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

| api_hydrogen_near | under 1 ms | 0.3-0.8 ms | 5 / 5 | index scan of `hydrogen_stations_geom_gist` (the view's geometry cast read every station) |
| api_hydrogen_nearest | under 1 ms | 0.5-1 ms | 5 / 5 | nearest-neighbour scan of the same index |
| api_bays_near | 6 ms mean | 0.2-16 ms | 5 / 5 | index scan of `street_bays_geom_gist` |
| api_lorry_rules | 13 ms mean | 6-127 ms | 0 / 10 (custom by design: the road, the point and the layer decide the plan) | each lorry table's `*_geom_open` area index on its own geography |
| api_poi_match | 608 ms mean, up to 2.1 s | 6-1 300 ms by branch (below) | 8 / 5 | opaque, one generic plan with a branch for each way the match is asked, only the asked one runs (below) |
| api_train_board | under 1 ms | not measured: the board table is empty on prod | - | - |
| api_sharing_stations_near | 50 ms mean | 0.7-7 ms | 5 / 5 | index scan of `gbfs_stations_geom_open`, each count by the status key |
| api_sharing_vehicles_near | 58 ms mean | 0.3-4 ms | 5 / 5 | index scan of `gbfs_vehicles_geom` |
| api_sharing_nearest | 46 ms mean | 5-33 ms | 5 / 5 | nearest-neighbour scan of `gbfs_stations_geom_open` |
| api_speed_official | 5 ms mean | 0.7-6 ms | 5 / 5 | the official cameras' area indexes |
| api_speed_osm | 139 ms mean, up to 320 ms | 89-177 ms | 5 / 5 | the Gemeinde cut (BKG points within 80 km) is read on the points' surface, which no index holds, as before |
| api_register | 61 ms mean, up to 1.4 s (7330 calls) | 18-35 ms | no parameters | the view's freshness lateral on control.data_status is not read: no column of the register used it |
| api_sources | 135 ms mean, up to 402 ms | 20-34 ms | 5 / 5 | one source set serves both the attribution and the licence's shared terms link |
| api_terms_texts | | 0.1-1.6 ms | 5 / 5 | |
| api_charging_near | 704 ms mean, up to 1.2 s | 6-58 ms warm, 30-960 ms cold | 5 / 5 | `charging_sites_geom_gist` nearest walk, the points by `charging_points_site`; each point's report probes the 18 status partitions by key, as before |
| api_charging_by_id | | 0.8-29 ms | 8 / 5 | the sites' key |
| api_charging_in_radius | 24 ms mean | 3-13 ms | 5 / 5 | |
| api_place_by_id | 162 ms mean, up to 256 ms | 0.1-3.5 ms | 5 / 5 | `gazetteer_places_pkey`: the view's text id matched no index |
| api_parking_near | 2.6 s mean, up to 7.4 s | 2-97 ms | 5 / 5 | the facilities' location indexes; each facility's newest report is one backward probe of its own table's key, where the view's composed id read every report and sorted them |
| api_parking_next_near | | 4-52 ms | 5 / 5 | |
| api_freshness_versions | 612 ms mean, up to 1.6 s | 386 ms-1.3 s | no parameters | the same fingerprint, byte for byte; most of it is the motorway junctions' Land, as before |
| api_provenance | 84 ms mean | 73-127 ms | 5 / 5 | about as before: the published register is read whole for the licence's shared terms link |

`api_poi_match` is opaque like every other read: it was planned per call
(0 generic plans of 10), as a generic plan could not tell which of the
text, the town and the point was set. The one generic plan now holds a
branch for each (a one-time filter on the parameters runs only the one
asked): the town's places through `osm_pois_place_kind`; else the places
within the radius through `osm_pois_geom`; else, for a text of three
characters or more, a BitmapOr of the name and the brand trigram indexes;
else (a text of one or two characters, of no trigram) a read of the
table, as no trigram index can narrow it. The rows are the match's own
(parity cases for every branch, two-character and brand texts among them).

Production, read only, 2026-11-23, 13 runs of one prepared statement
(varied texts, towns, points, a category), then 8 generic and 5 custom
plans:

| Asked | Runs (generic plan) |
|-|-|
| a town (berlin, hamburg, muenchen) | 4-63 ms |
| a point and a radius | 19-108 ms |
| a text of 3 or more characters, anywhere | 640 ms-1.3 s: production has no trigram index on `lower(brand)` yet, so the brand side reads every brand (175 000) and rechecks them |
| a text of 2 characters, anywhere | 3.3 s, a read of the table (2 M rows), over the 2 s deadline under the load of the hour; the per-call plan of such a text was not measured |

On a test database built to viafrei-database 0.5.0's indexes (0337 adds
`osm_pois_brand_lower_trgm`), 500 000 places: the text branch is a BitmapOr
of `osm_pois_name_trgm` and `osm_pois_brand_lower_trgm` in the generic
plan, 69 ms for a text anywhere; a town 0.1-0.2 ms, a point 0.2 ms; the
two-character text reads the table, 290 ms; 8 generic plans of 13 runs.

`api_coverage_addresses`, root cause: no index holds the open addresses by
Land and extract date, so the newest extract of a Land reads every address
of the Land (19.4 M rows, 14 GB in all). The statement already reads in
that order (`order by a.extract_date desc nulls last limit 1` under
`a.land = l.land`); viafrei-database item: an index on the address table's
`(land, extract_date desc nulls last) where closed_at is null`, which turns
each Land's probe into the first entry of its range.

The view cast the area to `geometry(MultiPolygon,4326)`, an expression no
index holds, so the box test read every warning's area; the statements test
the box on `area::geometry` (the open rows' index) or on the geography
`area` (every row's index) instead.

## Statements (lane C: locations, states, lookup, nearby, catalogue, roads, road shapes)

Proved 2026-11-23 the same way, with a statement timeout of 20 s; each
"before" also measured in the same transaction (the frozen view statement,
prepared and executed with the same arguments), beside the
`pg_stat_statements` figure. p50 and max over the executions.

| statement | before (view) | after | plans (generic/custom) | plan of the last execution | platform twin |
|-|-|-|-|-|-|
| api_location_address | 1.1 ms mean, up to 15 s (timeouts); measured: every execution cancelled at 20 s | 8.6 ms p50, 74 ms max | 5 / 5 | `osm_addresses_geom_gist` nearest-neighbour scan for the nearest distance, then the ties within a metre of it | site place search |
| api_location_sectors | up to 15 s (timeouts); measured: cancelled at 20 s | 33 ms p50, 237 ms max | 5 / 5 | one `osm_addresses_geom_gist` probe per wedge | site place search |
| api_location_abroad_family | up to 15 s (timeouts); measured: cancelled at 20 s | 4.5 ms p50, 149 ms max | 5 / 5 | `osm_addresses_geom_gist` nearest-neighbour scan, then the ties | site place search |
| api_location_place | 290 ms mean, up to 801 ms; measured 50 ms p50, 1.5 s max | 4.4 ms p50, 48 ms max | 25 / 5 | municipality: `gazetteer_places_geom_gist` within the reach; district and Land: `gazetteer_places_kind_rank` (one branch per kind, only the asked one runs) | site nearby |
| api_location_state | 129 ms mean; measured 25 ms p50, 54 ms max | 0.4 ms p50, 3.2 ms max | 5 / 5 | bitmap AND of `gazetteer_places_kind_rank` and `gazetteer_places_admin_code`: the Land by its own key (a Regierungsbezirk's three-digit key began as its Land's and could answer for it) | state pages |
| api_lookup_ags | 59 ms mean; measured 96 ms p50, 150 ms max | 4.3 ms p50, 23 ms max | 5 / 5 | `gazetteer_places_bkg_city_geom_gist` (the BKG municipality points' partial index) | site nearby |
| api_lookup_anchor | 0.9 ms mean; measured 1.1 ms p50 | 1.1 ms p50, 27 ms max (first) | 5 / 5 | `tmc_locations_contract_junction_location` | motorway pages |
| api_lookup_eva | 14 ms mean; measured 8 ms p50, 40 ms max | 12.5 ms p50, 34 ms max | 5 / 5 | `stops_contract_search_fold` trigram bitmap, the DB station by `db_stations_contract_ifopt`: the same plan as the view's | site place search |
| api_nearby_junctions | 1-2 ms mean; measured 1.0 ms p50 | 1.5 ms p50, 40 ms max (first) | 5 / 5 | `tmc_locations_contract_junction_location` | motorway pages |
| api_nearby_need_food | 387 ms mean, up to 1.6 s; measured 730 ms p50, 2.1 s max | 2.2 ms p50, 35 ms max | 5 / 5 | `osm_pois_need_food_geom` nearest-neighbour scan for the fifth distance, then the places within it | site nearby |
| api_nearby_need_health | (the same read) measured 361 ms p50, 471 ms max | 1.7 ms p50, 27 ms max | 5 / 5 | `osm_pois_need_health_geom`, the same | site nearby |
| api_nearby_need_money | (the same read) measured 177 ms p50, 339 ms max | 3.1 ms p50, 37 ms max | 5 / 5 | `osm_pois_need_money_geom`, the same | site nearby |
| api_state_warnings | 7.8 ms mean, up to 76 ms; measured 120 ms p50, 460 ms max | 108 ms p50, 176 ms max | 10 / 5 | bitmap OR on `weather_warnings_live_cell` (the open warnings), the Land by the cell id's digits as before | state pages |
| api_state_alerts | 7 ms mean; measured 58 ms p50, 349 ms max | 67 ms p50, 93 ms max | 10 / 5 | bitmap scan of `delfi_service_alerts_open`, each publication's check by its key | state pages |
| api_road_junction_lands | measured 16 ms p50, 104 ms max | 9.8 ms p50, 31 ms max | 5 / 5 | bitmap scan of `tmc_locations_road_number`, each point's Land by `contract_junction_land` | motorway pages |
| api_road | measured 276 ms p50, 480 ms max | 10.5 ms p50, 16 ms max | 5 / 5 | `autobahn_roads_pkey`, then the shape of that one road: the view drew every road's shape before it filtered | road pages |
| api_road_coverage | 0.9 ms mean; measured 0.2 ms p50 | 0.2 ms p50, 0.3 ms max | 5 / 5 | sequential scan of `message_coverage_sources` (a few dozen rows) | road pages |
| api_motorway_exits | measured 11 ms p50, 52 ms max | 8.5 ms p50, 16 ms max | 5 / 5 | bitmap scan of `tmc_locations_road_number`, each exit's Land by `contract_junction_land` | motorway pages |
| api_motorway_exits_warm | measured 1.6 ms p50, 17 ms max | 1.4 ms p50, 2.3 ms max | 5 / 5 | the same, no Land | motorway pages |
| api_road_states | measured 63-73 ms p50, 112-147 ms max (ten roads) | 62-79 ms p50, 72-86 ms max | 5 / 5 | the roads' points by `tmc_locations_road_number` (the reference as written, no longer folded); the cost is `contract_junction_land`, once a junction, as before: on a par | road pages |
| api_lamp_warnings_road | measured 2824 ms p50, 8075 ms max (custom plans only) | 8.3 ms p50, 27 ms max | 5 / 5 | the road's points by `tmc_locations_road_number`, each point's open warnings through `weather_warnings_live_area_geom`: the view joined every junction to every warning | road pages |
| api_lamp_warnings_near | measured 769 ms p50, 1074 ms max (custom plans only) | 27 ms p50, 108 ms max | 5 / 5 | `weather_warnings_area_gist` on the warning's own geography | road pages |
| api_lamp_warnings_state | measured 47 ms p50, 280 ms max (custom plans only) | 63 ms p50, 121 ms max | 5 / 5 | bitmap scans of `weather_warnings_live_cell`; the median is a few ms behind, the tail less than half | road pages |
| api_road_lamp_warnings | measured 20 s, every run cancelled at the statement timeout (ten roads, custom plans only) | 49 ms p50, 59 ms max | 5 / 5 | per road its points by `tmc_locations_road_number`, each point's open warnings through `weather_warnings_live_area_geom`, then the warning by `weather_warnings_pkey` | motorway pages |
| api_states_road | measured 14.5 ms p50, 63 ms max | 7.2 ms p50, 16 ms max | 5 / 5 | the road's points by `tmc_locations_road_number`, each point's Land by `contract_junction_land` | road pages |
| api_states_near | measured 8.5 ms p50, 38 ms max | 7.9 ms p50, 11 ms max | 5 / 5 | `tmc_locations_contract_junction_location` within the radius, the nearest point when none | road pages |
| api_states_state | measured 0.2 ms p50, 2.4 ms max (custom plans only) | 0.2 ms p50, 0.5 ms max | 5 / 5 | the Länder of the region register for de, else the code itself | road pages |
| api_road_situations | measured 59 ms p50, 129 ms max | 37 ms p50, 78 ms max | 5 / 5 | the road by its reference and its one shape, not every road's; the situations without a Land lookup | road pages |
| api_area_situations | measured 143 ms p50, 180 ms max (custom plans only) | 76 ms p50, 193 ms max | 5 / 5 | no Land lookup for the situations near the point | road pages |
| api_state_situations | measured 2221 ms p50, 2883 ms max | 904 ms p50 (10 codes) with the Land looked up only near the Land; the stored Land (database 0338) is not on prod yet, so its time is not measured | 5 / 5 | the Land is the stored land_code (an Autobahn row written before it was stored: the same rule, looked up); a Land's read selects only its own situations, the traffic messages through road_events_open_land; de reads all | road pages |
| api_road_messages | measured 42 ms p50, 88 ms max | 46 ms p50, 82 ms max | 5 / 5 | on a par | road pages |
| api_area_messages | measured 55 ms p50, 98 ms max (custom plans only) | 45 ms p50, 77 ms max | 5 / 5 | no Land lookup | road pages |
| api_state_messages | measured 1872 ms p50, 2545 ms max | 466 ms p50 (Bayern 950 ms) before the stored Land; with it not measured yet | 5 / 5 | the same | road pages |
| api_state_placed | measured 1303 ms p50, 1441 ms max (custom plans only) | 270 ms p50, 898 ms max before the stored Land; with it not measured yet | 5 / 5 | the same | road pages |
| api_situation | measured 18 ms p50, 52 ms max | 11 ms p50, 26 ms max | 5 / 5 | `road_events_contract_event_id` for a traffic message; no Land lookup | road pages |
| api_road_covering | measured 47 ms p50, 247 ms max (custom plans only) | 56 ms p50, 251 ms max | 5 / 5 | on a par | road pages |
| api_road_lamp_messages | measured 206 ms p50, 395 ms max (ten roads) | 150 ms p50, 262 ms max | 5 / 5 | no Land lookup | motorway pages |
| api_road_sources | measured 4.9 ms p50, 11 ms max | 3.4 ms p50, 4.2 ms max | 5 / 5 | the registered families by their key, the data status row of each (read as the owner until the API role's grant, database 0337) | road pages |
| api_motorway_families | measured 369 ms p50, 446 ms max | 307 ms p50, 459 ms max | 5 / 5 | the register by its key, not the road view's shapes | motorway pages |
| api_motorways | measured 389 ms p50, 532 ms max | 399 ms p50, 616 ms max | 5 / 5 | on a par: the situations in force grouped by road | motorway pages |
| api_motorways_live_land | measured 643 ms p50, 1104 ms max | 538 ms p50, 708 ms max | 5 / 5 | a road's junction Land by `contract_junction_land`, only for the roads in question | motorway pages |
| api_road_shapes | 2.4 s (the road list's shape read); measured 481 ms p50, 1.3 s max | 264 ms p50, 445 ms max | no parameters | `autobahn_roads_pkey`, `contract_road_shape` once a road | road pages |

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

## Re-plan risks and what holds them

| Risk | Where | What holds it |
|-|-|-|
| A generic plan that cannot see an optional filter (one statement, every filter optional) | station search | one statement per search shape; each holds only its filters |
| A parameter's value changes the best plan (short text, a big area) | place search with a text under three characters (8.5 s, as before) | needs a prefix index on the folded names (DB item below); the API keeps the shape |
| Per-row source rules over large tables | charging, traffic events, every relation with a source-rule join | match the rules once per source (done for the energy summary; the same form fits the others) |
| A correlated subquery over a CTE per output row | affected stations (was 3 million row checks) | aggregate once, join |
| Sorts of concatenated ids spilling to disk | energy summary | join on the key columns, aggregate before the join |
| An index the platform drops | real-time departures: `(stop_ref, COALESCE(expected_dep, aimed_dep))` is to go | every real-time departure read bounds `aimed_dep` and is served by `(stop_ref, aimed_dep)`; none relies on the coalesce index |
| Statistics drift after a large load | all | auto mode re-plans when the generic plan's cost rises above the custom plans'; the probes above are the baseline to compare against |

## Database items (for a viafrei-database release)

- No folded-name index for English names (`name_en`): an English place
  search reads every place.
- Texts under three characters cannot use the trigram index: a prefix
  btree on the folded name (`text_pattern_ops`) serves them.
- The drop of the real-time departures' coalesce index is coordinated: no API statement needs it.
- The DB station register's EVA number has no index (8 800 rows, a scan of about 1 ms; not
  needed now).
