-- Read-contract stub, version 0.1.
--
-- This file is a STUB of the logical read model described on the wiki page
-- "Data Model". It names the logical entities and fields of that page, not any
-- physical schema. The service reads only the relations of the schema
-- "contract"; in a real deployment they are read-only views published behind
-- this contract. Here they are plain tables, so that tests and local
-- development can load synthetic fixtures into them.
--
-- sqlc compiles every query of db/queries against this file.
--
-- The observation relations are time series, partitioned by observed_at as
-- the published contract's are; 0003_physical.sql gives them a default
-- partition and adds the indexes the service's reads rely on.

create extension if not exists postgis;

create schema if not exists contract;

-- The version of the read contract this database publishes.
create table contract.contract_info (
    contract_version text not null check (contract_version ~ '^[0-9]+\.[0-9]+$'),
    published_at     timestamptz not null
);

-- ---------------------------------------------------------------- provenance

create table contract.publisher (
    publisher_id text primary key,
    display_name text not null,
    website      text
);

create table contract.licence (
    licence_code         text primary key,
    licence_name         text not null,
    licence_url          text not null,
    licence_category     text not null,
    share_alike          boolean not null,
    attribution_required boolean not null
);

create table contract.source (
    source_ref            text primary key,
    domain                text not null,
    publisher_id          text not null references contract.publisher,
    licence_code          text not null references contract.licence,
    attribution_text      text not null,
    changes_statement     text,
    terms_url             text,
    share_alike_offer_url text,
    acceptance_required   boolean not null,
    use_classes           text[] not null,
    freshness_target_s    integer not null check (freshness_target_s > 0),
    stale_after_s         integer not null check (stale_after_s > 0),
    review_signed_on      date,
    register_version      date not null
);

create table contract.source_note (
    source_ref text not null references contract.source,
    note_kind  text not null,
    lang       text not null,
    body       text not null
);

create table contract.source_freshness (
    source_ref        text primary key references contract.source,
    data_as_of        timestamptz,
    freshness_version text not null
);

-- -------------------------------------------------------------------- places

create table contract.admin_region (
    region_code  text primary key,
    region_level text not null,
    name         text not null,
    parent_code  text references contract.admin_region,
    boundary     geometry(MultiPolygon, 4326)
);

create table contract.place (
    place_id      text primary key,
    place_kind    text not null,
    name          text not null,
    location      geometry(Point, 4326) not null,
    region_code   text references contract.admin_region,
    official_key  text,
    prominence    integer not null,
    source_ref    text not null references contract.source,
    origin_key    text not null,
    published_at  timestamptz,
    recorded_at   timestamptz not null,
    changed_at    timestamptz not null,
    withdrawn_at  timestamptz
);

create table contract.place_alias (
    place_id    text not null references contract.place,
    label       text not null,
    lang        text,
    name_kind   text not null,
    search_fold text not null
);

-- --------------------------------------------------------------------- roads

create table contract.road (
    road_ref   text primary key,
    road_class text not null,
    name       text,
    length_km  numeric(6,1),
    geometry   geometry(MultiLineString, 4326),
    states     text[] not null
);

create table contract.junction (
    junction_id   text primary key,
    road_ref      text not null references contract.road,
    number        text,
    name          text not null,
    junction_kind text not null,
    chainage_km   numeric(6,1),
    location      geometry(Point, 4326) not null
);

create table contract.traffic_event (
    event_id          text primary key,
    event_kind        text not null,
    severity          text not null,
    road_ref          text references contract.road,
    road_class        text not null,
    direction         text not null,
    direction_label   text,
    location          geometry(Point, 4326),
    valid_from        timestamptz not null,
    valid_to          timestamptz,
    lanes_closed      integer,
    lanes_total       integer,
    delay_min         integer,
    affected_length_m integer,
    headline          text not null,
    description       text,
    text_lang         text not null,
    source_ref        text not null references contract.source,
    origin_key        text not null,
    published_at      timestamptz,
    recorded_at       timestamptz not null,
    changed_at        timestamptz not null,
    withdrawn_at      timestamptz
);

create table contract.traffic_event_observation (
    event_id    text not null references contract.traffic_event,
    observed_at timestamptz not null,
    version_no  integer not null,
    -- observed_at is part of the key because the table is partitioned by it.
    primary key (event_id, version_no, observed_at)
) partition by range (observed_at);

-- -------------------------------------------------------------------- energy

create table contract.fuel_station (
    station_id    text primary key,
    name          text not null,
    brand         text,
    region_code   text references contract.admin_region,
    location      geometry(Point, 4326) not null,
    open_state    text not null,
    open_state_at timestamptz,
    source_ref    text not null references contract.source,
    origin_key    text not null,
    recorded_at   timestamptz not null,
    changed_at    timestamptz not null,
    withdrawn_at  timestamptz
);

create table contract.fuel_product (
    product_code text primary key,
    label        text not null,
    price_unit   text not null
);

-- Fuel prices are consumer information only and may not be redistributed.
-- No endpoint may expose this table until the licence decision for it is
-- recorded in the wiki's licence matrix; the stub keeps the shape so the
-- contract stays complete.
create table contract.fuel_price_observation (
    station_id   text not null references contract.fuel_station,
    product_code text not null references contract.fuel_product,
    observed_at  timestamptz not null,
    price        numeric(6,3) not null,
    currency     char(3) not null,
    source_ref   text not null references contract.source
) partition by range (observed_at);

create table contract.charging_site (
    site_id       text primary key,
    name          text,
    operator_name text,
    access        text not null,
    location      geometry(Point, 4326) not null,
    source_ref    text not null references contract.source,
    origin_key    text not null,
    recorded_at   timestamptz not null,
    changed_at    timestamptz not null,
    withdrawn_at  timestamptz
);

create table contract.charge_point (
    point_id     text primary key,
    site_id      text not null references contract.charging_site,
    evse_code    text,
    current_kind text,
    power_max_kw numeric(7,2)
);

create table contract.charge_point_state_observation (
    point_id     text not null references contract.charge_point,
    observed_at  timestamptz not null,
    availability text not null,
    source_ref   text not null references contract.source
) partition by range (observed_at);

-- ------------------------------------------------------------------- parking

create table contract.parking_facility (
    facility_id         text primary key,
    name                text,
    parking_kind        text not null,
    operator_name       text,
    road_ref            text references contract.road,
    direction           text,
    location            geometry(Point, 4326) not null,
    acceptance_required boolean not null,
    source_ref          text not null references contract.source,
    origin_key          text not null,
    recorded_at         timestamptz not null,
    changed_at          timestamptz not null,
    withdrawn_at        timestamptz
);

create table contract.occupancy_observation (
    facility_id      text not null references contract.parking_facility,
    observed_at      timestamptz not null,
    free_spaces      integer,
    total_spaces     integer,
    occupancy_status text not null,
    source_ref       text not null references contract.source
) partition by range (observed_at);

-- ------------------------------------------------------------------- transit

create table contract.stop_place (
    stop_place_id      text primary key,
    name               text not null,
    location           geometry(Point, 4326) not null,
    national_stop_code text,
    region_code        text references contract.admin_region,
    modes              text[] not null,
    search_fold        text not null,
    source_ref         text not null references contract.source,
    origin_key         text not null,
    recorded_at        timestamptz not null,
    changed_at         timestamptz not null,
    withdrawn_at       timestamptz
);

create table contract.stop_point (
    stop_point_id  text primary key,
    stop_place_id  text not null references contract.stop_place,
    platform_label text
);

create table contract.transit_line (
    line_id        text primary key,
    line_name      text not null,
    transport_mode text not null
);

create table contract.journey (
    journey_id        text primary key,
    line_id           text not null references contract.transit_line,
    service_date      date not null,
    destination_label text,
    extra             boolean not null
);

create table contract.scheduled_call (
    journey_id           text not null references contract.journey,
    call_order           integer not null,
    stop_point_id        text not null references contract.stop_point,
    planned_arrival_at   timestamptz,
    planned_departure_at timestamptz,
    planned_platform     text,
    primary key (journey_id, call_order)
);

create table contract.call_observation (
    journey_id            text not null,
    call_order            integer not null,
    observed_at           timestamptz not null,
    expected_arrival_at   timestamptz,
    expected_departure_at timestamptz,
    call_status           text not null,
    source_ref            text not null references contract.source,
    foreign key (journey_id, call_order) references contract.scheduled_call
) partition by range (observed_at);

-- ------------------------------------------------------------- accessibility

create table contract.station_facility (
    facility_id        text primary key,
    stop_place_id      text not null references contract.stop_place,
    facility_kind      text not null,
    position_text      text,
    facility_status    text not null,
    status_observed_at timestamptz not null,
    source_ref         text not null references contract.source
);

-- ------------------------------------------------------------------- weather

create table contract.weather_warning (
    warning_id     text primary key,
    hazard         text not null,
    warning_level  integer not null,
    level_label    text not null,
    message_status text not null,
    issuer_name    text not null,
    headline       text not null,
    text_lang      text not null,
    valid_from     timestamptz not null,
    valid_to       timestamptz,
    published_at   timestamptz,
    region_code    text references contract.admin_region,
    source_ref     text not null references contract.source,
    recorded_at    timestamptz not null,
    withdrawn_at   timestamptz
);

-- --------------------------------------------------------------------- rules

create table contract.rule_topic (
    topic_code  text primary key,
    title       text not null,
    reviewed_on date not null
);

create table contract.rule_statement (
    statement_id text primary key,
    topic_code   text not null references contract.rule_topic,
    position     integer not null,
    body         text not null,
    legal_basis  text
);
