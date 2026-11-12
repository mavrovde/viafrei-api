-- Physical shape of the read-contract stub: partitions and indexes.
--
-- The published contract is read-only views; their storage is partitioned by
-- time and indexed for the reads below. This file gives the stub the same
-- shape, so a plan read on the stub is the plan the service gets (the EXPLAIN
-- test in internal/perf asserts that no read scans a time-partitioned
-- relation sequentially). sqlc does not read it: nothing here changes a
-- column.

-- ------------------------------------------------------------ time partitions

-- Every observation relation gets a default partition, so any instant can be
-- written; a database holding a long series adds day partitions beside it
-- (the benchmark does, before it loads the fixtures).
create table contract.traffic_event_observation_default
    partition of contract.traffic_event_observation default;
create table contract.fuel_price_observation_default
    partition of contract.fuel_price_observation default;
create table contract.charge_point_state_observation_default
    partition of contract.charge_point_state_observation default;
create table contract.occupancy_observation_default
    partition of contract.occupancy_observation default;
create table contract.call_observation_default
    partition of contract.call_observation default;

-- The last observation of one entity: newest first, per partition.
create index on contract.fuel_price_observation (station_id, product_code, observed_at desc);
create index on contract.charge_point_state_observation (point_id, observed_at desc);
create index on contract.occupancy_observation (facility_id, observed_at desc);
create index on contract.call_observation (journey_id, call_order, observed_at desc);

-- ------------------------------------------------------------------ geography

-- The radius searches compare geography distances.
create index on contract.charging_site using gist ((location::geography));
create index on contract.parking_facility using gist ((location::geography));
create index on contract.fuel_station using gist ((location::geography));
create index on contract.place using gist ((location::geography));
create index on contract.stop_place using gist ((location::geography));

-- --------------------------------------------------------------------- lookups

create index on contract.charge_point (site_id);
create index on contract.source_note (source_ref, lang);
