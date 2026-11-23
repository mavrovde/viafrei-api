package store

// The platform's relations as the read contract's views defined them (database
// 0.4.5), each the body of the view it replaces: a statement reads one as a
// subquery where no tuned form of its own answers the same rows faster.
// Generated from the views' definitions; edit a relation only with its parity test.

// relDeparture is the platform's departure (kind v).
const relDeparture = `
 SELECT (s.gtfs_feed_key::text || ':'::text) || COALESCE(s.parent_id, s.id) AS stop_place_id,
    (s.gtfs_feed_key::text || ':'::text) || s.id AS stop_point_id,
    (((t.gtfs_feed_key::text || ':'::text) || t.id) || ':'::text) || to_char(d.service_date::timestamp with time zone, 'YYYYMMDD'::text) AS journey_id,
    st.stop_sequence AS call_order,
    (r.gtfs_feed_key::text || ':'::text) || r.id AS line_id,
    COALESCE(NULLIF(btrim(r.short_name), ''::text), NULLIF(btrim(r.long_name), ''::text), r.id) AS line_name,
    contract_transport_mode(r.route_type) AS transport_mode,
    r.route_type,
    NULLIF(btrim(t.trip_short_name), ''::text) AS train_number,
    sc.category AS train_category,
    COALESCE(NULLIF(btrim(t.headsign), ''::text), sc.destination) AS destination_label,
    pl.arr AS planned_arrival_at,
    pl.dep AS planned_departure_at,
    NULLIF(btrim(s.platform_code), ''::text) AS planned_platform,
    COALESCE(sc.platform, NULLIF(btrim(s.platform_code), ''::text)) AS platform,
    COALESCE(sc.platform <> btrim(s.platform_code), false) AS platform_changed,
    st.departure IS NOT NULL AND st.pickup_type <> 1 AS boarding,
    NOT (EXISTS ( SELECT 1
           FROM stop_times nx
          WHERE nx.gtfs_feed_key = st.gtfs_feed_key AND nx.trip_id = st.trip_id AND nx.stop_sequence > st.stop_sequence AND nx.deleted_at IS NULL)) AS terminates,
    fig.feed IS NOT NULL AS realtime,
    fig.feed AS realtime_source,
    COALESCE(fig.cancelled, false) AS cancelled,
    fig.delay_s,
    round(fig.delay_s::numeric / 60.0)::integer AS delay_min,
    pl.arr + make_interval(secs => fig.delay_s::double precision) AS expected_arrival_at,
    pl.dep + make_interval(secs => fig.delay_s::double precision) AS expected_departure_at,
    fig.refused OR fig.feed IS NULL AND sc.call_at IS NOT NULL AS no_forecast,
    fig.observed_at,
    src.feed AS source_ref,
    array_remove(ARRAY[src.feed,
        CASE
            WHEN tu.carried THEN 'delfi_rt_tu'::text
            ELSE NULL::text
        END,
        CASE
            WHEN sc.call_at IS NOT NULL THEN sc.source_feed
            ELSE NULL::text
        END], NULL::text) AS source_refs,
    COALESCE(k.display_class, 'open'::text) AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience
   FROM stops s
     JOIN stop_times st ON st.gtfs_feed_key = s.gtfs_feed_key AND st.stop_id = s.id AND st.deleted_at IS NULL
     JOIN trips t ON t.gtfs_feed_key = st.gtfs_feed_key AND t.id = st.trip_id AND t.deleted_at IS NULL
     JOIN routes r ON r.gtfs_feed_key = t.gtfs_feed_key AND r.id = t.route_id AND r.deleted_at IS NULL
     JOIN gtfs_service_dates d ON d.gtfs_feed_key = t.gtfs_feed_key AND d.service_id = t.service_id
     CROSS JOIN LATERAL ( SELECT ( SELECT f.source_feed
                   FROM feeds f
                  WHERE f.gtfs_feed_key = s.gtfs_feed_key) AS feed
         OFFSET 0) src
     CROSS JOIN LATERAL ( SELECT ((d.service_date + '12:00:00'::time without time zone) AT TIME ZONE 'Europe/Berlin'::text) - '12:00:00'::interval + st.arrival AS arr,
            ((d.service_date + '12:00:00'::time without time zone) AT TIME ZONE 'Europe/Berlin'::text) - '12:00:00'::interval + st.departure AS dep) pl
     LEFT JOIN LATERAL ( WITH run AS (
                 SELECT u.stop_sequence,
                    u.delay_s,
                    upper(u.schedule_relationship) AS rel,
                    u.event_at,
                    u.observed_at
                   FROM transit_delays_live u
                  WHERE s.gtfs_feed_key = 1 AND u.trip_id = t.id AND abs(EXTRACT(epoch FROM u.observed_at - COALESCE(pl.dep, pl.arr))) <= 43200::numeric AND (u.stop_sequence IS NOT NULL OR u.start_date IS NULL OR u.start_date = to_char(d.service_date::timestamp with time zone, 'YYYYMMDD'::text)) AND (EXISTS ( SELECT 1
                           FROM feed_checks c
                          WHERE c.feed_key = 'tripupdates'::text AND c.checked_at IS NOT NULL AND (c.paused OR c.checked_at >= (now() - 3::double precision * make_interval(secs => COALESCE(NULLIF(c.cadence_s, 0), 600)::double precision)))))
                ), trip_row AS (
                 SELECT x.rel,
                    x.observed_at
                   FROM run x
                  WHERE x.stop_sequence IS NULL
                  ORDER BY x.observed_at DESC
                 LIMIT 1
                ), stop_row AS (
                 SELECT x.stop_sequence,
                    x.delay_s,
                    x.rel,
                    x.event_at,
                    x.observed_at
                   FROM run x
                  WHERE x.stop_sequence <= st.stop_sequence AND NOT (x.rel = 'SKIPPED'::text AND x.stop_sequence <> st.stop_sequence)
                  ORDER BY x.stop_sequence DESC
                 LIMIT 1
                )
         SELECT (EXISTS ( SELECT 1
                   FROM run)) AS carried,
                CASE
                    WHEN (tr.rel = ANY (ARRAY['CANCELED'::text, 'CANCELLED'::text, 'DELETED'::text])) AND tr.observed_at >= COALESCE(( SELECT max(x.observed_at) AS max
                       FROM run x
                      WHERE x.stop_sequence IS NOT NULL), '-infinity'::timestamp with time zone) THEN true
                    WHEN sr.rel = 'SKIPPED'::text THEN true
                    ELSE NULL::boolean
                END AS cancelled,
                CASE
                    WHEN (tr.rel = ANY (ARRAY['CANCELED'::text, 'CANCELLED'::text, 'DELETED'::text])) AND tr.observed_at >= COALESCE(( SELECT max(x.observed_at) AS max
                       FROM run x
                      WHERE x.stop_sequence IS NOT NULL), '-infinity'::timestamp with time zone) THEN NULL::integer
                    WHEN sr.rel = ANY (ARRAY['NO_DATA'::text, 'SKIPPED'::text]) THEN NULL::integer
                    WHEN sr.delay_s IS NOT NULL THEN sr.delay_s
                    WHEN sr.stop_sequence = st.stop_sequence AND sr.event_at IS NOT NULL AND abs(EXTRACT(epoch FROM sr.event_at - COALESCE(pl.dep, pl.arr))) <= 43200::numeric THEN round(EXTRACT(epoch FROM sr.event_at - COALESCE(pl.dep, pl.arr)))::integer
                    ELSE NULL::integer
                END AS delay_s,
            COALESCE(
                CASE
                    WHEN tr.rel = ANY (ARRAY['CANCELED'::text, 'CANCELLED'::text, 'DELETED'::text]) THEN tr.observed_at
                    ELSE NULL::timestamp with time zone
                END, sr.observed_at) AS observed_at
           FROM ( SELECT 1 AS "?column?") one
             LEFT JOIN trip_row tr ON true
             LEFT JOIN stop_row sr ON true) tu ON true
     LEFT JOIN LATERAL ( SELECT c.platform,
            c.category,
            c.destination,
            c.source_feed,
            c.aimed_dep AS call_at,
            c.observed_at,
                CASE
                    WHEN c.cancelled OR c.journey_cancelled THEN NULL::integer
                    ELSE COALESCE(c.delay_s, round(EXTRACT(epoch FROM c.expected_dep - c.aimed_dep))::integer)
                END AS delay_s,
            c.cancelled OR c.journey_cancelled AS cancelled
           FROM ( SELECT s.id AS ref,
                    false AS bridged
                UNION
                 SELECT COALESCE(s.parent_id, s.id) AS "coalesce",
                    false
                UNION
                 SELECT e.eva,
                    false
                   FROM db_stations x,
                    LATERAL unnest(x.eva_nos) e(eva)
                  WHERE x.ifopt = COALESCE(s.parent_id, "substring"(s.id, '^[a-z]{2}:[0-9]{5}:[0-9]+'::text), s.id) AND x.closed_at IS NULL
                UNION
                 SELECT p.eva,
                    true
                   FROM delfi_siri_et_stop_pairs p
                  WHERE p.dhid = s.id OR p.station_dhid = COALESCE(s.parent_id, "substring"(s.id, '^[a-z]{2}:[0-9]{5}:[0-9]+'::text), s.id)) refs
             JOIN delfi_siri_et_calls_latest c ON c.stop_ref = refs.ref AND COALESCE(c.expected_dep, c.aimed_dep) >= (pl.dep - '01:00:00'::interval) AND COALESCE(c.expected_dep, c.aimed_dep) <= (pl.dep + '06:01:00'::interval) AND c.aimed_dep > (pl.dep - '00:01:00'::interval) AND c.aimed_dep < (pl.dep + '00:01:00'::interval)
          WHERE s.gtfs_feed_key = 1 AND pl.dep IS NOT NULL AND NOT c.recorded AND c.observed_at > (now() - '08:00:00'::interval) AND c.observed_at <= now() AND upper(regexp_replace(c.line, '\s+'::text, ''::text, 'g'::text)) = upper(regexp_replace(COALESCE(NULLIF(btrim(r.short_name), ''::text), NULLIF(btrim(r.long_name), ''::text), ''::text), '\s+'::text, ''::text, 'g'::text)) AND (NOT refs.bridged OR c.destination = btrim(t.headsign)) AND (EXISTS ( SELECT 1
                   FROM feed_checks k_1
                  WHERE k_1.feed_key = c.source_feed AND k_1.checked_at IS NOT NULL AND (k_1.paused OR k_1.checked_at >= (now() - 3::double precision * make_interval(secs => COALESCE(NULLIF(k_1.cadence_s, 0), 600)::double precision)))))
          ORDER BY (c.destination = btrim(t.headsign)) DESC NULLS LAST, refs.bridged, c.observed_at DESC
         LIMIT 1) sc ON true
     CROSS JOIN LATERAL ( SELECT
                CASE
                    WHEN sc.call_at IS NOT NULL AND (sc.cancelled OR abs(COALESCE(sc.delay_s, 0)) <= 21600) AND (sc.cancelled OR sc.delay_s IS NOT NULL) THEN 'siri'::text
                    WHEN tu.carried AND (tu.cancelled OR tu.delay_s IS NOT NULL AND abs(tu.delay_s) <= 21600) THEN 'tu'::text
                    ELSE NULL::text
                END AS pick,
            sc.delay_s IS NOT NULL AND abs(sc.delay_s) > 21600 OR tu.delay_s IS NOT NULL AND abs(tu.delay_s) > 21600 AS over) pk
     CROSS JOIN LATERAL ( SELECT
                CASE pk.pick
                    WHEN 'siri'::text THEN sc.source_feed
                    WHEN 'tu'::text THEN 'delfi_rt_tu'::text
                    ELSE NULL::text
                END AS feed,
                CASE pk.pick
                    WHEN 'siri'::text THEN sc.cancelled
                    WHEN 'tu'::text THEN COALESCE(tu.cancelled, false)
                    ELSE NULL::boolean
                END AS cancelled,
                CASE pk.pick
                    WHEN 'siri'::text THEN sc.delay_s
                    WHEN 'tu'::text THEN tu.delay_s
                    ELSE NULL::integer
                END AS delay_s,
                CASE pk.pick
                    WHEN 'siri'::text THEN sc.observed_at
                    WHEN 'tu'::text THEN tu.observed_at
                    ELSE NULL::timestamp with time zone
                END AS observed_at,
            pk.pick IS NULL AND pk.over AS refused) fig
     LEFT JOIN LATERAL ( SELECT k_1.display_class,
            k_1.audience
           FROM contract_source_rules k_1
          WHERE fig.feed IS NOT NULL AND fig.feed ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE s.deleted_at IS NULL`

// relFuelPriceObservation is the platform's fuel price observation (kind v).
const relFuelPriceObservation = `
 SELECT p.station_id,
    p.product AS product_code,
    p.observed_at,
    p.price,
    'EUR'::character(3) AS currency,
    p.source AS source_ref,
    COALESCE(k.display_class, 'open'::text) AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience
   FROM fuel_prices p
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE COALESCE(p.source, ''::text) ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE NOT COALESCE(k.excluded, false)`

// relFuelStation is the platform's fuel station (kind v).
const relFuelStation = `
 SELECT f.id AS station_id,
    f.name,
    f.brand,
    f.land AS region_code,
    f.geom::geometry(Point,4326) AS location,
    COALESCE(f.state, 'unknown'::text) AS open_state,
    NULL::timestamp with time zone AS open_state_at,
    COALESCE(f.source_feed, f.source) AS source_ref,
    f.id AS origin_key,
    f.first_seen_at AS recorded_at,
    COALESCE(f.updated_at, f.last_seen_at) AS changed_at,
    NULL::timestamp with time zone AS withdrawn_at,
    COALESCE(k.display_class, 'open'::text) AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience,
    f.street,
    f.house_no AS house_number,
    f.postcode,
    f.city,
    f.opening_times,
    f.whole_day
   FROM fuel_stations f
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE COALESCE(f.source_feed, f.source, ''::text) ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE f.deleted_at IS NULL AND f.geom IS NOT NULL AND NOT COALESCE(k.excluded, false)`

// relParkingFacility is the platform's parking facility (kind v).
const relParkingFacility = `
 WITH f AS (
         SELECT 'p:'::text || parking_facilities.id AS facility_id,
            parking_facilities.name,
            parking_facilities.kind,
            parking_facilities.operator,
            parking_facilities.road,
            parking_facilities.geom,
            false AS sid,
            parking_facilities.source_feed,
            parking_facilities.external_id,
            parking_facilities.first_seen_at,
            parking_facilities.updated_at,
            parking_facilities.last_seen_at,
            parking_facilities.closed_at,
            parking_facilities.confidentiality,
            parking_facilities.facilities,
            parking_facilities.facilities_absent
           FROM parking_facilities
          WHERE parking_facilities.deleted_at IS NULL
        UNION ALL
         SELECT 'bysa:'::text || parking_facilities_bysa.id,
            parking_facilities_bysa.name,
            parking_facilities_bysa.kind,
            parking_facilities_bysa.operator,
            parking_facilities_bysa.road,
            parking_facilities_bysa.geom,
            false,
            parking_facilities_bysa.source_feed,
            parking_facilities_bysa.external_id,
            parking_facilities_bysa.first_seen_at,
            parking_facilities_bysa.updated_at,
            parking_facilities_bysa.last_seen_at,
            parking_facilities_bysa.closed_at,
            parking_facilities_bysa.confidentiality,
            '{}'::text[] AS text,
            '{}'::text[] AS text
           FROM parking_facilities_bysa
        UNION ALL
         SELECT 'odbl:'::text || parking_facilities_odbl.id,
            parking_facilities_odbl.name,
            parking_facilities_odbl.kind,
            parking_facilities_odbl.operator,
            parking_facilities_odbl.road,
            parking_facilities_odbl.geom,
            false,
            parking_facilities_odbl.source_feed,
            parking_facilities_odbl.external_id,
            parking_facilities_odbl.first_seen_at,
            parking_facilities_odbl.updated_at,
            parking_facilities_odbl.last_seen_at,
            parking_facilities_odbl.closed_at,
            parking_facilities_odbl.confidentiality,
            '{}'::text[] AS text,
            '{}'::text[] AS text
           FROM parking_facilities_odbl
        UNION ALL
         SELECT 'sid:'::text || parking_facilities_sid.id,
            parking_facilities_sid.name,
            parking_facilities_sid.kind,
            NULL::text,
            NULL::text,
            parking_facilities_sid.geom,
            true,
            parking_facilities_sid.source_feed,
            parking_facilities_sid.external_id,
            parking_facilities_sid.first_seen_at,
            parking_facilities_sid.updated_at,
            parking_facilities_sid.last_seen_at,
            parking_facilities_sid.closed_at,
            parking_facilities_sid.confidentiality,
            parking_facilities_sid.facilities,
            parking_facilities_sid.facilities_absent
           FROM parking_facilities_sid
        )
 SELECT f.facility_id,
    f.name,
        CASE f.kind
            WHEN 'rest_area'::text THEN 'rest_area'::text
            WHEN 'park_and_ride'::text THEN 'park_and_ride'::text
            WHEN 'truck'::text THEN 'truck_parking'::text
            ELSE 'other'::text
        END AS parking_kind,
    f.operator AS operator_name,
    f.road AS road_ref,
    NULL::text AS direction,
    st_pointonsurface(f.geom::geometry)::geometry(Point,4326) AS location,
    f.sid AS acceptance_required,
    f.source_feed AS source_ref,
    f.external_id AS origin_key,
    f.first_seen_at AS recorded_at,
    COALESCE(f.updated_at, f.last_seen_at) AS changed_at,
    f.closed_at AS withdrawn_at,
    ARRAY[f.source_feed] AS source_refs,
        CASE
            WHEN COALESCE(f.confidentiality, 'noRestriction'::text) <> 'noRestriction'::text THEN 'withhold'::text
            ELSE COALESCE(k.display_class, 'open'::text)
        END AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience,
    f.confidentiality,
    k.terms_gate,
    k.hold_reason,
    f.kind AS stored_kind,
    f.facilities AS amenities,
    f.facilities_absent AS amenities_absent
   FROM f
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE f.source_feed ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE f.geom IS NOT NULL AND NOT COALESCE(k.excluded, false)`

// relServiceAlert is the platform's service alert (kind v).
const relServiceAlert = `
 SELECT (a.source_feed || ':'::text) || a.alert_id AS alert_id,
    a.header,
    a.description,
    a.cause,
    a.effect,
    a.url,
    a.active_from,
    a.active_to,
    a.active_periods,
    a.route_ids,
    a.line_names,
    a.stop_ids,
    a.kreise AS district_codes,
    a.lands AS state_codes,
    a.placed_by,
    a.dedupe_key,
    a.source_feed AS source_ref,
    a.first_seen_at AS recorded_at,
    COALESCE(a.provider_updated_at, a.last_seen_at) AS changed_at,
    a.last_seen_at AS seen_at,
    a.message_ref,
    a.source_id = 'delfi_rt_sa'::text AS primary_source,
    c.checked_at,
    c.cadence_s,
    COALESCE(c.paused, false) AS paused,
        CASE
            WHEN COALESCE(a.confidentiality, 'noRestriction'::text) <> 'noRestriction'::text THEN 'withhold'::text
            ELSE COALESCE(k.display_class, 'open'::text)
        END AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience,
    a.confidentiality
   FROM delfi_service_alerts a
     LEFT JOIN feed_checks c ON c.feed_key = a.source_feed
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE a.source_feed ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE a.closed_at IS NULL AND NOT COALESCE(k.excluded, false)`

// relSource is the platform's source (kind v).
const relSource = `
 WITH s AS (
         SELECT s_1.source_ref,
            d.domain,
            s_1.source_ref AS publisher_id,
            s_1.licence_code,
            s_1.attribution_text,
            g.changes_statement,
            s_1.terms_url,
                CASE
                    WHEN g.share_alike OR s_1.licence_code ~* '(BY-SA|ODbL|Open Database License)'::text THEN 'https://viafrei.de/impressum'::text
                    ELSE NULL::text
                END AS share_alike_offer_url,
            s_1.acceptance_required,
                CASE
                    WHEN COALESCE(k.display_class, 'open'::text) = 'open'::text THEN COALESCE(g.use_classes, '{}'::text[])
                    ELSE ARRAY( SELECT u.u
                       FROM unnest(COALESCE(g.use_classes, '{}'::text[])) u(u)
                      WHERE u.u = 'consumer'::text)
                END AS use_classes,
            COALESCE(s_1.cadence_s, f.cadence_s) AS freshness_target_s,
            3 * COALESCE(s_1.cadence_s, f.cadence_s) AS stale_after_s,
            g.verified_on AS review_signed_on,
            COALESCE(g.register_version, ( SELECT max(v.register_version) AS max
                   FROM source_register v)) AS register_version,
            k.excluded,
            k.display_class,
            k.audience,
            k.terms_gate,
            k.hold_reason,
            k.origin_restriction
           FROM contract_sources s_1
             LEFT JOIN source_register g ON g.source_id = s_1.register_id
             LEFT JOIN LATERAL ( SELECT max(x.cadence_s) AS cadence_s
                   FROM control.data_status x
                  WHERE x.key = s_1.source_ref) f ON true
             LEFT JOIN LATERAL ( SELECT d_1.domain
                   FROM contract_source_domains d_1
                  WHERE s_1.source_ref ~ d_1.pattern
                  ORDER BY d_1.priority
                 LIMIT 1) d ON true
             LEFT JOIN LATERAL ( SELECT k_1.priority,
                    k_1.pattern,
                    k_1.excluded,
                    k_1.audience,
                    k_1.display_class,
                    k_1.terms_gate,
                    k_1.hold_reason,
                    k_1.origin_restriction
                   FROM contract_source_rules k_1
                  WHERE s_1.source_ref ~ k_1.pattern
                  ORDER BY k_1.priority
                 LIMIT 1) k ON true
        )
 SELECT source_ref,
    domain,
    publisher_id,
    licence_code,
    attribution_text,
    changes_statement,
    terms_url,
    share_alike_offer_url,
    acceptance_required,
    use_classes,
    freshness_target_s,
    stale_after_s,
    review_signed_on,
    register_version,
    COALESCE(display_class, 'open'::text) AS display_class,
    COALESCE(audience, 'public'::text) AS audience,
    terms_gate,
    hold_reason,
    origin_restriction
   FROM s
  WHERE NOT COALESCE(excluded, false) AND COALESCE(licence_code, ''::text) !~* '^pending'::text`

// relStationDisruption is the platform's station disruption (kind v). The
// feeds checked recently enough are read once (fresh), not once a call: on
// prod the per-call check took most of its 7 s.
//
//nolint:misspell // MATERIALIZED is the SQL keyword
const relStationDisruption = `
 WITH fresh AS MATERIALIZED (
         SELECT k_1.feed_key
           FROM feed_checks k_1
          WHERE k_1.checked_at IS NOT NULL AND (k_1.paused OR k_1.checked_at >= (now() - 3::double precision * make_interval(secs => COALESCE(NULLIF(k_1.cadence_s, 0), 600)::double precision)))
        ), stations AS MATERIALIZED (
         SELECT DISTINCT s.ifopt
           FROM station_index_live s
          WHERE s.ifopt IS NOT NULL
        ), refs AS MATERIALIZED (
         SELECT s.ifopt AS ref,
            s.ifopt AS station_ifopt
           FROM stations s
        UNION
         SELECT p.eva,
            p.station_dhid
           FROM delfi_siri_et_stop_pairs p
             JOIN stations s ON s.ifopt = p.station_dhid
        ), calls AS (
         SELECT DISTINCT ON (r.station_ifopt, c_1.source_feed, c_1.journey_ref, c_1.aimed_dep) r.station_ifopt,
            date_trunc('minute'::text, c_1.aimed_dep) AS aimed_minute,
            c_1.source_feed,
            COALESCE(NULLIF(lower(replace(c_1.line, ' '::text, ''::text)), ''::text), c_1.journey_ref) AS line_key,
            c_1.cancelled OR c_1.journey_cancelled AS cancelled,
            COALESCE(c_1.delay_s, 0) >= 300 AS late
           FROM refs r
             CROSS JOIN LATERAL ( SELECT x.aimed_dep,
                    x.journey_ref,
                    x.line,
                    x.cancelled,
                    x.journey_cancelled,
                    x.delay_s,
                    x.source_feed,
                    x.observed_at
                   FROM delfi_siri_et_calls_latest x
                  WHERE x.stop_ref = r.ref AND COALESCE(x.expected_dep, x.aimed_dep) >= (now() - '00:30:00'::interval) AND COALESCE(x.expected_dep, x.aimed_dep) < (now() + '03:00:00'::interval) AND x.aimed_dep >= now() AND x.aimed_dep <= (now() + '01:00:00'::interval) AND x.observed_at > (now() - '08:00:00'::interval) AND x.observed_at <= now() AND NOT x.recorded AND x.source_feed IN ( SELECT fr.feed_key FROM fresh fr)
                 LIMIT 500) c_1
          ORDER BY r.station_ifopt, c_1.source_feed, c_1.journey_ref, c_1.aimed_dep, c_1.observed_at DESC
        )
 SELECT c.station_ifopt,
    now() AS window_start,
    c.aimed_minute,
    c.line_key,
    count(*)::integer AS departures,
    count(*) FILTER (WHERE c.cancelled)::integer AS cancelled,
    count(*) FILTER (WHERE c.late AND NOT c.cancelled)::integer AS late,
    c.source_feed AS source_ref,
    COALESCE(k.display_class, 'open'::text) AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience
   FROM calls c
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE c.source_feed ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE NOT COALESCE(k.excluded, false)
  GROUP BY c.station_ifopt, c.aimed_minute, c.line_key, c.source_feed, k.display_class, k.audience`

// relStationFacility is the platform's station facility (kind v).
const relStationFacility = `
 SELECT 'equipment:'::text || e.equipment_id AS facility_id,
    contract_stop_place_of(e.stop_place_id) AS stop_place_id,
    e.kind AS facility_kind,
    e.description AS position_text,
    COALESCE(st.state, 'unknown'::text) AS facility_status,
    COALESCE(st.status_since, st.last_seen_at, e.last_seen_at) AS status_observed_at,
    COALESCE(st.source_feed, e.source_feed) AS source_ref,
    'open'::text AS display_class,
    'public'::text AS audience
   FROM station_equipment e
     LEFT JOIN station_equipment_state st ON st.equipment_id = e.equipment_id
  WHERE e.closed_at IS NULL
UNION ALL
 SELECT 'fasta:'::text || f.equipment_no AS facility_id,
    ( SELECT contract_stop_place_of(d.ifopt) AS contract_stop_place_of
           FROM db_stations d
          WHERE d.station_no = f.station_no) AS stop_place_id,
    f.kind AS facility_kind,
    f.description AS position_text,
    f.state AS facility_status,
    COALESCE(f.state_since, f.last_seen_at) AS status_observed_at,
    f.source_feed AS source_ref,
    'open'::text AS display_class,
    'public'::text AS audience
   FROM db_facilities f`

// relStopPlace is the platform's stop place (kind v).
const relStopPlace = `
 SELECT (s.gtfs_feed_key || ':'::text) || s.id AS stop_place_id,
    s.name,
    s.geom::geometry(Point,4326) AS location,
        CASE
            WHEN s.id ~ '^[a-z]{2}:[0-9]{5}:'::text THEN (((split_part(s.id, ':'::text, 1) || ':'::text) || split_part(s.id, ':'::text, 2)) || ':'::text) || split_part(s.id, ':'::text, 3)
            ELSE NULL::text
        END AS national_stop_code,
    ( SELECT a.ags
           FROM ags_regions a
          WHERE a.ags = split_part(s.id, ':'::text, 2)) AS region_code,
    COALESCE(m.modes, '{}'::text[]) AS modes,
    contract_search_fold(s.name) AS search_fold,
    f.source_feed AS source_ref,
    s.id AS origin_key,
    f.created_at AS recorded_at,
    f.updated_at AS changed_at,
    NULL::timestamp with time zone AS withdrawn_at,
    ARRAY[f.source_feed] AS source_refs,
    'open'::text AS display_class,
    'public'::text AS audience,
    d.category AS station_category,
    COALESCE(d.city, ( SELECT g.name
           FROM gazetteer_places g
          WHERE g.source_id = 'bkg_gvisys'::text AND g.kind = 'city'::text AND g.geom IS NOT NULL
          ORDER BY (g.geom::geometry <-> s.geom::geometry)
         LIMIT 1)) AS municipality_name,
    d.eva_no,
    COALESCE(d.ifopt,
        CASE
            WHEN s.id ~ '^[a-z]{2}:[0-9]{5}:[0-9]+$'::text THEN s.id
            ELSE NULL::text
        END) AS ifopt,
        CASE
            WHEN (EXISTS ( SELECT 1
               FROM ags_regions a
              WHERE a.ags = split_part(s.id, ':'::text, 2))) THEN 'bkg_gvisys'::text
            ELSE NULL::text
        END AS placement_source_ref,
    COALESCE(ARRAY( SELECT u.e
           FROM unnest(ARRAY[d.eva_no] || d.eva_nos) WITH ORDINALITY u(e, o)
          WHERE u.e IS NOT NULL
          GROUP BY u.e
          ORDER BY (min(u.o))), '{}'::text[]) AS eva_numbers
   FROM stops s
     JOIN feeds f ON f.gtfs_feed_key = s.gtfs_feed_key
     LEFT JOIN transit_stop_modes m ON m.gtfs_feed_key = s.gtfs_feed_key AND m.root_id = s.id
     LEFT JOIN LATERAL ( SELECT x.category,
            x.city,
            x.eva_no,
            x.eva_nos,
            x.ifopt
           FROM db_stations x
          WHERE x.ifopt = s.id AND x.closed_at IS NULL
          ORDER BY (x.source_id = 'db_stada'::text) DESC, x.station_no
         LIMIT 1) d ON true
  WHERE s.deleted_at IS NULL AND s.geom IS NOT NULL AND s.name IS NOT NULL AND (s.location_type = 1 OR COALESCE(s.location_type::integer, 0) = 0 AND s.parent_id IS NULL)`

// relStopPoint is the platform's stop point (kind v).
const relStopPoint = `
 SELECT (s.gtfs_feed_key || ':'::text) || s.id AS stop_point_id,
    (s.gtfs_feed_key || ':'::text) || COALESCE(s.parent_id, s.id) AS stop_place_id,
    NULLIF(btrim(s.platform_code), ''::text) AS platform_label,
    f.source_feed AS source_ref,
    'open'::text AS display_class,
    'public'::text AS audience,
    NULLIF(btrim(s.name), ''::text) AS name
   FROM stops s
     JOIN feeds f ON f.gtfs_feed_key = s.gtfs_feed_key
  WHERE s.deleted_at IS NULL AND COALESCE(s.location_type::integer, 0) = 0`

// relTrafficEvent is the platform's traffic event (kind v).
const relTrafficEvent = `
 WITH newest AS (
         SELECT s.road,
            s.kind,
            s.observed_at AS at
           FROM autobahn_sweeps s
          WHERE s.observed_at > (now() - '1 day'::interval) AND (s.kind = ANY (ARRAY['warning'::text, 'roadworks'::text, 'closure'::text, 'closure_entry_exit'::text]))
        ), autobahn AS (
         SELECT e.observed_at,
            e.road,
            e.kind,
            e.ext_id,
            e.title,
            e.subtitle,
            e.abnormal_traffic_type,
            e.delay_s,
            e.geom,
            e.extent,
            e.raw,
            e.source,
            e.source_run_id,
            e.raw_hash,
            COALESCE(e.raw, ( SELECT r.raw
                   FROM autobahn_events r
                  WHERE r.ext_id = e.ext_id AND r.raw IS NOT NULL AND r.raw_hash = e.raw_hash AND r.observed_at <= e.observed_at AND r.observed_at >= date_trunc('day'::text, e.observed_at, 'UTC'::text)
                  ORDER BY r.observed_at DESC
                 LIMIT 1)) AS p
           FROM autobahn_events e
             JOIN newest n ON n.road = e.road AND n.kind = e.kind AND n.at = e.observed_at
        ), b AS (
         SELECT 'autobahn:'::text || a.ext_id AS event_id,
                CASE
                    WHEN a.kind ~~ 'closure%'::text THEN 'closure'::text
                    WHEN a.kind = 'roadworks'::text THEN 'roadworks'::text
                    WHEN a.kind = 'warning'::text AND (a.abnormal_traffic_type = ANY (ARRAY['QUEUING_TRAFFIC'::text, 'STATIONARY_TRAFFIC'::text, 'SLOW_TRAFFIC'::text, 'HEAVY_TRAFFIC'::text])) THEN 'jam'::text
                    WHEN a.kind = 'warning'::text THEN 'hazard'::text
                    ELSE 'other'::text
                END AS event_kind,
                CASE
                    WHEN a.kind = 'closure'::text OR (a.p ->> 'isBlocked'::text) = 'true'::text THEN 'road_closed'::text
                    WHEN a.kind = 'closure_entry_exit'::text THEN 'major'::text
                    WHEN a.kind = 'roadworks'::text THEN 'lane_closed'::text
                    WHEN a.kind = 'warning'::text AND (a.abnormal_traffic_type = ANY (ARRAY['QUEUING_TRAFFIC'::text, 'STATIONARY_TRAFFIC'::text, 'SLOW_TRAFFIC'::text, 'HEAVY_TRAFFIC'::text])) THEN
                    CASE
                        WHEN a.delay_s >= 600 THEN 'major'::text
                        ELSE 'minor'::text
                    END
                    WHEN a.kind = 'warning'::text THEN 'info'::text
                    ELSE 'other'::text
                END AS severity,
            a.road AS road_ref,
            'motorway'::text AS road_class,
            'unknown'::text AS direction,
            NULL::text AS direction_label,
            st_pointonsurface(a.geom::geometry)::geometry(Point,4326) AS location,
            COALESCE(
                CASE
                    WHEN (a.p ->> 'startTimestamp'::text) ~ '^\d{4}-\d\d-\d\dT\d\d:\d\d'::text THEN (a.p ->> 'startTimestamp'::text)::timestamp with time zone
                    ELSE NULL::timestamp with time zone
                END, a.observed_at) AS valid_from,
            NULL::timestamp with time zone AS valid_to,
            NULL::integer AS lanes_closed,
            NULL::integer AS lanes_total,
                CASE
                    WHEN a.delay_s >= 0 THEN a.delay_s / 60
                    ELSE NULL::integer
                END AS delay_min,
            NULL::integer AS affected_length_m,
            COALESCE(a.title, a.kind) AS headline,
            ( SELECT string_agg(l.line, '
'::text ORDER BY l.n) AS string_agg
                   FROM jsonb_array_elements_text(
                        CASE
                            WHEN jsonb_typeof(a.p -> 'description'::text) = 'array'::text THEN a.p -> 'description'::text
                            ELSE NULL::jsonb
                        END) WITH ORDINALITY l(line, n)) AS description,
            'de'::text AS text_lang,
            a.source AS source_ref,
            a.ext_id AS origin_key,
            NULL::timestamp with time zone AS published_at,
            a.observed_at AS recorded_at,
            a.observed_at AS changed_at,
            NULL::timestamp with time zone AS withdrawn_at,
            'operator_event'::text AS record_class,
            a.subtitle,
            NULL::text AS place_text,
                CASE a.p ->> 'isBlocked'::text
                    WHEN 'true'::text THEN true
                    WHEN 'false'::text THEN false
                    ELSE NULL::boolean
                END AS is_blocked,
            a.abnormal_traffic_type AS traffic_type,
                CASE
                    WHEN a.delay_s >= 0 THEN a.delay_s
                    ELSE NULL::integer
                END AS delay_s,
            NULL::text AS confidentiality
           FROM autobahn a
        UNION ALL
         SELECT (('datex:'::text || e.source_id) || ':'::text) || e.publication_id,
                CASE
                    WHEN e.event_type ~* '(maintenance|roadworks|construction)'::text THEN 'roadworks'::text
                    WHEN e.event_type ~* '(accident|unfall)'::text THEN 'accident'::text
                    WHEN e.event_type ~* 'abnormaltraffic'::text THEN 'jam'::text
                    WHEN e.event_type ~* '(weather|environment)'::text THEN 'weather'::text
                    WHEN e.event_type ~* '(closure|sperrung)'::text THEN 'closure'::text
                    WHEN e.event_type ~* '(speedmanagement|restriction|reroutingmanagement|carriagewayorlanemanagement|networkmanagement)'::text THEN 'restriction'::text
                    WHEN e.event_type ~* '(obstruction|hazard|animal|vehicle|equipment)'::text THEN 'hazard'::text
                    ELSE 'other'::text
                END AS "case",
                CASE
                    WHEN e.event_type ~* '(closure|sperrung)'::text THEN 'road_closed'::text
                    WHEN e.event_type ~* '(accident|unfall)'::text THEN 'major'::text
                    WHEN e.event_type ~* '(maintenance|roadworks|construction)'::text THEN 'lane_closed'::text
                    WHEN e.event_type ~* '(abnormaltraffic|speedmanagement|restriction|reroutingmanagement|carriagewayorlanemanagement|networkmanagement)'::text THEN 'minor'::text
                    WHEN e.event_type ~* '(weather|environment|obstruction|hazard|animal|vehicle|equipment)'::text THEN 'info'::text
                    ELSE 'other'::text
                END AS "case",
            e.road_number,
                CASE
                    WHEN e.road_number ~ '^A ?[0-9]'::text THEN 'motorway'::text
                    WHEN e.road_number ~ '^B ?[0-9]'::text THEN 'federal'::text
                    WHEN e.road_number ~ '^(L|S|St) ?[0-9]'::text THEN 'state'::text
                    ELSE 'other'::text
                END AS "case",
            'unknown'::text,
            NULL::text,
            st_pointonsurface(e.geom)::geometry(Point,4326) AS st_pointonsurface,
            COALESCE(e.overall_start, e.first_seen_at) AS "coalesce",
            e.overall_end,
            NULL::integer,
            NULL::integer,
            NULL::integer,
            NULL::integer,
            COALESCE(e.event_type, 'situation'::text) AS "coalesce",
            NULL::text,
            'de'::text,
            e.source_feed,
            e.publication_id,
            e.feed_ts,
            e.first_seen_at,
            e.updated_at,
            e.closed_at,
            'traffic_message'::text,
            NULL::text,
            NULL::text,
            NULL::boolean,
            NULL::text,
            NULL::integer,
            e.confidentiality
           FROM road_events e
          WHERE e.deleted_at IS NULL
        )
 SELECT b.event_id,
    b.event_kind,
    b.severity,
    b.road_ref,
    b.road_class,
    b.direction,
    b.direction_label,
    b.location,
    b.valid_from,
    b.valid_to,
    b.lanes_closed,
    b.lanes_total,
    b.delay_min,
    b.affected_length_m,
    b.headline,
    b.description,
    b.text_lang,
    b.source_ref,
    b.origin_key,
    b.published_at,
    b.recorded_at,
    b.changed_at,
    b.withdrawn_at,
    b.record_class,
    b.subtitle,
    b.place_text,
    b.is_blocked,
    b.traffic_type,
    b.delay_s,
    ARRAY[b.source_ref] AS source_refs,
        CASE
            WHEN COALESCE(b.confidentiality, 'noRestriction'::text) <> 'noRestriction'::text THEN 'withhold'::text
            ELSE COALESCE(k.display_class, 'open'::text)
        END AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience,
    b.confidentiality,
    k.origin_restriction,
    land.code AS state_code,
        CASE
            WHEN land.code IS NOT NULL THEN 'bkg_gvisys'::text
            ELSE NULL::text
        END AS placement_source_ref
   FROM b
     LEFT JOIN LATERAL ( SELECT contract_junction_land(b.location::geography) AS code) land ON true
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE b.source_ref ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE NOT COALESCE(k.excluded, false)`

// relTransitLine is the platform's transit line (kind v).
const relTransitLine = `
 SELECT (r.gtfs_feed_key || ':'::text) || r.id AS line_id,
    COALESCE(NULLIF(btrim(r.short_name), ''::text), NULLIF(btrim(r.long_name), ''::text), r.id) AS line_name,
    contract_transport_mode(r.route_type) AS transport_mode,
    f.source_feed AS source_ref,
    'open'::text AS display_class,
    'public'::text AS audience
   FROM routes r
     JOIN feeds f ON f.gtfs_feed_key = r.gtfs_feed_key
  WHERE r.deleted_at IS NULL`

// relTransitPunctuality is the platform's transit punctuality (kind v).
const relTransitPunctuality = `
 SELECT p.level AS area_level,
    p.area AS area_code,
    p.name AS area_name,
    p.trips_observed,
    p.trips_late,
    p.trips_skipped_stop,
    p.trips_untimed,
    p.median_worst_delay_s,
    p.stops_seen,
    p.newest AS observed_at,
    'delfi_rt_tu'::text AS source_ref,
    COALESCE(k.display_class, 'open'::text) AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience
   FROM transit_punctuality_area_live p
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE 'delfi_rt_tu'::text ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE (p.trips_observed + p.trips_untimed + p.trips_skipped_stop) > 0 AND NOT COALESCE(k.excluded, false)`

// relTransitTripDelay is the platform's transit trip delay (kind v).
const relTransitTripDelay = `
 SELECT d.trip_id AS trip_ref,
    split_part(d.stop_id, ':'::text, 2) AS area_code,
    max(d.delay_s) AS worst_delay_s,
    max(d.observed_at) AS observed_at,
    'delfi_rt_tu'::text AS source_ref,
    COALESCE(k.display_class, 'open'::text) AS display_class,
    COALESCE(k.audience, 'public'::text) AS audience
   FROM transit_delays d
     LEFT JOIN LATERAL ( SELECT k_1.priority,
            k_1.pattern,
            k_1.excluded,
            k_1.audience,
            k_1.display_class,
            k_1.terms_gate,
            k_1.hold_reason,
            k_1.origin_restriction
           FROM contract_source_rules k_1
          WHERE 'delfi_rt_tu'::text ~ k_1.pattern
          ORDER BY k_1.priority
         LIMIT 1) k ON true
  WHERE d.source = 'delfi_rt_tu'::text AND d.delay_s IS NOT NULL AND d.stop_id ~* '^de:[0-9]+(:|$)'::text AND (length(split_part(d.stop_id, ':'::text, 2)) = ANY (ARRAY[2, 3, 5, 8])) AND d.observed_at >= (now() - '00:30:00'::interval) AND d.observed_at <= now() AND NOT COALESCE(k.excluded, false)
  GROUP BY d.trip_id, (split_part(d.stop_id, ':'::text, 2)), k.display_class, k.audience`

// relWeatherWarning is the platform's weather warning (kind v).
const relWeatherWarning = `
 SELECT 'dwd:'::text || id AS warning_id,
        CASE
            WHEN event ~* 'gewitter'::text THEN 'thunderstorm'::text
            WHEN event ~* '(sturm|wind|böen|orkan)'::text THEN 'wind'::text
            WHEN event ~* 'hochwasser|flut'::text THEN 'flood'::text
            WHEN event ~* 'schnee'::text THEN 'snow'::text
            WHEN event ~* '(glätte|glatteis|eis)'::text THEN 'ice'::text
            WHEN event ~* 'regen'::text THEN 'rain'::text
            WHEN event ~* 'nebel'::text THEN 'fog'::text
            WHEN event ~* 'hitze'::text THEN 'heat'::text
            WHEN event ~* 'frost'::text THEN 'frost'::text
            WHEN event ~* 'uv'::text THEN 'uv'::text
            ELSE 'other'::text
        END AS hazard,
    level::integer AS warning_level,
    COALESCE(severity, level::text) AS level_label,
        CASE
            WHEN closed_reason = 'withdrawn'::text THEN 'cancelled'::text
            ELSE 'actual'::text
        END AS message_status,
    'Deutscher Wetterdienst'::text AS issuer_name,
    COALESCE(headline, event) AS headline,
    'de'::text AS text_lang,
    valid_from,
    valid_to,
    published_at,
    ( SELECT a.ags
           FROM ags_regions a
          WHERE a.ags = substr(w.warncell_id, 2, 5)) AS region_code,
    source_feed AS source_ref,
    first_seen_at AS recorded_at,
    closed_at AS withdrawn_at,
    'open'::text AS display_class,
    'public'::text AS audience,
    warncell_id AS warn_cell_id,
    area_desc AS area_name,
    area::geometry(MultiPolygon,4326) AS area,
    event,
    identifier,
    last_seen_at AS seen_at
   FROM weather_warnings w
  WHERE deleted_at IS NULL AND valid_from IS NOT NULL`
