-- Read-contract stub, version 0.2: what the road-conditions lamp reads.
--
-- The lamp of /v1/motorways/{road}/conditions and /v1/road-conditions is the
-- platform's motorway Ampel. Version 0.1 carried every road situation in one
-- shape; the Ampel reads three kinds of record differently, reads a few
-- fields 0.1 does not hold, and judges its sight by each source's newest
-- confirmation and by which sources cover a state's traffic messages. This
-- file adds them. The published views fill them; this stub only names them.

-- ---------------------------------------------------------- traffic_event

alter table contract.traffic_event
    -- How the lamp reads the record:
    --   operator_event  the motorway operator's own event record, read by
    --                   its kind, its scope, its traffic type and its words;
    --   traffic_message any other traffic message, read by its kind and its
    --                   words;
    --   bridge_state    a bridge's state record, read by its state text
    --                   alone. Its headline is the bridge's name, its
    --                   description the state text.
    add column record_class text not null default 'traffic_message'
        check (record_class in ('operator_event', 'traffic_message', 'bridge_state')),
    -- An operator event's subtitle as published ("AS X (aus Richtung Y)");
    -- null for any other record.
    add column subtitle text,
    -- A traffic message's place words as published, the road's name already
    -- removed from their start; null for any other record.
    add column place_text text,
    -- True when the publisher marks an operator event blocked; null when it
    -- says nothing.
    add column is_blocked boolean,
    -- An operator event's abnormal traffic type as published; a record that
    -- carries one is a jam. Null when it carries none.
    add column traffic_type text,
    -- The published delay in seconds; delay_min is the same delay in whole
    -- minutes. Null when no delay is published.
    add column delay_s integer check (delay_s >= 0);

comment on column contract.traffic_event.description is
    'The publisher''s description: its lines in their order, joined with one newline (U+000A) and nothing else. The lamp reads an operator event''s location line ("Auffahrt auf die A9: ...", "A45: Hagen -> Gießen, ...") line by line. For a bridge_state record, the bridge''s state text.';

-- ---------------------------------------------------------------- junction

-- The code of the state (as contract.road.states writes it) the junction
-- lies in: the lamp judges the traffic messages' sight per state of the
-- junctions in its scope. Null when it is not known.
alter table contract.junction add column state_code text;

-- ------------------------------------------------------- source_freshness

alter table contract.source_freshness
    -- The newest instant the source's data was confirmed current, whether
    -- or not it changed. Null: never confirmed.
    add column confirmed_at timestamptz,
    -- The interval, in seconds, at which the source is confirmed; null when
    -- it states none (the lamp then judges it against 600 s).
    add column confirm_interval_s integer check (confirm_interval_s > 0),
    -- True while the source's updates are paused and the data held stands:
    -- a confirmed source whose updates are paused stays current.
    add column updates_paused boolean not null default false;

-- ------------------------------------------------------- message_coverage

-- Which sources tell the lamp a state's traffic messages are seen: the
-- state's own message sources (coverage_role own) and the sources that
-- carry the state's messages as well (coverage_role carried). An own
-- source whose messages are answered only while it is current
-- (current_only) caps the state's sight. A state with no row here is not
-- seen.
create table contract.message_coverage (
    state_code    text not null,
    source_ref    text not null references contract.source,
    coverage_role text not null check (coverage_role in ('own', 'carried')),
    current_only  boolean not null default false,
    primary key (state_code, source_ref),
    check (not current_only or coverage_role = 'own')
);
