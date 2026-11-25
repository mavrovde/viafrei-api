-- Test-only: the base-table seed of viafrei-api's integration tests and
-- local stack. Adapted from viafrei-database 0.4.5
-- db/test-fixtures/contract_seed.sql (its INSTEAD OF triggers on the
-- contract views), so that the API's tests no longer write through the
-- contract views:
--
--   insert into vf_seed.<relation> (<contract columns>) values (...);
--
-- vf_seed.<relation> is a plain table with the columns the contract view
-- of the same name had at 0.4.5; an AFTER INSERT trigger on it writes the
-- platform tables the workers write, as the view's seed trigger did. While a
-- contract view of the same name still exists the row is read back through
-- it once the seed ends (vf_test.contract_seed_end); without the view the
-- read-back is skipped. Run in one transaction:
--
--   <this file>; select vf_test.contract_seed_begin('<name>'); <rows>; select vf_test.contract_seed_end();
--
-- (internal/testkit/baseseed.Script). Refused on any database whose name
-- does not start with test_.

DO $$
BEGIN
  IF current_database() NOT LIKE 'test\_%' THEN
    RAISE EXCEPTION 'vf_test contract seed is for a test_ database only, not %', current_database() USING ERRCODE = '42501';
  END IF;
END $$;

SET client_min_messages = warning;  -- a re-run's "already exists" notices are not the seed's news
CREATE SCHEMA IF NOT EXISTS vf_test;
REVOKE ALL ON SCHEMA vf_test FROM PUBLIC;

-- The platform's name folds, so a seeded row is searched as an ingested one is:
-- vf_test.name_fold is the ingest's normalizeName (lower case, ß as ss, the
-- diacritic dropped, every run of non-letters and non-digits one space), and
-- vf_test.admin_core its stripAdminWords (a ", Landkreis" suffix and leading
-- administrative words taken off), which the gazetteer and the Kreis map store
-- as name_norm; an address's street and house number are folded as the
-- address ingest folds them (normalizeStreet, normalizeHouseNumber). A plain lower() kept "ü" and "ß", which no query fold produces.
CREATE OR REPLACE FUNCTION vf_test.name_fold(t text) RETURNS text LANGUAGE sql IMMUTABLE AS $f$
  SELECT btrim(regexp_replace(
           translate(replace(lower(t), 'ß', 'ss'),
                     'àáâãäåāăąçćčĉèéêëēėęěìíîïīįñńňòóôõöøōśšşùúûüūůűýÿźżžłřğďť',
                     'aaaaaaaaacccceeeeeeeeiiiiiinnnooooooosssuuuuuuuyyzzzlrgdt'),
           '[^[:alnum:]]+', ' ', 'g'))
$f$;

CREATE OR REPLACE FUNCTION vf_test.admin_core(t text) RETURNS text LANGUAGE plpgsql IMMUTABLE AS $f$
DECLARE
  c int := length(t) - strpos(reverse(t), ',') + 1;
  cand text := t;
  w text[];
  i int := 1;
BEGIN
  IF strpos(t, ',') > 1 AND vf_test.name_fold(substr(t, c + 1)) = ANY (ARRAY['landkreis','kreis','stadtkreis','kreisfreie stadt','stadt','gemeinde',
       'verbandsgemeinde','samtgemeinde','markt','regierungsbezirk','bezirk','land','freistaat','bundesland']) THEN
    cand := substr(t, 1, c - 1);
  END IF;
  w := regexp_split_to_array(vf_test.name_fold(cand), ' ');
  WHILE i < coalesce(array_length(w, 1), 0) AND w[i] = ANY (ARRAY['landkreis','kreis','stadtkreis','regierungsbezirk','verbandsgemeinde','samtgemeinde','bundesland','freistaat']) LOOP
    i := i + 1;
  END LOOP;
  RETURN coalesce(nullif(array_to_string(w[i:], ' '), ''), vf_test.name_fold(t));
END $f$;

CREATE TABLE IF NOT EXISTS vf_test.contract_seed_runs (
    run_id    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    file_name text NOT NULL,
    seeded_at timestamptz NOT NULL DEFAULT now()
);

-- Called first in the seed's transaction: one seed per database.
CREATE OR REPLACE FUNCTION vf_test.contract_seed_begin(p_file text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  IF current_database() NOT LIKE 'test\_%' THEN
    RAISE EXCEPTION 'vf_test: not a test_ database (%)', current_database() USING ERRCODE = '42501';
  END IF;
  IF EXISTS (SELECT FROM vf_test.contract_seed_runs) THEN
    RAISE EXCEPTION 'vf_test contract seed: this database was seeded already (%); seed a fresh test database',
      (SELECT string_agg(file_name, ', ' ORDER BY run_id) FROM vf_test.contract_seed_runs) USING ERRCODE = '55000';
  END IF;
  INSERT INTO vf_test.contract_seed_runs (file_name) VALUES (p_file);
  -- The rows given, read back once the seed's last row is in (contract_seed_end).
  CREATE TEMP TABLE vf_seed_given (n bigint GENERATED ALWAYS AS IDENTITY, view_name text NOT NULL, want jsonb NOT NULL) ON COMMIT DROP;
END $$;

-- The read-back: a row of the view must carry every non-null value given.
-- Within a seed (contract_seed_begin) the row is noted and read back with
-- every other at the seed's end (contract_seed_end), one read of each view;
-- outside one it is read back at once (seed_read_back). Reading the view
-- back for each row as it was seeded made a seed quadratic in its size: a
-- view keyed by an expression (scheduled_call's journey_id) is read whole
-- for each row, and the API's ci set (44 k rows) took longer than the API's
-- 15-minute set-up.
CREATE OR REPLACE FUNCTION vf_test.seed_check(p_view text, p_row jsonb) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  IF to_regclass(format('contract.%I', p_view)) IS NULL THEN
    RETURN;  -- the contract view is gone: nothing to read back through
  END IF;
  IF to_regclass('pg_temp.vf_seed_given') IS NULL THEN
    PERFORM vf_test.seed_read_back(p_view, jsonb_strip_nulls(p_row));
  ELSE
    INSERT INTO vf_seed_given (view_name, want) VALUES (p_view, jsonb_strip_nulls(p_row));
  END IF;
END $$;

-- The seed's last step: each view is read once, and each row given is
-- looked for among the view's rows with the same first column (a hash join
-- on it), then must be contained in one. The first row given that no view
-- row carries is read back alone, which raises naming its cause.
CREATE OR REPLACE FUNCTION vf_test.contract_seed_end() RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  v text;
  id_col text;
  missing bigint;
  first_missing bigint;
BEGIN
  FOR v IN SELECT view_name FROM vf_seed_given GROUP BY view_name ORDER BY min(n) LOOP
    SELECT a.attname INTO id_col FROM pg_attribute a
     WHERE a.attrelid = format('contract.%I', v)::regclass AND a.attnum = 1;
    EXECUTE format('with r as materialized (select to_jsonb(x) as j from contract.%I x)
                    select min(g.n) from vf_seed_given g
                     where g.view_name = $1
                       and not exists (select from r where r.j -> $2 = g.want -> $2 and r.j @> g.want)
                       and (g.want ? $2 or not exists (select from r where r.j @> g.want))', v)
      INTO missing USING v, id_col;
    IF missing IS NOT NULL AND (first_missing IS NULL OR missing < first_missing) THEN
      first_missing := missing;
    END IF;
  END LOOP;
  IF first_missing IS NOT NULL THEN
    PERFORM vf_test.seed_read_back(g.view_name, g.want) FROM vf_seed_given g WHERE g.n = first_missing;
    RAISE EXCEPTION 'vf_test contract seed: row % given does not read back', first_missing USING ERRCODE = '22023';
  END IF;
END $$;

-- One row read back: when no row of the view carries every non-null value
-- given, the error names the cause: no row with the view's first column (the
-- view's own conditions leave the row out), else each column whose value
-- reads back otherwise, given against read.
CREATE OR REPLACE FUNCTION vf_test.seed_read_back(p_view text, want jsonb) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  ok boolean;
  id_col text;
  near jsonb;
  diff text;
BEGIN
  SELECT a.attname INTO id_col FROM pg_attribute a
   WHERE a.attrelid = format('contract.%I', p_view)::regclass AND a.attnum = 1;
  IF want ? id_col THEN
    EXECUTE format('select exists (select from contract.%1$I v where v.%2$I = (jsonb_populate_record(null::contract.%1$I, $1)).%2$I and to_jsonb(v) @> $1)',
                   p_view, id_col) INTO ok USING want;
  ELSE
    EXECUTE format('select exists (select from contract.%I v where to_jsonb(v) @> $1)', p_view) INTO ok USING want;
  END IF;
  IF ok THEN RETURN; END IF;
  EXECUTE format('select to_jsonb(v) from contract.%I v where to_jsonb(v) @> jsonb_build_object($1, $2) limit 1', p_view)
    INTO near USING id_col, want -> id_col;
  IF near IS NULL THEN
    RAISE EXCEPTION 'vf_test contract seed: contract.% shows no row with % = %: the view''s own conditions leave the row out (a column it requires is missing or outside its rules)',
      p_view, id_col, coalesce((want -> id_col)::text, 'null') USING ERRCODE = '22023';
  END IF;
  SELECT string_agg(format('%s = %s (reads back as %s)', k, want -> k, coalesce((near -> k)::text, 'null')), ', ' ORDER BY k) INTO diff
    FROM jsonb_object_keys(want) k WHERE NOT (near @> jsonb_build_object(k, want -> k));
  RAISE EXCEPTION 'vf_test contract seed: contract.% cannot hold %', p_view, diff USING ERRCODE = '22023';
END $$;

-- A tick's day partition: the fixture's dates are its own, not today's, and
-- a fixture outlives ensure_daily_partitions' 400-day window, so the one day
-- of the one table is made the first time a tick meets it (named and attached
-- as that function does, UTC days).
CREATE OR REPLACE FUNCTION vf_test.ensure_day(p_parent regclass, p_at timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  d date := (p_at AT TIME ZONE 'UTC')::date;
  ns text;
  t text;
  part text;
BEGIN
  SELECT n.nspname, c.relname INTO ns, t FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = p_parent AND c.relkind = 'p';
  IF t IS NULL THEN RETURN; END IF;  -- a table not partitioned by day takes the row as it is
  part := format('%s_%s', t, to_char(d, 'YYYYMMDD'));
  IF to_regclass(format('%I.%I', ns, part)) IS NOT NULL THEN RETURN; END IF;
  EXECUTE format('create table %I.%I (like %I.%I including all excluding indexes excluding statistics)', ns, part, ns, t);
  EXECUTE format('alter table %I.%I attach partition %I.%I for values from (%L) to (%L)', ns, t, ns, part,
                 d::timestamp AT TIME ZONE 'UTC', (d + 1)::timestamp AT TIME ZONE 'UTC');
END $$;

-- "<prefix>:<rest>" -> rest
CREATE OR REPLACE FUNCTION vf_test.after_colon(p text) RETURNS text LANGUAGE sql IMMUTABLE AS $$ select substr(p, strpos(p, ':') + 1) $$;
CREATE OR REPLACE FUNCTION vf_test.point(p geometry) RETURNS geography LANGUAGE sql IMMUTABLE AS $$ select coalesce(p, st_setsrid(st_makepoint(10, 51), 4326))::geography $$;

-- A register row for a source key, so a source note or register field has its row.
CREATE OR REPLACE FUNCTION vf_test.ensure_register(p_source text, p_attribution text DEFAULT NULL, p_url text DEFAULT NULL, p_label text DEFAULT NULL) RETURNS void LANGUAGE sql AS $$
  insert into public.source_register (source_id, attribution_text, source_url, licence_label, share_alike, share_alike_database,
                                      use_classes, consumer_answers_only, no_redistribution, register_version)
  select p_source, coalesce(p_attribution, c.attribution, p_source), coalesce(p_url, c.offer_url, 'https://example.org/' || p_source),
         coalesce(p_label, c.licence_label, 'CC BY 4.0'), false, false, array['consumer', 'non_commercial', 'commercial'], false, false, current_date
    from (select) one left join public.feed_catalog c on c.feed_key = p_source
  on conflict (source_id) do nothing
$$;

-- ------------------------------------------------------------------ register

CREATE OR REPLACE FUNCTION vf_test.seed_publisher() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.feed_catalog (feed_key, provider, display_name, licence_id, offer_url)
  VALUES (NEW.publisher_id, 'test', NEW.display_name, NEW.publisher_id, NEW.website)
  ON CONFLICT (feed_key) DO UPDATE SET display_name = excluded.display_name, offer_url = coalesce(excluded.offer_url, feed_catalog.offer_url);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cadence integer := coalesce(NEW.freshness_target_s, NEW.stale_after_s / 3);
BEGIN
  INSERT INTO public.feed_catalog (feed_key, provider, display_name, licence_id, licence_label, attribution, offer_url, cadence_s)
  VALUES (NEW.source_ref, 'test', NEW.source_ref, NEW.source_ref, NEW.licence_code, NEW.attribution_text, NEW.terms_url, cadence)
  ON CONFLICT (feed_key) DO UPDATE SET licence_label = excluded.licence_label, attribution = excluded.attribution,
         offer_url = coalesce(excluded.offer_url, feed_catalog.offer_url), cadence_s = excluded.cadence_s;
  IF NEW.use_classes IS NOT NULL OR NEW.review_signed_on IS NOT NULL OR NEW.changes_statement IS NOT NULL
     OR NEW.register_version IS NOT NULL OR NEW.share_alike_offer_url IS NOT NULL THEN
    INSERT INTO public.source_register (source_id, attribution_text, source_url, licence_label, share_alike, share_alike_database,
                                        changes_statement, use_classes, consumer_answers_only, no_redistribution, verified_on, register_version)
    VALUES (NEW.source_ref, coalesce(NEW.attribution_text, NEW.source_ref), coalesce(NEW.terms_url, 'https://example.org/' || NEW.source_ref),
            coalesce(NEW.licence_code, 'CC BY 4.0'), NEW.share_alike_offer_url IS NOT NULL, false, NEW.changes_statement,
            coalesce(nullif(NEW.use_classes, '{}'), array['consumer', 'non_commercial', 'commercial']), false, false,
            NEW.review_signed_on, coalesce(NEW.register_version, current_date))
    ON CONFLICT (source_id) DO NOTHING;
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_licence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.licence_texts (licence_label, url, url_kind)
  VALUES (NEW.licence_code, coalesce(NEW.licence_url, 'https://example.org/licence'), 'licence_text')
  ON CONFLICT (licence_label) DO UPDATE SET url = excluded.url;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_source_note() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM vf_test.ensure_register(NEW.source_ref);
  INSERT INTO public.source_register_notes (source_id, note_kind, lang, body) VALUES (NEW.source_ref, NEW.note_kind, NEW.lang, NEW.body);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_source_freshness() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO control.feed_state (ledger, key, updated_at, last_changed_at, last_success_at, cadence_s)
  VALUES ('feed', NEW.source_ref, now(), NEW.data_as_of, NEW.confirmed_at, NEW.confirm_interval_s);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_terms() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM vf_test.ensure_register(coalesce(NEW.source_ref, 'toll_collect_sid'));
  INSERT INTO public.source_terms (terms_gate, terms_id, terms_version, effective_on, source_id, provider_terms_url)
  VALUES (NEW.terms_gate, NEW.terms_id, NEW.terms_version, coalesce(NEW.effective_on, current_date), coalesce(NEW.source_ref, 'toll_collect_sid'),
          coalesce(NEW.provider_terms_url, 'https://example.org/terms'))
  ON CONFLICT (terms_gate) DO NOTHING;
  INSERT INTO public.source_terms_texts (terms_gate, lang, binding, terms_url, heading, body)
  VALUES (NEW.terms_gate, NEW.lang, coalesce(NEW.binding, NEW.lang = 'de'), coalesce(NEW.terms_url, 'https://example.org/terms'), NEW.heading, NEW.body);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- ----------------------------------------------------------------- reference

CREATE OR REPLACE FUNCTION vf_test.seed_admin_region() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.ags_regions (ags, level, name, name_norm, parent_ags, source_id)
  VALUES (NEW.region_code, CASE NEW.region_level WHEN 'state' THEN 'land' ELSE 'kreis' END, NEW.name, vf_test.admin_core(NEW.name),
          NEW.parent_code, coalesce(NEW.source_ref, 'destatis_gv'))
  ON CONFLICT (ags) DO UPDATE SET level = excluded.level, name = excluded.name, name_norm = excluded.name_norm,
                                  parent_ags = excluded.parent_ags, source_id = excluded.source_id;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_place() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.gazetteer_places (id, kind, name, name_norm, admin_code, rank, geom, source_id, external_id, first_seen_at, last_seen_at, closed_at)
  OVERRIDING SYSTEM VALUE
  VALUES (NEW.place_id::bigint,
          CASE NEW.place_kind WHEN 'municipality' THEN 'city' WHEN 'district' THEN 'district' WHEN 'state' THEN 'admin'
                              WHEN 'station_area' THEN 'station' WHEN 'locality' THEN 'island' WHEN 'motorway' THEN 'motorway' ELSE 'junction' END,
          NEW.name, vf_test.admin_core(NEW.name), NEW.official_key, coalesce(NEW.prominence, 0), vf_test.point(NEW.location),
          coalesce(NEW.source_ref, 'geonames'), coalesce(NEW.origin_key, NEW.place_id), coalesce(NEW.recorded_at, now()),
          coalesce(NEW.changed_at, now()), NEW.withdrawn_at);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- The place's own name is its 'name' alias; an exonym is its English name.
CREATE OR REPLACE FUNCTION vf_test.seed_place_alias() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.name_kind = 'exonym' THEN
    UPDATE public.gazetteer_places SET name_en = NEW.label WHERE id = NEW.place_id::bigint;
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_road() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.autobahn_roads (road) VALUES (NEW.road_ref) ON CONFLICT DO NOTHING;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- junction_id is "cid:tabcd:lcd". A state_code is read from the nearest BKG
-- municipality point, so one is planted at the junction for it, loaded at a
-- fixed instant (the API fixture world's first day) rather than at seed time:
-- a gazetteer's newest load per kind then reads the same on every run.
CREATE OR REPLACE FUNCTION vf_test.seed_junction() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.tmc_locations (cid, tabcd, lcd, lcl_version, class, tcd, stcd, junction_no, name, road_number, geom)
  VALUES (split_part(NEW.junction_id, ':', 1)::smallint, split_part(NEW.junction_id, ':', 2)::smallint, split_part(NEW.junction_id, ':', 3)::integer,
          '22.0', 'P', 1, CASE NEW.junction_kind WHEN 'interchange' THEN 1 WHEN 'triangle' THEN 2 ELSE 3 END,
          NEW.number, NEW.name, NEW.road_ref, vf_test.point(NEW.location));
  IF NEW.state_code IS NOT NULL THEN
    INSERT INTO public.gazetteer_places (kind, name, name_norm, admin_code, geom, source_id, external_id, first_seen_at, last_seen_at)
    VALUES ('city', 'Gemeinde ' || NEW.state_code, 'gemeinde ' || NEW.state_code, NEW.state_code || '000000',
            vf_test.point(NEW.location), 'bkg_gvisys', 'seed:' || NEW.junction_id, '2026-03-28T00:00:00Z', '2026-03-28T00:00:00Z');
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- -------------------------------------------------------------------- roads

-- event_id "autobahn:<ext_id>" is a tick of the newest Autobahn sweep (now);
-- "datex:<source_id>:<publication_id>" a DATEX II road event, whose headline
-- is its event type (the view reads it so). An Autobahn jam is a warning with
-- a standing traffic type; severity is derived from the kind, the delay and
-- isBlocked, never stored.
CREATE OR REPLACE FUNCTION vf_test.seed_traffic_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  kind text;
  dtype text;
BEGIN
  IF NEW.event_id LIKE 'autobahn:%' THEN
    kind := CASE NEW.event_kind WHEN 'closure' THEN 'closure' WHEN 'roadworks' THEN 'roadworks' ELSE 'warning' END;
    INSERT INTO public.autobahn_roads (road) VALUES (NEW.road_ref) ON CONFLICT DO NOTHING;
    PERFORM vf_test.ensure_day('public.autobahn_events', coalesce(NEW.recorded_at, now()));
    INSERT INTO public.autobahn_events (observed_at, road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, geom, raw, raw_hash, source)
    VALUES (coalesce(NEW.recorded_at, now()), NEW.road_ref, kind, vf_test.after_colon(NEW.event_id), NEW.headline, NEW.subtitle,
            coalesce(NEW.traffic_type, CASE WHEN NEW.event_kind = 'jam' THEN 'QUEUING_TRAFFIC' END), coalesce(NEW.delay_s, NEW.delay_min * 60), vf_test.point(NEW.location),
            jsonb_strip_nulls(jsonb_build_object(
              'startTimestamp', to_char(NEW.valid_from at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
              'isBlocked', NEW.is_blocked::text,
              'description', CASE WHEN NEW.description IS NOT NULL THEN to_jsonb(string_to_array(NEW.description, E'\n')) END)),
            decode(md5(NEW.event_id), 'hex'), coalesce(NEW.source_ref, 'autobahn'));
  ELSE
    dtype := coalesce(NEW.headline, CASE NEW.event_kind
      WHEN 'roadworks' THEN 'MaintenanceWorks' WHEN 'accident' THEN 'Accident' WHEN 'jam' THEN 'AbnormalTraffic'
      WHEN 'weather' THEN 'WeatherRelatedRoadConditions' WHEN 'closure' THEN 'Straßensperrung'
      WHEN 'restriction' THEN 'SpeedManagement' WHEN 'hazard' THEN 'GeneralObstruction' ELSE 'Other' END);
    INSERT INTO public.road_events (feed_key, source_id, publication_id, version, payload, event_type, road_number, geom,
                                    overall_start, overall_end, source_feed, feed_ts, first_seen_at, updated_at, closed_at, confidentiality)
    VALUES (NEW.source_ref, split_part(NEW.event_id, ':', 2), coalesce(NEW.origin_key, substr(NEW.event_id, length(split_part(NEW.event_id, ':', 2)) + 8)),
            1, '{}', dtype, NEW.road_ref, NEW.location, NEW.valid_from, NEW.valid_to, NEW.source_ref, NEW.published_at,
            coalesce(NEW.recorded_at, now()), coalesce(NEW.changed_at, now()), NEW.withdrawn_at, NEW.confidentiality);
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_traffic_event_observation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e record;
BEGIN
  IF NEW.event_id LIKE 'autobahn:%' THEN
    SELECT road, kind INTO e FROM public.autobahn_events WHERE ext_id = vf_test.after_colon(NEW.event_id) LIMIT 1;
    INSERT INTO public.autobahn_roads (road) VALUES (coalesce(e.road, 'A1')) ON CONFLICT DO NOTHING;
    PERFORM vf_test.ensure_day('public.autobahn_events', NEW.observed_at);
    INSERT INTO public.autobahn_events (observed_at, road, kind, ext_id, source)
    VALUES (NEW.observed_at, coalesce(e.road, 'A1'), coalesce(e.kind, 'warning'), vf_test.after_colon(NEW.event_id), coalesce(NEW.source_ref, 'autobahn'));
  ELSE
    SELECT feed_key, source_id, publication_id INTO e FROM public.road_events
     WHERE 'datex:' || source_id || ':' || publication_id = NEW.event_id LIMIT 1;
    PERFORM vf_test.ensure_day('public.road_event_ticks', NEW.observed_at);
    INSERT INTO public.road_event_ticks (observed_at, feed_key, source_id, publication_id, version, payload)
    VALUES (NEW.observed_at, coalesce(NEW.source_ref, e.feed_key), coalesce(e.source_id, split_part(NEW.event_id, ':', 2)),
            coalesce(e.publication_id, substr(NEW.event_id, length(split_part(NEW.event_id, ':', 2)) + 8)), coalesce(NEW.version_no, 1), '{}');
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- --------------------------------------------------------------------- fuel

CREATE OR REPLACE FUNCTION vf_test.seed_fuel_station() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.fuel_stations (id, name, brand, land, geom, state, source_feed, first_seen_at, updated_at,
                                    street, house_no, postcode, city, opening_times, whole_day)
  VALUES (NEW.station_id, NEW.name, NEW.brand, NEW.region_code, vf_test.point(NEW.location), nullif(NEW.open_state, 'unknown'),
          coalesce(NEW.source_ref, 'tankerkoenig'), coalesce(NEW.recorded_at, now()), coalesce(NEW.changed_at, now()),
          NEW.street, NEW.house_number, NEW.postcode, NEW.city, coalesce(NEW.opening_times, '[]'), NEW.whole_day);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_fuel_product() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.fuel_products (code, label, unit) VALUES (NEW.product_code, NEW.label, NEW.price_unit)
  ON CONFLICT (code) DO UPDATE SET label = excluded.label, unit = excluded.unit;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_fuel_price_observation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM vf_test.ensure_day('public.fuel_prices', NEW.observed_at);
  INSERT INTO public.fuel_prices (observed_at, station_id, product, price, source)
  VALUES (NEW.observed_at, NEW.station_id, NEW.product_code, NEW.price, coalesce(NEW.source_ref, 'tankerkoenig'));
  -- The newest price of a station and product, as the price ingest keeps it beside the ticks: the platform's
  -- readers (the MCP fuel tools) read this table, the read contract the ticks.
  INSERT INTO public.fuel_price_latest (station_id, product, price, is_open, observed_at, source_feed)
  SELECT NEW.station_id, NEW.product_code, NEW.price, s.state = 'open', NEW.observed_at, coalesce(NEW.source_ref, 'tankerkoenig')
    FROM public.fuel_stations s WHERE s.id = NEW.station_id
  ON CONFLICT (station_id, product) DO UPDATE
     SET price = excluded.price, is_open = excluded.is_open, observed_at = excluded.observed_at, source_feed = excluded.source_feed
   WHERE excluded.observed_at > public.fuel_price_latest.observed_at;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- ----------------------------------------------------------------- charging

-- site_id is "<source_feed>:<site>"; a point's id is its EVSE id, else
-- "<source_feed>:<point key>".
CREATE OR REPLACE FUNCTION vf_test.seed_charging_site() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE feed text := coalesce(NEW.source_ref, split_part(NEW.site_id, ':', 1));
BEGIN
  INSERT INTO public.charging_sites (source_feed, site_id, name, operator, geom, first_seen_at, updated_at, closed_at, confidentiality)
  VALUES (feed, substr(NEW.site_id, length(feed) + 2), NEW.name, NEW.operator_name, vf_test.point(NEW.location),
          coalesce(NEW.recorded_at, now()), coalesce(NEW.changed_at, now()), NEW.withdrawn_at, NEW.confidentiality);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_charge_point() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  feed text := coalesce(NEW.source_ref, split_part(NEW.site_id, ':', 1));
  site text := substr(NEW.site_id, length(feed) + 2);
BEGIN
  INSERT INTO public.charging_points (source_feed, point_key, site_id, evse_id, current_type, max_power_kw, geom, confidentiality,
                                      connector_types, price_per_kwh, price_adhoc_zero, price_last_updated, extras)
  VALUES (feed, CASE WHEN NEW.evse_code IS NULL AND NEW.point_id LIKE feed || ':%' THEN substr(NEW.point_id, length(feed) + 2) ELSE NEW.point_id END,
          site, NEW.evse_code,
          CASE NEW.current_kind WHEN 'ac' THEN 'AC_3_PHASE' WHEN 'dc' THEN 'DC' WHEN 'other' THEN 'OTHER' END,
          NEW.power_max_kw,
          coalesce((SELECT s.geom FROM public.charging_sites s WHERE s.source_feed = feed AND s.site_id = site), vf_test.point(NULL)),
          NEW.confidentiality,
          coalesce(array(SELECT CASE c WHEN 'ccs2' THEN 'iec62196T2COMBO' WHEN 'type2' THEN 'iec62196T2' WHEN 'chademo' THEN 'chademo' ELSE c END
                           FROM unnest(NEW.connectors) c), '{}'),
          NEW.price_per_kwh, NEW.price_adhoc_zero, NEW.price_updated_at,
          jsonb_strip_nulls(jsonb_build_object('device', jsonb_strip_nulls(jsonb_build_object(
            'openingHours', CASE NEW.opening_hours_kind WHEN 'always' THEN '247' WHEN 'restricted' THEN 'Eingeschränkt' END,
            'payment', nullif(concat_ws(';', CASE WHEN NEW.payment_card THEN 'Kreditkarte' END, CASE WHEN NEW.free_of_charge THEN 'Kostenlos' END,
                                        CASE WHEN NEW.payment_card = false OR NEW.free_of_charge = false THEN 'Bargeld' END), ''),
            'parking', CASE NEW.parking_access WHEN 'public' THEN 'keine Beschränkung' WHEN 'customers' THEN 'nur für Kunden' END)))));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_charge_point_state_observation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM vf_test.ensure_day('public.charging_point_status', NEW.observed_at);
  INSERT INTO public.charging_point_status (observed_at, evse_id, source_feed, raw, status, confidentiality)
  VALUES (NEW.observed_at, NEW.point_id, NEW.source_ref, '{}',
          CASE NEW.availability WHEN 'available' THEN 'AVAILABLE' WHEN 'occupied' THEN 'CHARGING' WHEN 'reserved' THEN 'RESERVED'
                                WHEN 'out_of_order' THEN 'OUTOFORDER' WHEN 'other' THEN 'OTHER' END,
          NEW.confidentiality);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- ------------------------------------------------------------------ parking

-- facility_id is "<family>:<id>" with family p, bysa, odbl or sid.
CREATE OR REPLACE FUNCTION vf_test.seed_parking_facility() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  fam text := split_part(NEW.facility_id, ':', 1);
  fid bigint := vf_test.after_colon(NEW.facility_id)::bigint;
  k text := coalesce(NEW.stored_kind, CASE NEW.parking_kind WHEN 'rest_area' THEN 'rest_area' WHEN 'park_and_ride' THEN 'park_and_ride' WHEN 'truck_parking' THEN 'truck' ELSE 'car_park' END);
  g geography := vf_test.point(NEW.location);
  ext text := coalesce(NEW.origin_key, vf_test.after_colon(NEW.facility_id));
BEGIN
  IF fam = 'p' THEN
    INSERT INTO public.parking_facilities (id, source_feed, external_id, kind, name, operator, road, geom, attribution_id, first_seen_at, updated_at, closed_at, confidentiality,
                                           facilities, facilities_absent, facilities_source)
    OVERRIDING SYSTEM VALUE VALUES (fid, NEW.source_ref, ext, k, NEW.name, NEW.operator_name, NEW.road_ref, g, NEW.source_ref,
                                    coalesce(NEW.recorded_at, now()), coalesce(NEW.changed_at, now()), NEW.withdrawn_at, NEW.confidentiality,
                                    coalesce(NEW.amenities, '{}'), coalesce(NEW.amenities_absent, '{}'),
                                    CASE WHEN cardinality(coalesce(NEW.amenities, '{}')) + cardinality(coalesce(NEW.amenities_absent, '{}')) > 0 THEN NEW.source_ref END);
  ELSIF fam = 'bysa' THEN
    INSERT INTO public.parking_facilities_bysa (id, source_feed, external_id, kind, name, operator, road, geom, attribution_id, first_seen_at, updated_at, closed_at, confidentiality)
    OVERRIDING SYSTEM VALUE VALUES (fid, NEW.source_ref, ext, k, NEW.name, NEW.operator_name, NEW.road_ref, g, 'mobilithek_park_nrw_pr',
                                    coalesce(NEW.recorded_at, now()), coalesce(NEW.changed_at, now()), NEW.withdrawn_at, NEW.confidentiality);
  ELSIF fam = 'odbl' THEN
    INSERT INTO public.parking_facilities_odbl (id, source_feed, external_id, kind, name, operator, road, geom, attribution_id, first_seen_at, updated_at, closed_at, confidentiality)
    OVERRIDING SYSTEM VALUE VALUES (fid, NEW.source_ref, ext, k, NEW.name, NEW.operator_name, NEW.road_ref, g, 'mobilithek_park_trucklounge',
                                    coalesce(NEW.recorded_at, now()), coalesce(NEW.changed_at, now()), NEW.withdrawn_at, NEW.confidentiality);
  ELSIF fam = 'sid' THEN
    INSERT INTO public.parking_facilities_sid (id, source_feed, external_id, kind, name, geom, attribution_id, first_seen_at, updated_at, closed_at, confidentiality)
    OVERRIDING SYSTEM VALUE VALUES (fid, coalesce(NEW.source_ref, 'truckpark_sid_static'), ext, k, NEW.name, g, 'toll_collect_sid',
                                    coalesce(NEW.recorded_at, now()), coalesce(NEW.changed_at, now()), NEW.withdrawn_at, NEW.confidentiality);
  ELSE
    RAISE EXCEPTION 'vf_test contract seed: parking_facility id % names no family (p:, bysa:, odbl:, sid:)', NEW.facility_id USING ERRCODE = '22023';
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_occupancy_observation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  fam text := split_part(NEW.facility_id, ':', 1);
  fid bigint := vf_test.after_colon(NEW.facility_id)::bigint;
  st text := CASE NEW.occupancy_status WHEN 'free' THEN 'spacesAvailable' WHEN 'filling' THEN 'almostFull' WHEN 'full' THEN 'full'
                                       WHEN 'closed' THEN 'closed' WHEN 'other' THEN 'other' END;
BEGIN
  PERFORM vf_test.ensure_day(CASE fam WHEN 'odbl' THEN 'public.parking_occupancy_odbl' WHEN 'sid' THEN 'public.parking_occupancy_sid' ELSE 'public.parking_occupancy' END::regclass, NEW.observed_at);
  IF fam = 'p' THEN
    INSERT INTO public.parking_occupancy (observed_at, facility_id, source_feed, raw, spaces_free, spaces_total, status, confidentiality)
    VALUES (NEW.observed_at, fid, NEW.source_ref, '{}', NEW.free_spaces, NEW.total_spaces, st, NEW.confidentiality);
  ELSIF fam = 'odbl' THEN
    INSERT INTO public.parking_occupancy_odbl (observed_at, facility_id, source_feed, attribution_id, raw, spaces_free, spaces_total, status, confidentiality)
    VALUES (NEW.observed_at, fid, NEW.source_ref, 'mobilithek_park_trucklounge_occ', '{}', NEW.free_spaces, NEW.total_spaces, st, NEW.confidentiality);
  ELSIF fam = 'sid' THEN
    INSERT INTO public.parking_occupancy_sid (observed_at, facility_id, source_feed, raw, spaces_free, spaces_total, status, confidentiality)
    VALUES (NEW.observed_at, fid, coalesce(NEW.source_ref, 'truckpark_sid_dyn'), '{}', NEW.free_spaces, NEW.total_spaces, st, NEW.confidentiality);
  ELSE
    RAISE EXCEPTION 'vf_test contract seed: occupancy_observation id % names no family with ticks (p:, odbl:, sid:)', NEW.facility_id USING ERRCODE = '22023';
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- ------------------------------------------------------------------ transit

-- Static GTFS ids are "<gtfs_feed_key>:<id>"; the feeds row of that key
-- names the source (source_ref, default delfi_static).
CREATE OR REPLACE FUNCTION vf_test.ensure_feed(p_key smallint, p_source text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT FROM public.feeds WHERE gtfs_feed_key = p_key) THEN
    INSERT INTO public.feeds (provider, feed_version, gtfs_feed_key, source_feed) VALUES ('test', 'seed', p_key, coalesce(p_source, 'delfi_static'));
  END IF;
END $$;

-- The one StaDa station whose IFOPT is a stop's id (FaSta names it; the stop
-- place reads its category, EVA number and municipality); its number is
-- derived from the IFOPT, so a second call finds the first one's row.
CREATE OR REPLACE FUNCTION vf_test.ensure_station(p_ifopt text, p_name text) RETURNS text LANGUAGE plpgsql AS $$
DECLARE no text;
BEGIN
  SELECT station_no INTO no FROM public.db_stations WHERE ifopt = p_ifopt AND source_id = 'db_stada' ORDER BY station_no LIMIT 1;
  IF no IS NULL THEN
    no := (800000000 + abs(hashtext(p_ifopt)) % 100000000)::text;
    INSERT INTO public.db_stations (station_no, name, content_hash, first_seen_at, last_seen_at, updated_at, ifopt, source_id)
    VALUES (no, p_name, 'seed', now(), now(), now(), p_ifopt, 'db_stada');
    -- a station has a page (station_index_live): its slug, letters only after "seed-"
    INSERT INTO public.station_slugs (slug, station_no, name)
    VALUES ('seed-' || translate(substr(md5(p_ifopt), 1, 12), '0123456789', 'ghijklmnop'), no, p_name);
  END IF;
  RETURN no;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_stop_place() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k smallint := split_part(NEW.stop_place_id, ':', 1)::smallint;
BEGIN
  PERFORM vf_test.ensure_feed(k, NEW.source_ref);
  INSERT INTO public.stops (gtfs_feed_key, id, name, geom, location_type)
  VALUES (k, vf_test.after_colon(NEW.stop_place_id), NEW.name, vf_test.point(NEW.location), 1);
  -- category, EVA number and municipality name are a DB station's whose IFOPT is the stop's id
  IF NEW.station_category IS NOT NULL OR NEW.eva_no IS NOT NULL OR NEW.municipality_name IS NOT NULL OR cardinality(NEW.eva_numbers) > 0 THEN
    PERFORM vf_test.ensure_station(vf_test.after_colon(NEW.stop_place_id), NEW.name);
    UPDATE public.db_stations SET eva_no = NEW.eva_no, eva_nos = coalesce(NEW.eva_numbers, '{}'), category = NEW.station_category, city = NEW.municipality_name
     WHERE ifopt = vf_test.after_colon(NEW.stop_place_id);
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_stop_point() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  k smallint := split_part(NEW.stop_point_id, ':', 1)::smallint;
  parent text := nullif(vf_test.after_colon(NEW.stop_place_id), vf_test.after_colon(NEW.stop_point_id));
  p record;
BEGIN
  PERFORM vf_test.ensure_feed(k, NEW.source_ref);
  SELECT name, geom INTO p FROM public.stops WHERE gtfs_feed_key = k AND id = parent;
  INSERT INTO public.stops (gtfs_feed_key, id, name, geom, location_type, parent_id, platform_code)
  VALUES (k, vf_test.after_colon(NEW.stop_point_id), coalesce(NEW.name, p.name, 'Halt'), coalesce(p.geom, vf_test.point(NULL)), 0, parent, NEW.platform_label);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_transit_line() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k smallint := split_part(NEW.line_id, ':', 1)::smallint;
BEGIN
  PERFORM vf_test.ensure_feed(k, NEW.source_ref);
  INSERT INTO public.routes (gtfs_feed_key, id, route_type, short_name)
  VALUES (k, vf_test.after_colon(NEW.line_id),
          CASE NEW.transport_mode WHEN 'sbahn' THEN 109 WHEN 'rail' THEN 2 WHEN 'subway' THEN 1 WHEN 'tram' THEN 0
                                  WHEN 'bus' THEN 3 WHEN 'ferry' THEN 4 ELSE 7 END,
          NEW.line_name);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- journey_id is "<key>:<trip>:<YYYYMMDD>"; the trip runs on that one date.
CREATE OR REPLACE FUNCTION vf_test.seed_journey() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  k smallint := split_part(NEW.journey_id, ':', 1)::smallint;
  trip text := substr(NEW.journey_id, length(split_part(NEW.journey_id, ':', 1)) + 2, length(NEW.journey_id) - length(split_part(NEW.journey_id, ':', 1)) - 10);
  d date := coalesce(NEW.service_date, to_date(right(NEW.journey_id, 8), 'YYYYMMDD'));
BEGIN
  PERFORM vf_test.ensure_feed(k, NEW.source_ref);
  INSERT INTO public.trips (gtfs_feed_key, id, route_id, service_id, headsign)
  VALUES (k, trip, vf_test.after_colon(NEW.line_id), 'seed:' || trip, NEW.destination_label)
  ON CONFLICT DO NOTHING;
  INSERT INTO public.gtfs_service_dates (service_date, gtfs_feed_key, service_id) VALUES (d, k, 'seed:' || trip) ON CONFLICT DO NOTHING;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- Times are stored as GTFS offsets from noon minus 12 h of the service date
-- in Europe/Berlin, as the view reads them.
CREATE OR REPLACE FUNCTION vf_test.seed_scheduled_call() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  k smallint := split_part(NEW.journey_id, ':', 1)::smallint;
  trip text := substr(NEW.journey_id, length(split_part(NEW.journey_id, ':', 1)) + 2, length(NEW.journey_id) - length(split_part(NEW.journey_id, ':', 1)) - 10);
  base timestamptz := ((to_date(right(NEW.journey_id, 8), 'YYYYMMDD') + time '12:00') at time zone 'Europe/Berlin') - interval '12 hours';
BEGIN
  INSERT INTO public.stop_times (gtfs_feed_key, trip_id, stop_sequence, stop_id, arrival, departure)
  VALUES (k, trip, NEW.call_order, vf_test.after_colon(NEW.stop_point_id), NEW.planned_arrival_at - base, NEW.planned_departure_at - base);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- A realtime tick of a DELFI trip (key 1): the delay is the expected time
-- less the planned one of the same call.
CREATE OR REPLACE FUNCTION vf_test.seed_call_observation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  trip text := substr(NEW.journey_id, 3, length(NEW.journey_id) - 11);
  c record;
BEGIN
  SELECT ((to_date(right(NEW.journey_id, 8), 'YYYYMMDD') + time '12:00') AT TIME ZONE 'Europe/Berlin') - interval '12 hours' + st.arrival AS planned_arrival_at,
         ((to_date(right(NEW.journey_id, 8), 'YYYYMMDD') + time '12:00') AT TIME ZONE 'Europe/Berlin') - interval '12 hours' + st.departure AS planned_departure_at,
         st.gtfs_feed_key::text || ':' || st.stop_id AS stop_point_id
    INTO c FROM public.stop_times st
   WHERE st.gtfs_feed_key::text = split_part(NEW.journey_id, ':', 1) AND st.trip_id = trip
     AND st.stop_sequence = NEW.call_order AND st.deleted_at IS NULL;
  PERFORM vf_test.ensure_day('public.transit_delays', NEW.observed_at);
  INSERT INTO public.transit_delays (observed_at, feed_ts, trip_id, stop_id, stop_sequence, delay_s, schedule_relationship, source)
  VALUES (NEW.observed_at, NEW.observed_at, trip, vf_test.after_colon(c.stop_point_id), NEW.call_order,
          coalesce(extract(epoch FROM coalesce(NEW.expected_arrival_at - c.planned_arrival_at, NEW.expected_departure_at - c.planned_departure_at))::integer, 0),
          upper(coalesce(NEW.call_status, 'scheduled')), coalesce(NEW.source_ref, 'delfi_rt_tu'));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- A departure is the stop, line, trip, service date and stop time the view
-- reads, plus a later stop unless it terminates there: a stop of another
-- station ('next:' and the stop's id, no DHID the station's family holds). A realtime figure is
-- seeded through the feed it names, which is marked up: delfi_rt_tu writes a
-- tick of the call (SKIPPED when cancelled), delfi_siri_et* a call at the
-- stop's DHID with the platform; either applies only to a call planned within
-- six hours of now, as the view reads them.
CREATE OR REPLACE FUNCTION vf_test.seed_departure() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  k smallint := split_part(NEW.journey_id, ':', 1)::smallint;
  trip text := substr(NEW.journey_id, length(split_part(NEW.journey_id, ':', 1)) + 2, length(NEW.journey_id) - length(split_part(NEW.journey_id, ':', 1)) - 10);
  day date := to_date(right(NEW.journey_id, 8), 'YYYYMMDD');
  base timestamptz := ((to_date(right(NEW.journey_id, 8), 'YYYYMMDD') + time '12:00') at time zone 'Europe/Berlin') - interval '12 hours';
  point text := vf_test.after_colon(NEW.stop_point_id);
  place text := vf_test.after_colon(coalesce(NEW.stop_place_id, NEW.stop_point_id));
  line text := coalesce(vf_test.after_colon(NEW.line_id), 'seed-line:' || trip);
  feed text;
BEGIN
  PERFORM vf_test.ensure_feed(k, NEW.source_ref);
  IF place <> point THEN
    INSERT INTO public.stops (gtfs_feed_key, id, name, geom, location_type) VALUES (k, place, 'Bahnhof', vf_test.point(NULL), 1) ON CONFLICT DO NOTHING;
  END IF;
  INSERT INTO public.stops (gtfs_feed_key, id, name, geom, location_type, parent_id, platform_code)
  VALUES (k, point, 'Bahnhof', vf_test.point(NULL), 0, nullif(place, point), NEW.planned_platform)
  ON CONFLICT DO NOTHING;
  INSERT INTO public.routes (gtfs_feed_key, id, route_type, short_name)
  VALUES (k, line, coalesce(NEW.route_type, CASE NEW.transport_mode WHEN 'sbahn' THEN 109 WHEN 'rail' THEN 2 WHEN 'subway' THEN 1 WHEN 'tram' THEN 0
                                                                    WHEN 'bus' THEN 3 WHEN 'ferry' THEN 4 ELSE 7 END), coalesce(NEW.line_name, line))
  ON CONFLICT DO NOTHING;
  INSERT INTO public.trips (gtfs_feed_key, id, route_id, service_id, headsign, trip_short_name)
  VALUES (k, trip, line, 'seed:' || trip, NEW.destination_label, NEW.train_number)
  ON CONFLICT DO NOTHING;
  INSERT INTO public.gtfs_service_dates (service_date, gtfs_feed_key, service_id) VALUES (day, k, 'seed:' || trip) ON CONFLICT DO NOTHING;
  INSERT INTO public.stop_times (gtfs_feed_key, trip_id, stop_sequence, stop_id, arrival, departure, pickup_type)
  VALUES (k, trip, NEW.call_order, point, coalesce(NEW.planned_arrival_at, NEW.planned_departure_at) - base, NEW.planned_departure_at - base,
          CASE WHEN NEW.boarding = false THEN 1 ELSE 0 END);
  IF NOT coalesce(NEW.terminates, false) THEN
    INSERT INTO public.stop_times (gtfs_feed_key, trip_id, stop_sequence, stop_id, arrival, departure)
    VALUES (k, trip, NEW.call_order + 1, 'next:' || point, coalesce(NEW.planned_departure_at, NEW.planned_arrival_at) - base + interval '5 minutes',
            coalesce(NEW.planned_departure_at, NEW.planned_arrival_at) - base + interval '5 minutes')
    ON CONFLICT DO NOTHING;
  END IF;
  feed := CASE WHEN NEW.realtime_source = 'delfi_rt_tu' THEN 'tripupdates' WHEN NEW.realtime_source LIKE 'delfi_siri_et%' THEN NEW.realtime_source END;
  IF feed IS NOT NULL THEN
    INSERT INTO public.feed_catalog (feed_key, provider, display_name, licence_id, cadence_s)
    VALUES (feed, 'delfi', feed, 'cc-by-sa', 120) ON CONFLICT DO NOTHING;
    INSERT INTO control.feed_state (ledger, key, last_success_at, updated_at) VALUES ('feed', feed, now(), now())
    ON CONFLICT (ledger, key) DO UPDATE SET last_success_at = now();
  END IF;
  IF NEW.realtime_source = 'delfi_rt_tu' THEN
    PERFORM vf_test.ensure_day('public.transit_delays', now());
    INSERT INTO public.transit_delays (observed_at, feed_ts, trip_id, stop_id, stop_sequence, delay_s, schedule_relationship, source)
    VALUES (now(), now(), trip, point, NEW.call_order, CASE WHEN coalesce(NEW.cancelled, false) THEN NULL ELSE coalesce(NEW.delay_s, NEW.delay_min * 60, 0) END,
            CASE WHEN coalesce(NEW.cancelled, false) THEN 'SKIPPED' ELSE 'SCHEDULED' END, 'delfi_rt_tu');
  ELSIF NEW.realtime_source LIKE 'delfi_siri_et%' THEN
    INSERT INTO public.delfi_siri_et_calls_latest (stop_ref, service_day, journey_ref, visit, source_feed, observed_at, line, category, destination, platform,
                                                   aimed_dep, expected_dep, delay_s, recorded, cancelled, journey_cancelled)
    VALUES (point, day, 'seed:' || trip, 1, NEW.realtime_source, now(), coalesce(NEW.line_name, line), NEW.train_category, NEW.destination_label,
            CASE WHEN NEW.platform IS DISTINCT FROM NEW.planned_platform THEN NEW.platform END,
            NEW.planned_departure_at, NEW.planned_departure_at + make_interval(secs => coalesce(NEW.delay_s, NEW.delay_min * 60, 0)), NULL, false,
            coalesce(NEW.cancelled, false), false);
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- "equipment:<id>" is an OpenStation/KVB lift or escalator with its state,
-- "fasta:<number>" a DB FaSta facility.
CREATE OR REPLACE FUNCTION vf_test.seed_station_facility() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE station text;
BEGIN
  IF NEW.facility_id LIKE 'fasta:%' THEN
    -- FaSta names a DB station; its stop place is that station's IFOPT
    IF NEW.stop_place_id IS NOT NULL THEN
      station := vf_test.ensure_station(vf_test.after_colon(NEW.stop_place_id), 'Bahnhof');
    END IF;
    INSERT INTO public.db_facilities (equipment_no, station_no, kind, state, state_since, first_seen_at, last_seen_at, source_feed, source_publisher, description)
    VALUES (vf_test.after_colon(NEW.facility_id), coalesce(station, '1'), NEW.facility_kind, NEW.facility_status, coalesce(NEW.status_observed_at, now()),
            now(), coalesce(NEW.status_observed_at, now()), coalesce(NEW.source_ref, 'db_fasta'), 'DB', NEW.position_text);
  ELSE
    INSERT INTO public.station_equipment (equipment_id, kind, stop_place_id, description, first_seen_at, last_seen_at, source_feed, source_publisher)
    VALUES (vf_test.after_colon(NEW.facility_id), NEW.facility_kind, vf_test.after_colon(NEW.stop_place_id), NEW.position_text,
            now(), now(), 'openstation_netex', 'DB');
    INSERT INTO public.station_equipment_state (equipment_id, status, state, status_since, last_seen_at, known_equipment, source_feed, source_publisher)
    -- the state was last restated when the row says it was observed, as
    -- FaSta's row above: stamped now(), every seeded state read as current
    VALUES (vf_test.after_colon(NEW.facility_id), upper(NEW.facility_status), NEW.facility_status, coalesce(NEW.status_observed_at, now()),
            coalesce(NEW.status_observed_at, now()), true, coalesce(NEW.source_ref, 'openstation_siri_fm'), 'DB');
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- ------------------------------------------------------------------ weather

-- warning_id is "dwd:<id>"; the warn cell is warn_cell_id when given, else
-- 1 + the region (a Kreis) + 000; identifier and event default to the id
-- and the hazard's DWD name.
CREATE OR REPLACE FUNCTION vf_test.seed_weather_warning() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.weather_warnings (id, source_feed, source_id, identifier, warncell_id, version, level, severity, event, headline,
                                       official_sha256, area_sha256, valid_from, valid_to, published_at, raw, first_seen_at, closed_at, closed_reason,
                                       area_desc, area, last_seen_at)
  OVERRIDING SYSTEM VALUE
  VALUES (vf_test.after_colon(NEW.warning_id)::bigint, coalesce(NEW.source_ref, 'dwd_warnings'), 'dwd', coalesce(NEW.identifier, NEW.warning_id),
          coalesce(NEW.warn_cell_id, '1' || coalesce(NEW.region_code, '00000') || '000'), 1, NEW.warning_level, NEW.level_label,
          coalesce(NEW.event, CASE NEW.hazard WHEN 'thunderstorm' THEN 'GEWITTER' WHEN 'wind' THEN 'STURMBÖEN' WHEN 'flood' THEN 'HOCHWASSER'
                          WHEN 'snow' THEN 'SCHNEEFALL' WHEN 'ice' THEN 'GLÄTTE' WHEN 'rain' THEN 'STARKREGEN' WHEN 'fog' THEN 'NEBEL'
                          WHEN 'heat' THEN 'HITZE' WHEN 'frost' THEN 'FROST' WHEN 'uv' THEN 'UV-INDEX' ELSE 'SONSTIGES' END),
          NEW.headline, repeat('a', 64), CASE WHEN NEW.area IS NULL THEN repeat('a', 64) ELSE md5(st_astext(NEW.area)) || md5(st_astext(NEW.area)) END,
          NEW.valid_from, NEW.valid_to, NEW.published_at, '{}',
          coalesce(NEW.recorded_at, now()),
          coalesce(NEW.withdrawn_at, CASE WHEN NEW.message_status = 'cancelled' THEN now() END),
          CASE WHEN NEW.message_status = 'cancelled' THEN 'withdrawn' WHEN NEW.withdrawn_at IS NOT NULL THEN 'superseded' END,
          -- the appended cell columns: its name, its boundary (a polygon made a multipolygon), its newest load
          NEW.area_name, st_multi(NEW.area)::geography, coalesce(NEW.seen_at, now()));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- -------------------------------------------------------------- rules, coverage

CREATE OR REPLACE FUNCTION vf_test.seed_rule_topic() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.driving_rule_topics (topic_code, title_de, title_en, reviewed_on)
  VALUES (NEW.topic_code, NEW.title, coalesce(NEW.title_en, NEW.title), coalesce(NEW.reviewed_on, current_date));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_rule_statement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.driving_rule_facts (fact_key, topic_code, position, body_de, body_en, source_url, source_name, source_note_de, source_note_en,
                                         to_verify_de, to_verify_en, reviewed_on, headline)
  VALUES (NEW.statement_id, NEW.topic_code, NEW.position, NEW.body, coalesce(NEW.body_en, NEW.body), coalesce(NEW.legal_basis_url, 'https://example.org/law'),
          NEW.legal_basis, NEW.legal_basis_note, coalesce(NEW.legal_basis_note_en, NEW.legal_basis_note), NEW.to_verify,
          coalesce(NEW.to_verify_en, NEW.to_verify), coalesce(NEW.reviewed_on, current_date), coalesce(NEW.headline, false));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_message_coverage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.message_coverage_sources (state_code, source_ref, coverage_role, current_only, measured_on)
  VALUES (NEW.state_code, NEW.source_ref, NEW.coverage_role, coalesce(NEW.current_only, false),
          coalesce(NEW.measured_on, CASE WHEN NEW.coverage_role = 'carried' THEN current_date END));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- contract_info states the version the migrations published: never seeded.
CREATE OR REPLACE FUNCTION vf_test.seed_refused() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'vf_test contract seed: vf_seed.% is not seeded (the migrations state it)', TG_TABLE_NAME USING ERRCODE = '0A000';
END $$;

-- ------------------------------------------------- the 0.3 read additions

-- station_id is "<feed>:<id>".
CREATE OR REPLACE FUNCTION vf_test.seed_hydrogen_station() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE feed text := coalesce(NEW.source_ref, split_part(NEW.station_id, ':', 1));
BEGIN
  INSERT INTO public.hydrogen_stations (id, source_feed, external_id, name, operator, brand, geom, address, refill, opening, attribution_id,
                                        provider_updated_at, first_seen_at, last_seen_at)
  VALUES (vf_test.after_colon(NEW.station_id)::bigint, feed, vf_test.after_colon(NEW.station_id), NEW.name, NEW.operator_name, NEW.brand,
          vf_test.point(NEW.location), NEW.address, coalesce(NEW.refill, '[{"solution":"700bar"}]'), NEW.opening, 'mobilithek_h2_stations',
          NEW.provider_updated_at, coalesce(NEW.recorded_at, now()), coalesce(NEW.last_seen_at, now()));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- camera_id is "osm:node<osm id>", "se:<id>" or "hh:<camera id>".
CREATE OR REPLACE FUNCTION vf_test.seed_speed_camera() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fam text := split_part(NEW.camera_id, ':', 1);
BEGIN
  IF fam = 'osm' THEN
    INSERT INTO public.osm_speed_cameras (osm_type, osm_id, enforces, geom, max_speed_kmh, direction, name, source_id, land, extract_date, first_seen_at, last_seen_at)
    VALUES ('node', substr(NEW.camera_id, 9)::bigint, coalesce(NEW.enforces, 'speed'), vf_test.point(NEW.location), NEW.max_speed_kmh, NEW.direction,
            NEW.name, 'osm_speed_cameras', NEW.land_name, coalesce(NEW.data_as_of, current_date), coalesce(NEW.recorded_at, now()), coalesce(NEW.last_seen_at, now()));
  ELSIF fam = 'se' THEN
    INSERT INTO public.speed_enforcement (id, source_feed, source_publisher, external_id, attribution_id, kind, enforces, geom, ags, municipality, street,
                                          location_text, direction, max_speed_kmh, max_speed_hgv_kmh, valid_from, valid_to, data_as_of, first_seen_at, last_seen_at)
    VALUES (vf_test.after_colon(NEW.camera_id)::bigint, NEW.source_ref, 'Test', vf_test.after_colon(NEW.camera_id), NEW.source_ref,
            coalesce(NEW.camera_kind, 'fixed'), coalesce(NEW.enforces, 'speed'), CASE WHEN NEW.location IS NULL THEN NULL ELSE vf_test.point(NEW.location) END,
            NEW.admin_code, NEW.name, NEW.street, NEW.location_text, NEW.direction, NEW.max_speed_kmh, NEW.max_speed_hgv_kmh, NEW.valid_from, NEW.valid_to,
            NEW.data_as_of, coalesce(NEW.recorded_at, now()), coalesce(NEW.last_seen_at, now()));
  ELSIF fam = 'hh' THEN
    INSERT INTO public.hh_cameras (camera_id, location_label, note, geom, first_seen_at, last_seen_at, source_feed, source_publisher)
    VALUES (vf_test.after_colon(NEW.camera_id), coalesce(NEW.name, 'Kamera'), NEW.location_text, vf_test.point(NEW.location)::geometry,
            coalesce(NEW.recorded_at, now()), coalesce(NEW.last_seen_at, now()), 'hh_verkehrskameras', 'Hamburg');
  ELSE
    RAISE EXCEPTION 'vf_test contract seed: speed_camera id % names no family (osm:, se:, hh:)', NEW.camera_id USING ERRCODE = '22023';
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- The system of a sharing row is admitted under CC0 on first use.
CREATE OR REPLACE FUNCTION vf_test.ensure_gbfs_system(p_system text, p_feed text, p_name text DEFAULT NULL) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.gbfs_systems (system_id, publisher, name, license_id, admitted, licence_basis, licence_read_at, first_seen_at, last_seen_at,
                                   source_feed, source_publisher, status_seen_at, vehicles_seen_at)
  VALUES (p_system, p_feed, p_name, 'CC0-1.0', true, 'stated', now(), now(), now(), p_feed, p_feed, now(), now())
  ON CONFLICT (system_id) DO NOTHING;
END $$;

-- station_id is "<system>:<station>".
CREATE OR REPLACE FUNCTION vf_test.seed_sharing_station() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE feed text := coalesce(NEW.source_ref, 'gbfs_nextbike');
BEGIN
  PERFORM vf_test.ensure_gbfs_system(coalesce(NEW.system_id, split_part(NEW.station_id, ':', 1)), feed, NEW.system_name);
  INSERT INTO public.gbfs_stations (system_id, station_id, name, capacity, is_virtual, geom, first_seen_at, last_seen_at, source_feed, source_publisher)
  VALUES (split_part(NEW.station_id, ':', 1), vf_test.after_colon(NEW.station_id), NEW.name, NEW.capacity, NEW.is_virtual, vf_test.point(NEW.location),
          coalesce(NEW.recorded_at, now()), now(), feed, feed);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW) - 'status_seen_at');
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_sharing_station_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM vf_test.ensure_gbfs_system(split_part(NEW.station_id, ':', 1), coalesce(NEW.source_ref, 'gbfs_nextbike'));
  PERFORM vf_test.ensure_day('public.gbfs_station_status', NEW.observed_at);
  INSERT INTO public.gbfs_station_status (observed_at, system_id, station_id, vehicles_available, electric_available, docks_available,
                                          is_renting, is_returning, reported_at)
  VALUES (NEW.observed_at, split_part(NEW.station_id, ':', 1), vf_test.after_colon(NEW.station_id), NEW.vehicles_available, NEW.electric_available,
          NEW.docks_available, NEW.is_renting, NEW.is_returning, NEW.reported_at);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- vehicle_id is "<system>:<seq>".
CREATE OR REPLACE FUNCTION vf_test.seed_sharing_vehicle() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE feed text := coalesce(NEW.source_ref, 'gbfs_nextbike');
BEGIN
  PERFORM vf_test.ensure_gbfs_system(split_part(NEW.vehicle_id, ':', 1), feed, NEW.system_name);
  INSERT INTO public.gbfs_vehicles (system_id, seq, form_factor, propulsion, range_m, geom, observed_at, source_feed, source_publisher)
  VALUES (split_part(NEW.vehicle_id, ':', 1), vf_test.after_colon(NEW.vehicle_id)::int, NEW.form_factor, NEW.propulsion, NEW.range_m,
          vf_test.point(NEW.location), coalesce(NEW.observed_at, now()), feed, feed);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW) - 'vehicles_seen_at');
  RETURN NEW;
END $$;

-- poi_id and address_id are "osm:<type><osm id>".
CREATE OR REPLACE FUNCTION vf_test.seed_poi() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text := substring(NEW.poi_id from '^osm:([a-z]+)');
BEGIN
  INSERT INTO public.osm_pois (osm_type, osm_id, name, name_norm, category, category_value, brand, operator, street, house_number, postcode,
                               place, place_norm, geom, source_id, extract_date, first_seen_at, last_seen_at, land, access)
  VALUES (t, substring(NEW.poi_id from '[0-9]+$')::bigint, NEW.name, coalesce(NEW.name_fold, vf_test.name_fold(NEW.name)), NEW.category, NEW.category_value,
          NEW.brand, NEW.operator_name, NEW.street, NEW.house_number, NEW.postcode, NEW.place_name, NEW.place_fold, vf_test.point(NEW.location), 'osm',
          coalesce(NEW.data_as_of, current_date), coalesce(NEW.recorded_at, now()), now(), NEW.land_name, NEW.access);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION vf_test.seed_address() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text := substring(NEW.address_id from '^osm:([a-z]+)');
BEGIN
  INSERT INTO public.osm_addresses (osm_type, osm_id, street, house_number, postcode, place, street_norm, house_number_norm, place_norm, geom,
                                    source_id, extract_date, first_seen_at, last_seen_at, land)
  VALUES (t, substring(NEW.address_id from '[0-9]+$')::bigint, NEW.street, NEW.house_number, NEW.postcode, NEW.place_name,
          coalesce(NEW.street_fold, (SELECT string_agg(CASE WHEN w = 'str' THEN 'strasse' ELSE regexp_replace(w, 'str$', 'strasse') END, '' ORDER BY n)
                                      FROM unnest(string_to_array(vf_test.name_fold(NEW.street), ' ')) WITH ORDINALITY AS x(w, n))),
          coalesce(NEW.house_number_fold, regexp_replace(lower(NEW.house_number), '\s+', '', 'g')), coalesce(NEW.place_fold, vf_test.name_fold(NEW.place_name)),
          vf_test.point(NEW.location), 'osm', coalesce(NEW.data_as_of, current_date), coalesce(NEW.recorded_at, now()), now(), NEW.land_name);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- alert_id is "<feed>:<alert id>"; the source is DELFI's GTFS-RT alerts, or its SIRI SX
-- alerts when primary_source is false; seen_at is the last delivery.
CREATE OR REPLACE FUNCTION vf_test.seed_service_alert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE feed text := coalesce(NEW.source_ref, split_part(NEW.alert_id, ':', 1));
BEGIN
  INSERT INTO public.delfi_service_alerts (source_id, source_feed, alert_id, header, description, cause, effect, url, active_from, active_to,
                                           active_periods, route_ids, line_names, stop_ids, kreise, lands, placed_by, dedupe_key, message_ref, content_hash,
                                           confidentiality, first_seen_at, last_seen_at, provider_updated_at)
  VALUES (CASE WHEN coalesce(NEW.primary_source, true) THEN 'delfi_rt_sa' ELSE 'delfi_siri_sx' END, feed, vf_test.after_colon(NEW.alert_id), NEW.header, NEW.description, NEW.cause, NEW.effect, NEW.url, NEW.active_from,
          NEW.active_to, coalesce(NEW.active_periods, '[]'), coalesce(NEW.route_ids, '{}'), coalesce(NEW.line_names, '{}'), coalesce(NEW.stop_ids, '{}'),
          coalesce(NEW.district_codes, '{}'), coalesce(NEW.state_codes, '{}'),
          coalesce(NEW.placed_by, 'network'), coalesce(NEW.dedupe_key, NEW.alert_id), NEW.message_ref, md5(NEW.alert_id), NEW.confidentiality,
          coalesce(NEW.recorded_at, now()), coalesce(NEW.seen_at, now()), NEW.changed_at);
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- bay_id is "<city>:<external id>"; a stated state is a sensor tick.
CREATE OR REPLACE FUNCTION vf_test.seed_street_bay() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE feed text := coalesce(NEW.source_ref, 'dortmund_parksensoren_bays');
BEGIN
  INSERT INTO public.street_bays (bay_id, external_id, kind, publisher_kind, street, house_number, postcode, city, charge_point_operator, geom,
                                  last_message_at, first_seen_at, last_seen_at, source_feed, source_publisher, spaces, reserved_when)
  VALUES (NEW.bay_id, vf_test.after_colon(NEW.bay_id), coalesce(NEW.bay_kind, 'disabled'), NEW.publisher_kind, NEW.street, NEW.house_number,
          NEW.postcode, NEW.city, NEW.charge_point_operator, vf_test.point(NEW.location), NEW.last_message_at, coalesce(NEW.recorded_at, now()), now(),
          feed, 'Test', NEW.spaces, NEW.reserved_when);
  IF NEW.state IS NOT NULL THEN
    INSERT INTO public.street_bay_state (bay_id, state, message_at, last_seen_at, source_feed, source_publisher)
    VALUES (NEW.bay_id, NEW.state, coalesce(NEW.state_at, now()), now(), 'dortmund_parksensoren', 'Test');
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- zone_key is "<feed>/<zone id>"; the feed's check is its source_freshness row.
CREATE OR REPLACE FUNCTION vf_test.seed_low_emission_zone() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE feed text := coalesce(NEW.source_ref, split_part(NEW.zone_key, '/', 1));
BEGIN
  INSERT INTO public.lez_zones (source_feed, source_publisher, zone_id, role, name, props, geom, content_hash, first_seen_at, last_seen_at,
                                provider_updated_at, place_name)
  VALUES (feed, coalesce(NEW.publisher_name, 'Test'), coalesce(NEW.zone_id, substr(NEW.zone_key, length(feed) + 2)), coalesce(NEW.zone_role, 'zone'),
          NEW.name, '{}', NEW.geometry, md5(NEW.zone_key), coalesce(NEW.recorded_at, now()), coalesce(NEW.recorded_at, now()), NEW.changed_at, NEW.city);
  -- A row that names its feed's scope plants the feed's public.lez_feed_places
  -- row (a feed the migration names keeps its own).
  IF NEW.source_scope IS NOT NULL THEN
    INSERT INTO public.lez_feed_places (feed_key, scope, place_code, city)
    VALUES (feed, NEW.source_scope, NEW.source_place_code, NEW.source_city) ON CONFLICT (feed_key) DO NOTHING;
  END IF;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- One entry of the curated zone list: the migration's entries stay; a seeded
-- entry is added beside them.
CREATE OR REPLACE FUNCTION vf_test.seed_low_emission_zone_entry() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO public.lez_list_entries (entry_key, name, aliases, place_codes, plaque, covered, lifted_on, reviewed_on, source_url, source_name)
  VALUES (NEW.entry_key, NEW.name, coalesce(NEW.aliases, '{}'), NEW.place_codes, NEW.plaque, coalesce(NEW.covered, true), NEW.lifted_on,
          coalesce(NEW.reviewed_on, current_date), coalesce(NEW.source_url, 'https://example.org/zones'), coalesce(NEW.source_name, 'Test'));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- One departure of a fetched DB Timetables board, as viafrei-api appends it;
-- the source, attribution and licence are the table's own defaults.
CREATE OR REPLACE FUNCTION vf_test.seed_train_board() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM vf_test.ensure_day('domains.train_board_departures', NEW.fetched_at);
  INSERT INTO domains.train_board_departures (eva, observed_at, planned_at, changed_at, platform, changed_platform, category, train_number, line,
                                              destination, cancelled, recorded_at)
  VALUES (NEW.station_eva, NEW.fetched_at, NEW.planned_departure_at, NEW.changed_departure_at, NEW.platform, NEW.changed_platform, NEW.category,
          NEW.train_number, NEW.line, NEW.destination, coalesce(NEW.cancelled, false), coalesce(NEW.recorded_at, greatest(clock_timestamp(), NEW.fetched_at)));
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- rule_id is "long:<n>" (the long-lorry network) or "city:<n>" (a city's
-- restriction or priority route); the id itself is the table's own.
CREATE OR REPLACE FUNCTION vf_test.seed_lorry_rule() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n bigint;
BEGIN
  IF NEW.rule_id LIKE 'long:%' THEN
    INSERT INTO public.lorry_long_network (source_feed, feature_key, layer, road, road_raw, direction, network_state, version_date, geom, first_seen_at, source_publisher)
    VALUES (coalesce(NEW.source_ref, 'lorry_bast_langlkw'), NEW.rule_id, 'long_lorry', NEW.road_ref, NEW.road_ref, NEW.direction, NEW.network_state,
            NEW.version_date, NEW.geometry::geography, coalesce(NEW.recorded_at, now()), 'Test')
    RETURNING id INTO n;
  ELSE
    INSERT INTO public.lorry_city (source_feed, feature_key, layer, road, road_raw, street, kind, sign, value, unit, exceptions, times, days, category,
                                   version_date, geom, first_seen_at, source_publisher, published_text, direction)
    VALUES (coalesce(NEW.source_ref, 'lorry_koeln_beschraenkungen'), NEW.rule_id, coalesce(NEW.rule_layer, 'restriction'), NEW.road_ref, NEW.road_ref,
            NEW.street, NEW.restriction_kind, NEW.sign, NEW.value, NEW.unit, coalesce(NEW.exceptions, '{}'), NEW.times, NEW.days, NEW.category,
            NEW.version_date, NEW.geometry::geography, coalesce(NEW.recorded_at, now()), 'Test', NEW.published_text, NEW.direction)
    RETURNING id INTO n;
  END IF;
  NEW.rule_id := split_part(NEW.rule_id, ':', 1) || ':' || n;
  PERFORM vf_test.seed_check(TG_TABLE_NAME, to_jsonb(NEW));
  RETURN NEW;
END $$;

-- ------------------------------------------------- the staging tables

CREATE SCHEMA IF NOT EXISTS vf_seed;
REVOKE ALL ON SCHEMA vf_seed FROM PUBLIC;

CREATE TABLE IF NOT EXISTS vf_seed.address (address_id text, street text, street_fold text, house_number text, house_number_fold text, postcode text, place_name text, place_fold text, land_name text, location geometry(Point,4326), data_as_of date, source_ref text, recorded_at timestamp with time zone, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.admin_region (region_code text, region_level text, name text, parent_code text, boundary geometry(MultiPolygon,4326), source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.call_observation (journey_id text, call_order integer, observed_at timestamp with time zone, expected_arrival_at timestamp with time zone, expected_departure_at timestamp with time zone, call_status text, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.charge_point (point_id text, site_id text, evse_code text, current_kind text, power_max_kw numeric(7,2), source_ref text, source_refs text[], display_class text, audience text, confidentiality text, connectors text[], price_per_kwh numeric(6,3), price_adhoc_zero boolean, price_updated_at timestamp with time zone, opening_hours_kind text, payment_card boolean, free_of_charge boolean, parking_access text);
CREATE TABLE IF NOT EXISTS vf_seed.charge_point_state_observation (point_id text, observed_at timestamp with time zone, availability text, source_ref text, display_class text, audience text, confidentiality text);
CREATE TABLE IF NOT EXISTS vf_seed.charging_site (site_id text, name text, operator_name text, access text, location geometry(Point,4326), source_ref text, origin_key text, recorded_at timestamp with time zone, changed_at timestamp with time zone, withdrawn_at timestamp with time zone, source_refs text[], display_class text, audience text, confidentiality text, access_private boolean, operator_is_person boolean);
CREATE TABLE IF NOT EXISTS vf_seed.contract_info (contract_version text, published_at timestamp with time zone);
CREATE TABLE IF NOT EXISTS vf_seed.departure (stop_place_id text, stop_point_id text, journey_id text, call_order integer, line_id text, line_name text, transport_mode text, route_type integer, train_number text, train_category text, destination_label text, planned_arrival_at timestamp with time zone, planned_departure_at timestamp with time zone, planned_platform text, platform text, platform_changed boolean, boarding boolean, terminates boolean, realtime boolean, realtime_source text, cancelled boolean, delay_s integer, delay_min integer, expected_arrival_at timestamp with time zone, expected_departure_at timestamp with time zone, no_forecast boolean, observed_at timestamp with time zone, source_ref text, source_refs text[], display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.fuel_price_observation (station_id text, product_code text, observed_at timestamp with time zone, price numeric(6,3), currency character(3), source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.fuel_product (product_code text, label text, price_unit text);
CREATE TABLE IF NOT EXISTS vf_seed.fuel_station (station_id text, name text, brand text, region_code text, location geometry(Point,4326), open_state text, open_state_at timestamp with time zone, source_ref text, origin_key text, recorded_at timestamp with time zone, changed_at timestamp with time zone, withdrawn_at timestamp with time zone, display_class text, audience text, street text, house_number text, postcode text, city text, opening_times jsonb, whole_day boolean);
CREATE TABLE IF NOT EXISTS vf_seed.hydrogen_station (station_id text, name text, operator_name text, brand text, location geometry(Point,4326), address jsonb, refill jsonb, opening jsonb, provider_updated_at timestamp with time zone, last_seen_at timestamp with time zone, source_ref text, recorded_at timestamp with time zone, withdrawn_at timestamp with time zone, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.journey (journey_id text, line_id text, service_date date, destination_label text, extra boolean, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.junction (junction_id text, road_ref text, number text, name text, junction_kind text, chainage_km numeric(6,1), location geometry(Point,4326), state_code text, source_ref text, display_class text, audience text, placement_source_ref text);
CREATE TABLE IF NOT EXISTS vf_seed.licence (licence_code text, licence_name text, licence_url text, licence_category text, share_alike boolean, attribution_required boolean);
CREATE TABLE IF NOT EXISTS vf_seed.lorry_rule (rule_id text, rule_layer text, restriction_kind text, sign text, value numeric, unit text, published_text text, road_ref text, street text, municipality text, direction text, exceptions text[], times text, days text, category text, network_state text, version_date date, geometry geometry, recorded_at timestamp with time zone, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.low_emission_zone (zone_key text, zone_id text, zone_role text, name text, city text, publisher_name text, geometry geometry(Geometry,4326), geometry_m geometry(Geometry,25832), same_zone_keys text[], checked_at timestamp with time zone, cadence_s integer, paused boolean, changed_at timestamp with time zone, recorded_at timestamp with time zone, source_scope text, source_place_code text, source_city text, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.low_emission_zone_entry (entry_key text, name text, aliases text[], place_codes text[], plaque text, covered boolean, lifted_on date, reviewed_on date, source_url text, source_name text, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.message_coverage (state_code text, source_ref text, coverage_role text, current_only boolean, measured_on date, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.occupancy_observation (facility_id text, observed_at timestamp with time zone, free_spaces integer, total_spaces integer, occupancy_status text, source_ref text, display_class text, audience text, confidentiality text, terms_gate text, hold_reason text);
CREATE TABLE IF NOT EXISTS vf_seed.parking_facility (facility_id text, name text, parking_kind text, operator_name text, road_ref text, direction text, location geometry(Point,4326), acceptance_required boolean, source_ref text, origin_key text, recorded_at timestamp with time zone, changed_at timestamp with time zone, withdrawn_at timestamp with time zone, source_refs text[], display_class text, audience text, confidentiality text, terms_gate text, hold_reason text, stored_kind text, amenities text[], amenities_absent text[]);
CREATE TABLE IF NOT EXISTS vf_seed.place (place_id text, place_kind text, name text, location geometry(Point,4326), region_code text, official_key text, prominence integer, source_ref text, origin_key text, published_at timestamp with time zone, recorded_at timestamp with time zone, changed_at timestamp with time zone, withdrawn_at timestamp with time zone, source_refs text[], display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.place_alias (place_id text, label text, lang text, name_kind text, search_fold text, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.poi (poi_id text, name text, name_fold text, category text, category_value text, brand text, operator_name text, street text, house_number text, postcode text, place_name text, place_fold text, land_name text, access text, location geometry(Point,4326), data_as_of date, source_ref text, recorded_at timestamp with time zone, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.publisher (publisher_id text, display_name text, website text);
CREATE TABLE IF NOT EXISTS vf_seed.road (road_ref text, road_class text, name text, length_km numeric(6,1), geometry geometry(MultiLineString,4326), states text[], source_ref text, display_class text, audience text, source_refs text[]);
CREATE TABLE IF NOT EXISTS vf_seed.rule_statement (statement_id text, topic_code text, "position" integer, body text, legal_basis text, body_en text, legal_basis_url text, legal_basis_note text, legal_basis_note_en text, to_verify text, to_verify_en text, reviewed_on date, headline boolean, source_ref text, source_refs text[], display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.rule_topic (topic_code text, title text, reviewed_on date, title_en text, source_ref text, source_refs text[], display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.scheduled_call (journey_id text, call_order integer, stop_point_id text, planned_arrival_at timestamp with time zone, planned_departure_at timestamp with time zone, planned_platform text, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.service_alert (alert_id text, header text, description text, cause text, effect text, url text, active_from timestamp with time zone, active_to timestamp with time zone, active_periods jsonb, route_ids text[], line_names text[], stop_ids text[], district_codes text[], state_codes text[], placed_by text, dedupe_key text, source_ref text, recorded_at timestamp with time zone, changed_at timestamp with time zone, seen_at timestamp with time zone, message_ref text, primary_source boolean, checked_at timestamp with time zone, cadence_s integer, paused boolean, display_class text, audience text, confidentiality text);
CREATE TABLE IF NOT EXISTS vf_seed.sharing_station (station_id text, system_id text, system_name text, name text, capacity integer, is_virtual boolean, location geometry(Point,4326), status_seen_at timestamp with time zone, source_ref text, recorded_at timestamp with time zone, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.sharing_station_status (station_id text, observed_at timestamp with time zone, vehicles_available smallint, electric_available smallint, docks_available smallint, is_renting boolean, is_returning boolean, reported_at timestamp with time zone, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.sharing_vehicle (vehicle_id text, system_id text, system_name text, form_factor text, propulsion text, range_m integer, location geometry(Point,4326), observed_at timestamp with time zone, vehicles_seen_at timestamp with time zone, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.source (source_ref text, domain text, publisher_id text, licence_code text, attribution_text text, changes_statement text, terms_url text, share_alike_offer_url text, acceptance_required boolean, use_classes text[], freshness_target_s integer, stale_after_s integer, review_signed_on date, register_version date, display_class text, audience text, terms_gate text, hold_reason text, origin_restriction text);
CREATE TABLE IF NOT EXISTS vf_seed.source_freshness (source_ref text, data_as_of timestamp with time zone, freshness_version text, confirmed_at timestamp with time zone, confirm_interval_s integer, updates_paused boolean);
CREATE TABLE IF NOT EXISTS vf_seed.source_note (source_ref text, note_kind text, lang text, body text);
CREATE TABLE IF NOT EXISTS vf_seed.speed_camera (camera_id text, camera_kind text, enforces text, location geometry(Point,4326), max_speed_kmh integer, max_speed_hgv_kmh integer, direction text, name text, street text, location_text text, admin_code text, land_name text, data_as_of date, valid_from date, valid_to date, source_ref text, recorded_at timestamp with time zone, last_seen_at timestamp with time zone, display_class text, audience text, origin_restriction text);
CREATE TABLE IF NOT EXISTS vf_seed.station_disruption (station_ifopt text, window_start timestamp with time zone, aimed_minute timestamp with time zone, line_key text, departures integer, cancelled integer, late integer, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.station_facility (facility_id text, stop_place_id text, facility_kind text, position_text text, facility_status text, status_observed_at timestamp with time zone, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.stop_place (stop_place_id text, name text, location geometry(Point,4326), national_stop_code text, region_code text, modes text[], search_fold text, source_ref text, origin_key text, recorded_at timestamp with time zone, changed_at timestamp with time zone, withdrawn_at timestamp with time zone, source_refs text[], display_class text, audience text, station_category smallint, municipality_name text, eva_no text, ifopt text, placement_source_ref text, eva_numbers text[]);
CREATE TABLE IF NOT EXISTS vf_seed.stop_point (stop_point_id text, stop_place_id text, platform_label text, source_ref text, display_class text, audience text, name text);
CREATE TABLE IF NOT EXISTS vf_seed.street_bay (bay_id text, bay_kind text, publisher_kind text, street text, house_number text, postcode text, city text, charge_point_operator text, spaces integer, reserved_when text, location geometry(Point,4326), state text, state_at timestamp with time zone, last_message_at timestamp with time zone, source_ref text, recorded_at timestamp with time zone, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.terms (terms_gate text, terms_id text, terms_version text, effective_on date, lang text, binding boolean, heading text, body text, terms_url text, provider_terms_url text, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.traffic_event (event_id text, event_kind text, severity text, road_ref text, road_class text, direction text, direction_label text, location geometry(Point,4326), valid_from timestamp with time zone, valid_to timestamp with time zone, lanes_closed integer, lanes_total integer, delay_min integer, affected_length_m integer, headline text, description text, text_lang text, source_ref text, origin_key text, published_at timestamp with time zone, recorded_at timestamp with time zone, changed_at timestamp with time zone, withdrawn_at timestamp with time zone, record_class text, subtitle text, place_text text, is_blocked boolean, traffic_type text, delay_s integer, source_refs text[], display_class text, audience text, confidentiality text, origin_restriction text, state_code text, placement_source_ref text);
CREATE TABLE IF NOT EXISTS vf_seed.traffic_event_observation (event_id text, observed_at timestamp with time zone, version_no integer, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.train_board (station_eva text, fetched_at timestamp with time zone, planned_departure_at timestamp with time zone, changed_departure_at timestamp with time zone, platform text, changed_platform text, category text, train_number text, line text, destination text, cancelled boolean, attribution text, licence text, source_ref text, recorded_at timestamp with time zone, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.transit_line (line_id text, line_name text, transport_mode text, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.transit_punctuality (area_level text, area_code text, area_name text, trips_observed integer, trips_late integer, trips_skipped_stop integer, trips_untimed integer, median_worst_delay_s integer, stops_seen integer, observed_at timestamp with time zone, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.transit_trip_delay (trip_ref text, area_code text, worst_delay_s integer, observed_at timestamp with time zone, source_ref text, display_class text, audience text);
CREATE TABLE IF NOT EXISTS vf_seed.weather_warning (warning_id text, hazard text, warning_level integer, level_label text, message_status text, issuer_name text, headline text, text_lang text, valid_from timestamp with time zone, valid_to timestamp with time zone, published_at timestamp with time zone, region_code text, source_ref text, recorded_at timestamp with time zone, withdrawn_at timestamp with time zone, display_class text, audience text, warn_cell_id text, area_name text, area geometry(MultiPolygon,4326), event text, identifier text, seen_at timestamp with time zone);

-- ------------------------------------------------------------ the triggers

DO $$
DECLARE v record;
BEGIN
  FOR v IN SELECT c.relname FROM pg_class c WHERE c.relnamespace = 'vf_seed'::regnamespace AND c.relkind = 'r' LOOP
    EXECUTE format('DROP TRIGGER IF EXISTS vf_test_seed ON vf_seed.%I', v.relname);
    IF v.relname IN ('contract_info', 'transit_punctuality', 'transit_trip_delay', 'station_disruption') THEN
      EXECUTE format('CREATE TRIGGER vf_test_seed AFTER INSERT ON vf_seed.%I FOR EACH ROW EXECUTE FUNCTION vf_test.seed_refused()', v.relname);
    ELSIF to_regprocedure(format('vf_test.seed_%s()', v.relname)) IS NOT NULL THEN
      EXECUTE format('CREATE TRIGGER vf_test_seed AFTER INSERT ON vf_seed.%I FOR EACH ROW EXECUTE FUNCTION vf_test.seed_%s()', v.relname, v.relname);
    ELSE
      RAISE EXCEPTION 'vf_test seed: vf_seed.% has no seed function', v.relname USING ERRCODE = '0A000';
    END IF;
  END LOOP;
END $$;
