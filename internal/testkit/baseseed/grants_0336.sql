-- Test-only, until database.pin reaches the release that ships it: the
-- grants of viafrei-database migration 0336 (api_reader reads the platform
-- relations the API's statements read), copied as written.

-- api_reader reads the platform's tables (step a of the contract retirement).
--
-- viafrei-api stops reading the contract views (0307, 0312) and states its
-- reads over the platform's own tables, as the platform's own queries do. This
-- release grants api_reader SELECT on exactly the relations the 50 contract
-- views read today: the tables, and the views between (control.data_status,
-- feed_checks, the *_live views, contract_source_rules, contract_source_domains,
-- contract_sources), each named, never "all tables in schema", so a table made
-- later is not granted by accident. USAGE on control and domains, the two
-- schemas besides public that hold one of them. Table-level grants only (owner,
-- 2026-11-23): no column grants.
--
-- Nothing is revoked or dropped: the contract views and their grants stay
-- until the API that no longer reads them is deployed; a later release drops
-- them. The views between are owner-rights views (no security_invoker), so the
-- tables under them (feed_catalog, registered_sources, runtime_config,
-- source_schedule, control.budgets, control.feed_state, control.refusals,
-- lez_zones, lorry_*, station_slugs, gtfs_untimed_trips) stay unreadable to
-- api_reader; so do access.* and every other relation. Through the security
-- definer functions api_reader already reaches (contract_stop_place_of) nothing
-- changes.
--
-- The licence and confidentiality rules the views apply (contract_source_rules:
-- excluded sources, own-key audiences, the SID terms gate, consumer-only
-- sources; DATEX II confidentiality; private charging sites; share-alike
-- families kept apart) are not enforced by these grants: the API's own
-- statements must apply them, as the views do, before it reads a table here.
--
-- A GRANT takes no lock on the relation; the bound costs nothing.
--
-- rollback:
--   revoke usage on schema control, domains from api_reader;
--   revoke select on public.ags_regions from api_reader;
--   revoke select on public.autobahn_events from api_reader;
--   revoke select on public.autobahn_roads from api_reader;
--   revoke select on public.autobahn_sweeps from api_reader;
--   revoke select on public.charging_point_status from api_reader;
--   revoke select on public.charging_points from api_reader;
--   revoke select on public.charging_sites from api_reader;
--   revoke select on public.contract_source_domains from api_reader;
--   revoke select on public.contract_source_rules from api_reader;
--   revoke select on public.contract_sources from api_reader;
--   revoke select on control.data_status from api_reader;
--   revoke select on public.db_facilities from api_reader;
--   revoke select on public.db_stations from api_reader;
--   revoke select on public.delfi_service_alerts from api_reader;
--   revoke select on public.delfi_siri_et_calls_latest from api_reader;
--   revoke select on public.delfi_siri_et_stop_pairs from api_reader;
--   revoke select on domains.train_board_departures from api_reader;
--   revoke select on public.driving_rule_facts from api_reader;
--   revoke select on public.driving_rule_topics from api_reader;
--   revoke select on public.feed_checks from api_reader;
--   revoke select on public.feeds from api_reader;
--   revoke select on public.fuel_prices from api_reader;
--   revoke select on public.fuel_products from api_reader;
--   revoke select on public.fuel_stations from api_reader;
--   revoke select on public.gazetteer_places from api_reader;
--   revoke select on public.gbfs_station_status from api_reader;
--   revoke select on public.gbfs_stations from api_reader;
--   revoke select on public.gbfs_systems from api_reader;
--   revoke select on public.gbfs_vehicles from api_reader;
--   revoke select on public.gtfs_service_dates from api_reader;
--   revoke select on public.hh_cameras from api_reader;
--   revoke select on public.hydrogen_stations from api_reader;
--   revoke select on public.lez_feed_places from api_reader;
--   revoke select on public.lez_list_entries from api_reader;
--   revoke select on public.lez_zone_overlaps from api_reader;
--   revoke select on public.lez_zones_live from api_reader;
--   revoke select on public.licence_texts from api_reader;
--   revoke select on public.lorry_city_live from api_reader;
--   revoke select on public.lorry_hb_live from api_reader;
--   revoke select on public.lorry_ivm_live from api_reader;
--   revoke select on public.lorry_long_network_live from api_reader;
--   revoke select on public.lorry_sevas_live from api_reader;
--   revoke select on public.message_coverage_sources from api_reader;
--   revoke select on public.osm_addresses from api_reader;
--   revoke select on public.osm_pois from api_reader;
--   revoke select on public.osm_speed_cameras from api_reader;
--   revoke select on public.parking_facilities from api_reader;
--   revoke select on public.parking_facilities_bysa from api_reader;
--   revoke select on public.parking_facilities_odbl from api_reader;
--   revoke select on public.parking_facilities_sid from api_reader;
--   revoke select on public.parking_occupancy from api_reader;
--   revoke select on public.parking_occupancy_odbl from api_reader;
--   revoke select on public.parking_occupancy_sid from api_reader;
--   revoke select on public.road_event_ticks from api_reader;
--   revoke select on public.road_events from api_reader;
--   revoke select on public.routes from api_reader;
--   revoke select on public.source_register from api_reader;
--   revoke select on public.source_register_notes from api_reader;
--   revoke select on public.source_terms from api_reader;
--   revoke select on public.source_terms_texts from api_reader;
--   revoke select on public.speed_enforcement from api_reader;
--   revoke select on public.station_equipment from api_reader;
--   revoke select on public.station_equipment_state from api_reader;
--   revoke select on public.station_index_live from api_reader;
--   revoke select on public.stop_times from api_reader;
--   revoke select on public.stops from api_reader;
--   revoke select on public.street_bay_state from api_reader;
--   revoke select on public.street_bays from api_reader;
--   revoke select on public.tmc_locations from api_reader;
--   revoke select on public.transit_delays from api_reader;
--   revoke select on public.transit_delays_live from api_reader;
--   revoke select on public.transit_punctuality_area_live from api_reader;
--   revoke select on public.transit_stop_modes from api_reader;
--   revoke select on public.trips from api_reader;
--   revoke select on public.weather_warnings from api_reader;
--   delete from schema_migrations where version = 336;

set local lock_timeout = '5s';

grant usage on schema control, domains to api_reader;

grant select on public.ags_regions to api_reader;
grant select on public.autobahn_events to api_reader;
grant select on public.autobahn_roads to api_reader;
grant select on public.autobahn_sweeps to api_reader;
grant select on public.charging_point_status to api_reader;
grant select on public.charging_points to api_reader;
grant select on public.charging_sites to api_reader;
grant select on public.contract_source_domains to api_reader;
grant select on public.contract_source_rules to api_reader;
grant select on public.contract_sources to api_reader;
grant select on control.data_status to api_reader;
grant select on public.db_facilities to api_reader;
grant select on public.db_stations to api_reader;
grant select on public.delfi_service_alerts to api_reader;
grant select on public.delfi_siri_et_calls_latest to api_reader;
grant select on public.delfi_siri_et_stop_pairs to api_reader;
grant select on domains.train_board_departures to api_reader;
grant select on public.driving_rule_facts to api_reader;
grant select on public.driving_rule_topics to api_reader;
grant select on public.feed_checks to api_reader;
grant select on public.feeds to api_reader;
grant select on public.fuel_prices to api_reader;
grant select on public.fuel_products to api_reader;
grant select on public.fuel_stations to api_reader;
grant select on public.gazetteer_places to api_reader;
grant select on public.gbfs_station_status to api_reader;
grant select on public.gbfs_stations to api_reader;
grant select on public.gbfs_systems to api_reader;
grant select on public.gbfs_vehicles to api_reader;
grant select on public.gtfs_service_dates to api_reader;
grant select on public.hh_cameras to api_reader;
grant select on public.hydrogen_stations to api_reader;
grant select on public.lez_feed_places to api_reader;
grant select on public.lez_list_entries to api_reader;
grant select on public.lez_zone_overlaps to api_reader;
grant select on public.lez_zones_live to api_reader;
grant select on public.licence_texts to api_reader;
grant select on public.lorry_city_live to api_reader;
grant select on public.lorry_hb_live to api_reader;
grant select on public.lorry_ivm_live to api_reader;
grant select on public.lorry_long_network_live to api_reader;
grant select on public.lorry_sevas_live to api_reader;
grant select on public.message_coverage_sources to api_reader;
grant select on public.osm_addresses to api_reader;
grant select on public.osm_pois to api_reader;
grant select on public.osm_speed_cameras to api_reader;
grant select on public.parking_facilities to api_reader;
grant select on public.parking_facilities_bysa to api_reader;
grant select on public.parking_facilities_odbl to api_reader;
grant select on public.parking_facilities_sid to api_reader;
grant select on public.parking_occupancy to api_reader;
grant select on public.parking_occupancy_odbl to api_reader;
grant select on public.parking_occupancy_sid to api_reader;
grant select on public.road_event_ticks to api_reader;
grant select on public.road_events to api_reader;
grant select on public.routes to api_reader;
grant select on public.source_register to api_reader;
grant select on public.source_register_notes to api_reader;
grant select on public.source_terms to api_reader;
grant select on public.source_terms_texts to api_reader;
grant select on public.speed_enforcement to api_reader;
grant select on public.station_equipment to api_reader;
grant select on public.station_equipment_state to api_reader;
grant select on public.station_index_live to api_reader;
grant select on public.stop_times to api_reader;
grant select on public.stops to api_reader;
grant select on public.street_bay_state to api_reader;
grant select on public.street_bays to api_reader;
grant select on public.tmc_locations to api_reader;
grant select on public.transit_delays to api_reader;
grant select on public.transit_delays_live to api_reader;
grant select on public.transit_punctuality_area_live to api_reader;
grant select on public.transit_stop_modes to api_reader;
grant select on public.trips to api_reader;
grant select on public.weather_warnings to api_reader;
