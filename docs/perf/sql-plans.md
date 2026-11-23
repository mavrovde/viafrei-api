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
| api_affected_calls | 2 495/4 663 | 3/5 | 110 562 | 7 293/11 597 | recently checked sources read once; sources per station aggregated once | station disruptions |
| api_affected_places | 430/1 263 | 3/5 | 16 786 | 5 668/6 873 | refs found through the stops' indexes | station disruptions |
| api_service_alerts | 42/112 | 3/5 | 352 | — | mechanical | region alerts |
| api_alerts_summary | 21/49 | 3/5 | 369 | — | mechanical | region alerts |
| api_area_punctuality | 4.3/8.8 | 3/5 | 0 | 4.8/20.3 | mechanical | region punctuality |
| api_area_trips | 2.2/2.7 | 3/5 | 0 | 1.3/2.1 | mechanical | region punctuality |
| api_line_in_timetable | 37/59 | 3/5 | 368 | 69/97 | mechanical | line pages |
| api_national_energy | 1 309/1 509 | 3/5 | 246 135 | 4 547/8 445 (675 045; 7 268 inlined, same hour) | source rules once per source; points counted per site first | national page |
| api_national_roads | 1 757/2 355 | 3/5 | 118 008 | 3 938/6 511 (235 971, the view inlined, same hour) | events in force read once for both parts | national page |
| api_national_parking | 176/253 | 3/5 | 1 406 | 109/152 | mechanical (same buffers) | national page |
| api_national_accessibility | 439/472 | 3/5 | 43 299 | 1 325/1 491 (785/941 earlier) | each station ref mapped to its stop place once, not per facility | national page |
| api_national_weather | 225/352 (288 beside the view's 398, one snapshot) | 3/5 | 19 267 | 27/40 at an hour with few warnings; 398 with 20 535 in force | mechanical | national page |
| api_national_transit | 6.8/63 | 3/5 | 0 | 2.2/4.2 | mechanical | national page |
| api_storage_answers | < 1 | — | 0 | — | readiness: storage answers; the contract version is the build's | — |

The other families (features, fuel, hydrogen, sharing, bays, speed, lorry,
POI, address, weather, coverage, freshness, register, train board,
locations, states, lookup, nearby, catalogue, roads, road shapes) are
listed by their lanes below this table when they merge.

## Re-plan risks and what holds them

| Risk | Where | What holds it |
|-|-|-|
| A generic plan that cannot see an optional filter (one statement, every filter optional) | station search | one statement per search shape; each holds only its filters |
| A parameter's value changes the best plan (short text, a big area) | place search with a text under three characters (8.5 s, as before) | needs a prefix index on the folded names (DB item below); the API keeps the shape |
| Per-row source rules over large tables | charging, traffic events, every relation with a source-rule join | match the rules once per source (done for the energy summary; the same form fits the others) |
| A correlated subquery over a CTE per output row | affected stations (was 3 million row checks) | aggregate once, join |
| Sorts of concatenated ids spilling to disk (work_mem 32 MB) | energy summary | join on the key columns, aggregate before the join |
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

## UI load

The site will read the API in place of its own read service. That read
service's statements on prod since 2026-11-14 20:14 UTC (about nine days):
339 million calls, 2.07 million s of execution and 0.27 million s of
planning: on average about 440 calls a second and 3.0 s of database time a
second, three backends busy, with 12 of its 133 connections active at the
sample. The API's statements above are the same reads on the same tables;
its plans settle, so planning drops to the first five runs of each
statement on each connection.

## Pool proposal

Today the read service runs a pool of 56 per replica (133 connections), the
API's reader role is limited to 16 (`db.pool_max` at most 16), and the
server allows 300.

| Pool | Role | Size | Who |
|-|-|-|-|
| interactive | api_reader | 20 | people and AI clients (the tiers' permits) |
| bots | api_reader (own pool, own application name) | 8 | verified search and AI bots, paced; a wave of bot requests waits in its pool and never takes an interactive permit |
| access store | api_access | 2 | keys, metering |

That needs the reader role's connection limit raised from 16 to 32 (a
database release) and `db.pool_max` with a bot pool beside it (an API
release). Three busy backends on average leave the interactive pool room
for peaks of seven times the mean; the read service's 133 connections are
released as the site moves over.
