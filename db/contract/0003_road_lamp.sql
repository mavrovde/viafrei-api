-- Read-contract stub, version 0.2: what the road-conditions lamp reads.
--
-- The lamp of /v1/motorways/{road}/conditions and /v1/road-conditions is the
-- platform's motorway Ampel (toolAmpel of its MCP server, the rules of its
-- motorway-ampel.ts). Version 0.1 carried every road situation in one shape;
-- the Ampel reads three kinds of record differently and reads a few fields
-- 0.1 does not hold. This file adds them. Every field below is held by the
-- platform's database (the Autobahn GmbH events, the police and Land
-- message rows, the LBV.SH bridge states, the feed checks); the published
-- views fill them, this stub only names them.

-- ---------------------------------------------------------- traffic_event

alter table contract.traffic_event
    -- How the lamp reads the record, by its publisher:
    --   operator_event  the motorway operator's own event record (the
    --                   Autobahn GmbH), read as the Ampel reads an Autobahn
    --                   GmbH row (autobahnEventSignal);
    --   traffic_message a police, Land or city traffic message, read as the
    --                   Ampel reads one (messageSignal);
    --   bridge_state    a bridge's state record (LBV.SH), read by its state
    --                   text (bridgeStateOf, bridgeSignal). Its headline is
    --                   the bridge's name, its description the state text.
    add column record_class text not null default 'traffic_message'
        check (record_class in ('operator_event', 'traffic_message', 'bridge_state')),
    -- An operator event's subtitle as published ("AS X (aus Richtung Y)");
    -- null for any other record.
    add column subtitle text,
    -- A traffic message's place words as the Ampel reads them (the road
    -- entry's prefix already removed); null for any other record.
    add column place_text text,
    -- An operator event's isBlocked: true when the publisher marks the
    -- record blocked; null when it says nothing.
    add column is_blocked boolean,
    -- An operator event's abnormal traffic type as published; a record that
    -- carries one is a jam. Null when it carries none.
    add column traffic_type text,
    -- The published delay in seconds; delay_min is the same delay in whole
    -- minutes. Null when no delay is published.
    add column delay_s integer;

comment on column contract.traffic_event.description is
    'The publisher''s description: its lines in their order, joined with one line feed (U+000A) and nothing else. The lamp reads an operator event''s location line ("Auffahrt auf die A9: ...", "A45: Hagen -> Gießen, ...") line by line. For a bridge_state record, the bridge''s state text.';

-- ---------------------------------------------------------------- junction

-- The code of the state (as contract.road.states writes it) the junction
-- lies in: the lamp's police sight is judged per state of the junctions in
-- its scope. Null when it is not known.
alter table contract.junction add column state_code text;

-- ------------------------------------------------------------ feed checks

-- The newest check of each feed the lamp's sight reads (the platform's
-- feed_checks): the DWD warnings feed and the police and Land message feeds.
-- A feed is fresh while its newest check is inside three of its cadences
-- (600 s when it states none), or while its refresh is paused.
create table contract.feed_check (
    feed_ref   text primary key,
    source_ref text not null references contract.source,
    -- null: the feed was never checked.
    checked_at timestamptz,
    cadence_s  integer check (cadence_s > 0),
    paused     boolean not null default false
);

-- Which feeds tell the lamp a state's police and Land messages are seen
-- (the platform's police sight per Land): a state's own message feeds
-- (feed_role land) and, where the platform measured that the relay carries
-- the state's messages, the relay (feed_role relay). A land feed whose rows
-- are answered only while current (current_only) caps the state's sight. A
-- state with no row here has no police sight.
create table contract.message_sight_feed (
    state_code   text not null,
    feed_ref     text not null references contract.feed_check,
    feed_role    text not null check (feed_role in ('land', 'relay')),
    current_only boolean not null default false,
    primary key (state_code, feed_ref)
);
