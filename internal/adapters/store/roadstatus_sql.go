package store

// Today's statements of the platform's road status by road number, as its
// tool runs them: the road operator's live sweep, the closures and works it
// lists, the plan, message and works sources' entries in the window, when
// each source was last checked, and the road operator's switched
// strategies. Only the parameters' casts are written out; the live road
// events view is spelled as its definition over the base table, and the
// register's rule on the sources is applied by the families the key may
// read (the platform's open sources).

// planSuspended is the platform's rule of a message whose publisher marks it
// suspended, read from its payload.
const planSuspended = `road_event_suspended(e.payload)`

// strategyPayloadColumn and strategyPayloadOnce carry a plan entry's payload
// to the fields the statement derives from it, once per entry; the payload
// is never answered.
const (
	strategyPayloadColumn = `e.referent_from, e.referent_to, e.payload, e.overall_start`
	strategyPayloadOnce   = `select case when jsonb_typeof(r.payload) = 'object' then r.payload || '{}'::jsonb else r.payload end as p`
)

var (
	roadStatusTicks = named("api_road_status_ticks", `
  select road, max(observed_at) as t
  from autobahn_events
  where road = any($1::text[]) and observed_at > $2::timestamptz - interval '30 minutes' and observed_at <= $2::timestamptz
  group by road`, "text[]", "timestamptz")
	roadStatusAutobahn = named("api_road_status_autobahn", `
  with m as (
    select distinct on (e.ext_id)
           e.source as feed_key, e.road, e.kind, e.ext_id, e.title, e.subtitle, e.extent,
           ST_Y(e.geom::geometry) as lat, ST_X(e.geom::geometry) as lon,
           rr.raw->>'isBlocked' as is_blocked,
           rr.raw->>'startTimestamp' as start_ts,
           rr.raw->'description' as description,
           e.observed_at,
           case when $6::float8 is null then null
                else ST_Distance(e.geom, ST_SetSRID(ST_MakePoint($6::float8, $7::float8), 4326)::geography)::int end as distance_m,
           k.site
    from (
`+motorwaySweepRows("$10", "$11::boolean")+`
    cross join lateral (
    select coalesce(
             case when rr.raw->>'startTimestamp' is not null and pg_input_is_valid(rr.raw->>'startTimestamp', 'timestamptz')
                  then (rr.raw->>'startTimestamp')::timestamptz end,
             (select case when ph is not null
                               and pg_input_is_valid(format('20%s-%s-%s %s:%s', ph[3], ph[2], ph[1], ph[4], ph[5]), 'timestamp')
                          then format('20%s-%s-%s %s:%s', ph[3], ph[2], ph[1], ph[4], ph[5])::timestamp at time zone 'Europe/Berlin' end
                from regexp_match(
                       case when jsonb_typeof(rr.raw->'description') = 'array'
                            then concat_ws(' ', rr.raw->'description'->>0, rr.raw->'description'->>1, rr.raw->'description'->>2, rr.raw->'description'->>3) end,
                       '(?:gültig:|Beginn:)[[:space:]]*([0-9]{1,2})[.]([0-9]{1,2})[.](?:20)?([0-9]{2})[[:space:]]+(?:(?:um|von)[[:space:]]+)?([0-9]{1,2}):([0-9]{2})') as ph)
           ) as starts_at,
           coalesce(rr.raw->>'future', '') = 'true' as future
    offset 0) s
    cross join lateral (select jsonb_build_array(
      e.road, e.kind, regexp_replace(e.ext_id, '[.]de[0-9]+$', ''),
      e.title, e.subtitle,
      case when coalesce(btrim(e.title), '') = '' and coalesce(btrim(e.subtitle), '') = '' then e.extent end,
      e.abnormal_traffic_type, e.delay_s,
      rr.raw->>'isBlocked', rr.raw->>'averageSpeed', s.starts_at, s.future,
      case when rr.raw->>'startTimestamp' is null then null
           when pg_input_is_valid(rr.raw->>'startTimestamp', 'timestamptz') then ((rr.raw->>'startTimestamp')::timestamptz)::text
           else 'unreadable:' || (rr.raw->>'startTimestamp') end,
      (with d as (
              select a.o, a.x, case when jsonb_typeof(a.x) = 'string' then regexp_replace((a.x #>> '{}'), '^[\t\n\v\f\r\u0020\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+|[\t\n\v\f\r\u0020\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+$', '', 'g') else '' end as t
              from jsonb_array_elements(case when jsonb_typeof(rr.raw->'description') = 'array' then rr.raw->'description' else '[]'::jsonb end)
                   with ordinality as a(x, o)),
            maybe_dropped as (select o from d where t <> '' and t ~* '^Beginn:|seit[\t\n\v\f\r\u0020\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]+[0-9]'),
            printed as (
              select d.o from d
              where d.t <> '' and d.o not in (select o from maybe_dropped)
                and (d.t !~ ':$'
                     or coalesce((select n.t <> '' from d as n
                                   where n.o > d.o and n.o not in (select o from maybe_dropped)
                                   order by n.o limit 1), false)))
       select coalesce(jsonb_agg(d.x order by d.o), '[]'::jsonb)
       from d
       where d.o < coalesce((select min(b.o) from d as b
                              where b.t = '' and b.o > (select o from printed order by o offset 1 limit 1)), 2147483647))) as site offset 0) k
    where e.road = any($1::text[])
      and e.kind = any($3::text[])
      and e.source = any($4::text[])
      and ($6::float8 is null
           or (e.geom is not null
               and ST_DWithin(e.geom, ST_SetSRID(ST_MakePoint($6::float8, $7::float8), 4326)::geography, $8::float8)))
      and (case when $9::timestamptz <= $10::timestamptz then not (s.future or coalesce(s.starts_at > $10::timestamptz, false))
               when s.starts_at is not null then s.starts_at <= $9::timestamptz
               else true end
    and not coalesce(not autobahn_validity_overlaps(rr.raw->'description', $10::timestamptz, $9::timestamptz), false))
    order by e.ext_id, array_position($1::text[], e.road)
  ),
  f as (
    select m.feed_key, m.road, m.kind, m.ext_id, m.title, m.subtitle, m.extent, m.lat, m.lon, m.is_blocked, m.start_ts, m.description, m.observed_at, m.distance_m, m.site,
           row_number() over (partition by m.site order by m.distance_m nulls last, (m.lat is null), m.ext_id) as rn,
           (count(*) over (partition by m.site))::int as segments
    from m
  )
  select feed_key, road, kind, ext_id, title, subtitle, extent, lat, lon, is_blocked, start_ts, description, observed_at, distance_m, segments, total_rows, total_closed, near_rows, near_closed, feed_keys, class_rn from (
  select m.feed_key, m.road, m.kind, m.ext_id, m.title, m.subtitle, m.extent,
         m.lat, m.lon, m.is_blocked, m.start_ts, m.description, m.observed_at, m.distance_m, m.segments,
         (count(*) over ())::int as total_rows,
         (count(*) filter (
            where m.kind = 'closure' or coalesce(m.is_blocked, '') = 'true'
          ) over ())::int as total_closed,
         (count(*) filter (where m.distance_m <= 3000) over ())::int as near_rows,
         (count(*) filter (
            where m.distance_m <= 3000 and (m.kind = 'closure' or coalesce(m.is_blocked, '') = 'true')
          ) over ())::int as near_closed,
         (select array_agg(distinct feed_key) from m) as feed_keys,
         row_number() over (
           partition by (m.kind = 'closure' or coalesce(m.is_blocked, '') = 'true')
           order by case when $6::float8 is null then array_position($1::text[], m.road) end,
                    case when $6::float8 is not null then m.distance_m end,
                    case m.kind when 'closure' then 0 else 1 end,
                    m.distance_m nulls last, m.title, m.ext_id) as class_rn
  from f as m
  where m.rn = 1
  ) as m
  where m.class_rn <= $5::int
  order by case when $6::float8 is null then array_position($1::text[], m.road) end,
           case when $6::float8 is not null then m.distance_m end,
           case m.kind when 'closure' then 0 else 1 end,
           m.distance_m nulls last,
           m.title, m.ext_id`, "text[]", "timestamptz[]", "text[]", "text[]", "int", "float8", "float8", "float8", "timestamptz", "timestamptz", "boolean")
	// The answer rows' texts are read from each entry's payload (the plan
	// sources' DATEX II publications: up to 4 MB as text, 37 kB stored): the
	// payload is decompressed once per entry (dp, kept apart by offset 0;
	// #> '{}' is the whole value), where each of the eight paths over it
	// decompressed it again (#290: A3 324 -> 160 ms on prod).
	roadStatusPlanned = named("api_road_status_planned", `
  with m0 as (
    select e.id, e.feed_key, e.road_number, e.event_type, e.publisher,
           e.referent_from, e.referent_to, e.distance_from_m, e.distance_to_m,
           e.overall_start, e.overall_end, e.last_seen_at, e.publication_id, e.source_id, e.feed_ts, e.valid_windows,
           (coalesce(e.event_type ~* 'closure|closed|blocked|sperrung', false) and (not e.feed_key = any($10::text[]) or (e.closure_ends_at is null or e.closure_ends_at > $2::timestamptz) and (e.closure_starts_at is null or e.closure_starts_at < $3::timestamptz))) as type_closes,
           (case when e.feed_key = any($10::text[]) and (e.closure_ends_at is null or e.closure_ends_at > $2::timestamptz) and (e.closure_starts_at is null or e.closure_starts_at < $3::timestamptz) then (regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(coalesce((select string_agg(v #>> '{}', ' ') from jsonb_path_query(e.payload, '$.situationRecord[*].generalPublicComment[*].comment.values.value."#text"') v), ''), '(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*sperrung', ' ', 'gi'), '(?<![A-Za-zÄÖÜäöüß])(?<!(?:und|oder|sowie)\s+(?:(?:der|die|das|den)\s+)?)(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*(?:\s*(?:und|oder|sowie)\s*(?:(?:der|die|das|den)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*)*(?:\s+(?:ist|sind|wird|werden|teilweise|zeitweise|jeweils|abschnittsweise|einseitig|kurzzeitig))*\s+gesperrt', ' ', 'gi'), '(?:voll)?sperrung(?:\s*/\s*[A-Za-zÄÖÜäöüß]+)?\s+(?:nur\s+)?(?:(?:des|der|von|einer|eines|zweier|für\s+den|für\s+die)\s+)?(?:(?:link|recht|mittler|äußer|inner|erst|zweit|dritt)(?:en|er|es|e)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*', ' ', 'gi'), '(?:^|(?<=[.;:!?\n]))\s*?(?:(?:der|die|das)\s+)?(?:(?:link|recht|mittler|äußer|inner|erst|zweit|dritt)(?:en|er|es|e)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*(?:\s*(?:,|und|oder|sowie)\s*(?:(?:der|die|das)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*)*(?:\s+(?:der|des)(?:\s+[A-Za-zÄÖÜäöüß]+){1,3})?\s+(?:ist|sind|wird|werden|bleibt|bleiben)\s(?:(?!(?<![A-Za-zÄÖÜäöüß])(?:ist|sind|wird|werden|bleibt|bleiben|(?:und|oder|sowie)\s+(?:der|die|das))(?![A-Za-zÄÖÜäöüß]))(?:[^.;,\n]|[0-9]\.|,(?!\s*(?:der|die|das)\s)))*?gesperrt', ' ', 'gi'), '(?:halb|ein)seitig[A-Za-zÄÖÜäöüß]*\s+(?:gesperrt|sperrung)', ' ', 'gi'), 'sperrung\s+(?:ist\s+|wurde\s+|wird\s+)?(?:aufgehoben|beendet)', ' ', 'gi'), '(?:nicht|kein[A-Za-zÄÖÜäöüß]*)\s+(?:mehr\s+)?(?:gesperrt|sperrung)', ' ', 'gi'), 'für\s+(?:(?:kraft)?fahrzeuge|lkw|lastkraftwagen|lastwagen)\s+(?:mit\s+(?:einem\s+)?(?:zul[äa]ssigen\s+)?gesamtgewicht\s+)?(?:über|ab|von)\s+[0-9]+(?:[,.][0-9]+)?\s*t(?![A-Za-zÄÖÜäöüß])\s+gesperrt', ' ', 'gi'), '(?:gesperrt|sperrung)\s+für\s+(?:(?:kraft)?fahrzeuge|lkw|lastkraftwagen|lastwagen)\s+(?:mit\s+(?:einem\s+)?(?:zul[äa]ssigen\s+)?gesamtgewicht\s+)?(?:über|ab|von)\s+[0-9]+(?:[,.][0-9]+)?\s*t(?![A-Za-zÄÖÜäöüß])', ' ', 'gi') ~* 'gesperrt|sperrung'
           or (jsonb_path_exists(e.payload, '$.situationRecord[*].roadOrCarriagewayOrLaneManagementType."#text" ? (@ == "roadClosed" || @ == "carriagewayClosures")')
               and not coalesce((select string_agg(v #>> '{}', ' ') from jsonb_path_query(e.payload, '$.situationRecord[*].generalPublicComment[*].comment.values.value."#text"') v), '') ~* 'gesperrt|sperrung')) else false end) as closes_text,
           case when e.feed_key = any($10::text[]) and e.closure_starts_at >= $3::timestamptz and (regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(coalesce((select string_agg(v #>> '{}', ' ') from jsonb_path_query(e.payload, '$.situationRecord[*].generalPublicComment[*].comment.values.value."#text"') v), ''), '(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*sperrung', ' ', 'gi'), '(?<![A-Za-zÄÖÜäöüß])(?<!(?:und|oder|sowie)\s+(?:(?:der|die|das|den)\s+)?)(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*(?:\s*(?:und|oder|sowie)\s*(?:(?:der|die|das|den)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*)*(?:\s+(?:ist|sind|wird|werden|teilweise|zeitweise|jeweils|abschnittsweise|einseitig|kurzzeitig))*\s+gesperrt', ' ', 'gi'), '(?:voll)?sperrung(?:\s*/\s*[A-Za-zÄÖÜäöüß]+)?\s+(?:nur\s+)?(?:(?:des|der|von|einer|eines|zweier|für\s+den|für\s+die)\s+)?(?:(?:link|recht|mittler|äußer|inner|erst|zweit|dritt)(?:en|er|es|e)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*', ' ', 'gi'), '(?:^|(?<=[.;:!?\n]))\s*?(?:(?:der|die|das)\s+)?(?:(?:link|recht|mittler|äußer|inner|erst|zweit|dritt)(?:en|er|es|e)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*(?:\s*(?:,|und|oder|sowie)\s*(?:(?:der|die|das)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*)*(?:\s+(?:der|des)(?:\s+[A-Za-zÄÖÜäöüß]+){1,3})?\s+(?:ist|sind|wird|werden|bleibt|bleiben)\s(?:(?!(?<![A-Za-zÄÖÜäöüß])(?:ist|sind|wird|werden|bleibt|bleiben|(?:und|oder|sowie)\s+(?:der|die|das))(?![A-Za-zÄÖÜäöüß]))(?:[^.;,\n]|[0-9]\.|,(?!\s*(?:der|die|das)\s)))*?gesperrt', ' ', 'gi'), '(?:halb|ein)seitig[A-Za-zÄÖÜäöüß]*\s+(?:gesperrt|sperrung)', ' ', 'gi'), 'sperrung\s+(?:ist\s+|wurde\s+|wird\s+)?(?:aufgehoben|beendet)', ' ', 'gi'), '(?:nicht|kein[A-Za-zÄÖÜäöüß]*)\s+(?:mehr\s+)?(?:gesperrt|sperrung)', ' ', 'gi'), 'für\s+(?:(?:kraft)?fahrzeuge|lkw|lastkraftwagen|lastwagen)\s+(?:mit\s+(?:einem\s+)?(?:zul[äa]ssigen\s+)?gesamtgewicht\s+)?(?:über|ab|von)\s+[0-9]+(?:[,.][0-9]+)?\s*t(?![A-Za-zÄÖÜäöüß])\s+gesperrt', ' ', 'gi'), '(?:gesperrt|sperrung)\s+für\s+(?:(?:kraft)?fahrzeuge|lkw|lastkraftwagen|lastwagen)\s+(?:mit\s+(?:einem\s+)?(?:zul[äa]ssigen\s+)?gesamtgewicht\s+)?(?:über|ab|von)\s+[0-9]+(?:[,.][0-9]+)?\s*t(?![A-Za-zÄÖÜäöüß])', ' ', 'gi') ~* 'gesperrt|sperrung'
           or (jsonb_path_exists(e.payload, '$.situationRecord[*].roadOrCarriagewayOrLaneManagementType."#text" ? (@ == "roadClosed" || @ == "carriagewayClosures")')
               and not coalesce((select string_agg(v #>> '{}', ' ') from jsonb_path_query(e.payload, '$.situationRecord[*].generalPublicComment[*].comment.values.value."#text"') v), '') ~* 'gesperrt|sperrung')) then e.closure_starts_at end as closure_ahead_from,
           case when e.feed_key = any($10::text[]) and e.closure_starts_at >= $3::timestamptz and e.closure_ends_at is not null and (regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(regexp_replace(coalesce((select string_agg(v #>> '{}', ' ') from jsonb_path_query(e.payload, '$.situationRecord[*].generalPublicComment[*].comment.values.value."#text"') v), ''), '(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*sperrung', ' ', 'gi'), '(?<![A-Za-zÄÖÜäöüß])(?<!(?:und|oder|sowie)\s+(?:(?:der|die|das|den)\s+)?)(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*(?:\s*(?:und|oder|sowie)\s*(?:(?:der|die|das|den)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*)*(?:\s+(?:ist|sind|wird|werden|teilweise|zeitweise|jeweils|abschnittsweise|einseitig|kurzzeitig))*\s+gesperrt', ' ', 'gi'), '(?:voll)?sperrung(?:\s*/\s*[A-Za-zÄÖÜäöüß]+)?\s+(?:nur\s+)?(?:(?:des|der|von|einer|eines|zweier|für\s+den|für\s+die)\s+)?(?:(?:link|recht|mittler|äußer|inner|erst|zweit|dritt)(?:en|er|es|e)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*', ' ', 'gi'), '(?:^|(?<=[.;:!?\n]))\s*?(?:(?:der|die|das)\s+)?(?:(?:link|recht|mittler|äußer|inner|erst|zweit|dritt)(?:en|er|es|e)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*(?:\s*(?:,|und|oder|sowie)\s*(?:(?:der|die|das)\s+)?(?:[A-Za-zÄÖÜäöüß]*(?:streifen|spur)|(?<![A-Za-zÄÖÜäöüß])(?:(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)-\s*(?:/\s*|und\s+|oder\s+)?)?(?:geh|rad|fuß|fuss|fußgänger|wander|reit|bürger)(?:weg|steig))[A-Za-zÄÖÜäöüß]*)*(?:\s+(?:der|des)(?:\s+[A-Za-zÄÖÜäöüß]+){1,3})?\s+(?:ist|sind|wird|werden|bleibt|bleiben)\s(?:(?!(?<![A-Za-zÄÖÜäöüß])(?:ist|sind|wird|werden|bleibt|bleiben|(?:und|oder|sowie)\s+(?:der|die|das))(?![A-Za-zÄÖÜäöüß]))(?:[^.;,\n]|[0-9]\.|,(?!\s*(?:der|die|das)\s)))*?gesperrt', ' ', 'gi'), '(?:halb|ein)seitig[A-Za-zÄÖÜäöüß]*\s+(?:gesperrt|sperrung)', ' ', 'gi'), 'sperrung\s+(?:ist\s+|wurde\s+|wird\s+)?(?:aufgehoben|beendet)', ' ', 'gi'), '(?:nicht|kein[A-Za-zÄÖÜäöüß]*)\s+(?:mehr\s+)?(?:gesperrt|sperrung)', ' ', 'gi'), 'für\s+(?:(?:kraft)?fahrzeuge|lkw|lastkraftwagen|lastwagen)\s+(?:mit\s+(?:einem\s+)?(?:zul[äa]ssigen\s+)?gesamtgewicht\s+)?(?:über|ab|von)\s+[0-9]+(?:[,.][0-9]+)?\s*t(?![A-Za-zÄÖÜäöüß])\s+gesperrt', ' ', 'gi'), '(?:gesperrt|sperrung)\s+für\s+(?:(?:kraft)?fahrzeuge|lkw|lastkraftwagen|lastwagen)\s+(?:mit\s+(?:einem\s+)?(?:zul[äa]ssigen\s+)?gesamtgewicht\s+)?(?:über|ab|von)\s+[0-9]+(?:[,.][0-9]+)?\s*t(?![A-Za-zÄÖÜäöüß])', ' ', 'gi') ~* 'gesperrt|sperrung'
           or (jsonb_path_exists(e.payload, '$.situationRecord[*].roadOrCarriagewayOrLaneManagementType."#text" ? (@ == "roadClosed" || @ == "carriagewayClosures")')
               and not coalesce((select string_agg(v #>> '{}', ' ') from jsonb_path_query(e.payload, '$.situationRecord[*].generalPublicComment[*].comment.values.value."#text"') v), '') ~* 'gesperrt|sperrung')) then e.closure_ends_at end as closure_ahead_to,
           (case when e.feed_key in ('sperrinfosys_sn') then e.payload ->> 'twinOf' when e.feed_key in ('viz_berlin', 'roadworks_mv_planned', 'ald_14d', 'roadworks_akd', 'roadworks_he_gesammelt', 'traffic_nrw', 'traffic_nrw_einzel', 'traffic_hamburg') then (select o.publication_id from road_event_twins l join road_events o on o.id = l.origin_id and o.deleted_at is null and o.closed_at is null and coalesce(o.confidentiality, 'noRestriction') = 'noRestriction' where l.twin_id = e.id) end) as twin_of,
           case when $6::float8 is null then null
                else ST_Distance(e.geom::geography, ST_SetSRID(ST_MakePoint($6::float8, $7::float8), 4326)::geography)::int end as distance_m
    
    from road_events e
    where e.deleted_at is null and e.closed_at is null
      and e.road_number = any($1::text[])
      and e.feed_key = any($4::text[])
      and coalesce(e.confidentiality, 'noRestriction') = 'noRestriction'
      and (e.overall_start is null or e.overall_start <= $3::timestamptz)
      and (e.overall_end is null or e.overall_end >= $2::timestamptz)
      and (e.valid_windows is null or e.valid_windows && tstzrange($2::timestamptz, $3::timestamptz, '[]'))
      and ($6::float8 is null
           or (e.geom is not null
               and ST_DWithin(e.geom::geography, ST_SetSRID(ST_MakePoint($6::float8, $7::float8), 4326)::geography, $8::float8)))
      and case when $6::float8 is null then true
               when e.geom is null then false
               when e.feed_key = any($11::text[]) then not jsonb_path_exists(e.payload, '$.situationRecord[*] ? (@.abnormalTrafficType[*]."#text" like_regex "^(stationaryTraffic|queuingTraffic|slowTraffic|heavyTraffic)$")') and not `+planSuspended+`
               else not jsonb_path_exists(e.payload, '$.situationRecord[*] ? (@.abnormalTrafficType[*]."#text" like_regex "^(stationaryTraffic|queuingTraffic|slowTraffic|heavyTraffic)$")') end
  ),
  m as (select id, feed_key, road_number, event_type, publisher, referent_from, referent_to, distance_from_m, distance_to_m, overall_start, overall_end, last_seen_at, publication_id, source_id, feed_ts, valid_windows, type_closes, closes_text, closure_ahead_from, closure_ahead_to, twin_of, distance_m from m0 where not (((m0.feed_key = 'traffic_berlin' and m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_lms_berlin')) or (m0.feed_key = 'viz_berlin' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_berlin') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_berlin')))) or (m0.feed_key = 'roadworks_mv_planned' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_mv') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_mv')))) or (m0.feed_key = 'ald_14d' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast')))) or (m0.feed_key = 'roadworks_akd' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast')))) or (m0.feed_key = 'sperrinfosys_sn' and m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_ald_sn')) or (m0.feed_key = 'roadworks_he_gesammelt' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_he') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_he')))) or (m0.feed_key = 'traffic_nrw' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events')))) or (m0.feed_key = 'traffic_nrw_einzel' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events')))) or (m0.feed_key = 'traffic_hamburg' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events'))))))),
  page as (
    select id, feed_key, road_number, event_type, publisher, referent_from, referent_to, distance_from_m, distance_to_m, overall_start, overall_end, last_seen_at, publication_id, source_id, feed_ts, valid_windows, type_closes, closes_text, closure_ahead_from, closure_ahead_to, twin_of, distance_m, total_rows, total_planned, total_current, total_closed, near_rows, near_closed, total_programme, feed_keys, current_feed_keys, class_rn from (
    select m.id, m.feed_key, m.road_number, m.event_type, m.publisher, m.referent_from, m.referent_to, m.distance_from_m, m.distance_to_m, m.overall_start, m.overall_end, m.last_seen_at, m.publication_id, m.source_id, m.feed_ts, m.valid_windows, m.type_closes, m.closes_text, m.closure_ahead_from, m.closure_ahead_to, m.twin_of, m.distance_m,
           (count(*) over ())::int as total_rows,
           (count(*) filter (
              where ((m.overall_start is not null and m.overall_start > $2::timestamptz) or m.feed_key = any(array['roadworks_akd']::text[]) or (m.valid_windows is not null and not (m.valid_windows @> $2::timestamptz)))
            ) over ())::int as total_planned,
           (count(*) filter (
              where not ((m.overall_start is not null and m.overall_start > $2::timestamptz) or m.feed_key = any(array['roadworks_akd']::text[]) or (m.valid_windows is not null and not (m.valid_windows @> $2::timestamptz)))
                and m.feed_key = any($12::text[])
                and coalesce(m.road_number = any($9::text[]), false)
                and not (m.type_closes or m.closes_text)
            ) over ())::int as total_current,
           (count(*) filter (
              where not ((m.overall_start is not null and m.overall_start > $2::timestamptz) or m.feed_key = any(array['roadworks_akd']::text[]) or (m.valid_windows is not null and not (m.valid_windows @> $2::timestamptz)))
                and (m.type_closes or m.closes_text)
            ) over ())::int as total_closed,
           (count(*) filter (where m.distance_m <= 3000) over ())::int as near_rows,
           (count(*) filter (
              where m.distance_m <= 3000
                and not ((m.overall_start is not null and m.overall_start > $2::timestamptz) or m.feed_key = any(array['roadworks_akd']::text[]) or (m.valid_windows is not null and not (m.valid_windows @> $2::timestamptz)))
                and (m.type_closes or m.closes_text)
            ) over ())::int as near_closed,
           (count(*) filter (
              where m.feed_key = any($13::text[])
                and not ((m.overall_start is not null and m.overall_start > $2::timestamptz) or m.feed_key = any(array['roadworks_akd']::text[]) or (m.valid_windows is not null and not (m.valid_windows @> $2::timestamptz)))
                and not (m.type_closes or m.closes_text)
            ) over ())::int as total_programme,
           (select array_agg(distinct feed_key) from m) as feed_keys,
           (select array_agg(distinct mc.feed_key order by mc.feed_key) from m mc
             where not ((mc.overall_start is not null and mc.overall_start > $2::timestamptz) or mc.feed_key = any(array['roadworks_akd']::text[]) or (mc.valid_windows is not null and not (mc.valid_windows @> $2::timestamptz)))
               and mc.feed_key = any($12::text[])
               and coalesce(mc.road_number = any($9::text[]), false)
               and not (mc.type_closes or mc.closes_text)) as current_feed_keys,
           row_number() over (
             partition by (not ((m.overall_start is not null and m.overall_start > $2::timestamptz) or m.feed_key = any(array['roadworks_akd']::text[]) or (m.valid_windows is not null and not (m.valid_windows @> $2::timestamptz))) and (m.type_closes or m.closes_text))
             order by case when $6::float8 is null then array_position($1::text[], m.road_number) end,
                      m.distance_m nulls last,
                      m.overall_start nulls first, m.referent_from, m.publication_id) as class_rn
    from m
    ) z
    where z.class_rn <= $5::int
  )
  select p.feed_key, p.publication_id, p.road_number, p.event_type, p.publisher,
         p.referent_from, p.referent_to, p.distance_from_m, p.distance_to_m,
         p.overall_start, p.overall_end, p.last_seen_at, p.distance_m, p.closes_text, p.type_closes,
         p.closure_ahead_from, p.closure_ahead_to,
         array(select lower(x) from unnest(p.valid_windows) x where x && tstzrange($2::timestamptz, $3::timestamptz, '[]') order by lower(x) limit 3) as window_starts,
         array(select upper(x) from unnest(p.valid_windows) x where x && tstzrange($2::timestamptz, $3::timestamptz, '[]') order by lower(x) limit 3) as window_ends,
         (select count(*) from unnest(p.valid_windows) x where x && tstzrange($2::timestamptz, $3::timestamptz, '[]'))::int as window_count,
         p.valid_windows is not null as windowed,
         coalesce(p.valid_windows @> $2::timestamptz, false) as window_now,
         p.feed_ts,
         jsonb_path_query_first(o.n, '$.generalPublicComment[*] ? (@.commentExtension[0].commentExtended[0].commentType2[0]."#text" == "roadworksName").comment[0].values[0].value[0]."#text"') #>> '{}' as site_name,
         jsonb_path_query_first(o.n, '$.groupOfLocations[0].**.roadName[0].values[0].value[0]."#text"') #>> '{}' as section_name,
         jsonb_path_query_first(o.n, '$.cause[0].causeDescription[0].values[0].value[0]."#text"') #>> '{}' as cause,
         p.total_rows, p.total_planned, p.total_current, p.total_closed, p.total_programme, p.near_rows, p.near_closed, p.feed_keys, p.current_feed_keys,
         (select array_agg(distinct m0.feed_key) from m0 where (((m0.feed_key = 'traffic_berlin' and m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_lms_berlin')) or (m0.feed_key = 'viz_berlin' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_berlin') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_berlin')))) or (m0.feed_key = 'roadworks_mv_planned' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_mv') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_mv')))) or (m0.feed_key = 'ald_14d' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast')))) or (m0.feed_key = 'roadworks_akd' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast')))) or (m0.feed_key = 'sperrinfosys_sn' and m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_ald_sn')) or (m0.feed_key = 'roadworks_he_gesammelt' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_he') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_he')))) or (m0.feed_key = 'traffic_nrw' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events')))) or (m0.feed_key = 'traffic_nrw_einzel' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events')))) or (m0.feed_key = 'traffic_hamburg' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events'))))))) as also_feed_keys,
         coalesce(
           jsonb_path_query_first(dp.p, '$.headerInformation.headerInformationExtension.EventName."#text"') #>> '{}',
           jsonb_path_query_first(dp.p, '$.situationRecord.generalPublicComment ? (@.commentType."#text" == "locationDescriptor").comment.values.value."#text"') #>> '{}',
           jsonb_path_query_first(dp.p, '$.situationRecord.groupOfLocations.**.roadName.values.value."#text"') #>> '{}'
         ) as place_text,
         jsonb_path_query_first(dp.p, '$.situationRecord.generalPublicComment ? (!exists(@.commentType) || @.commentType."#text" == "description").comment.values.value."#text"') #>> '{}' as detail_text,
         (select string_agg(v.n, ', ' order by v.o) from (
             select r.j #>> '{}' as n, min(r.o) as o
               from jsonb_path_query(dp.p, '$.situationRecord[*] ? (@."@xsi:type" == "ReroutingManagement").alternativeRoute.**.roadNumber."#text"') with ordinality as r(j, o)
              group by 1
           ) v) as detour_via,
         jsonb_path_exists(dp.p, '$.situationRecord[*] ? (@."@xsi:type" == "ReroutingManagement" && exists(@.forVehiclesWithCharacteristicsOf))') as detour_vehicles
  from page p
  join road_events x on x.id = p.id and x.deleted_at is null and coalesce(x.confidentiality, 'noRestriction') = 'noRestriction'
  cross join lateral (select x.payload #> '{}' as p offset 0) dp
  cross join lateral (
    select coalesce(dp.p -> 'situationExtension' -> 0 -> 'situationExtended' -> 0 -> 'overallSituation' -> 0,
                    dp.p -> 'situationRecord' -> 0) as n
  ) o
  order by case when $6::float8 is null then array_position($1::text[], p.road_number) end,
           p.distance_m nulls last,
           p.overall_start nulls first, p.referent_from, p.publication_id`, "text[]", "timestamptz", "timestamptz", "text[]", "int", "float8", "float8", "float8", "text[]", "text[]", "text[]", "text[]", "text[]")
	roadStatusChecked = named("api_road_status_checked", `select k as feed_key,
         (select r.finished_at
            from feed_runs r
           where r.feed_key = k
             and r.status in ('ok', 'unchanged', 'degraded')
             and r.finished_at is not null
           order by r.started_at desc
           limit 1) as checked_at
    from unnest($1::text[]) as k`, "text[]")
	roadStatusChecks   = named("api_road_status_plan_checks", `select feed_key, cadence_s, checked_at, paused from feed_checks where feed_key = any($1::text[])`, "text[]")
	roadStatusStrategy = named("api_road_status_strategies", `
  select r.feed_key, r.source_id, r.publication_id, r.road_number, r.event_type,
         r.referent_from, r.referent_to,
         jsonb_path_query_first(o.n, '$.generalPublicComment[*] ? (@.commentExtension[0].commentExtended[0].commentType2[0]."#text" == "roadworksName").comment[0].values[0].value[0]."#text"') #>> '{}' as name,
         jsonb_path_query_first(o.n, '$.groupOfLocations[0].**.roadName[0].values[0].value[0]."#text"') #>> '{}' as section,
         case when r.feed_key = any($6::text[]) then (select string_agg(v.n, ', ' order by v.o) from (
             select r.j #>> '{}' as n, min(r.o) as o
               from jsonb_path_query(dp.p, '$.situationRecord[*].**.alternativeRoute.**.roadNumber."#text"') with ordinality as r(j, o)
              group by 1
           ) v) end as via,
         case when r.feed_key = any($6::text[]) then coalesce(
           jsonb_path_query_first(dp.p, '$.situationRecord[*].**.reroutingItineraryDescription.**.value."#text"') #>> '{}',
           jsonb_path_query_first(dp.p, '$.situationRecord[*].generalPublicComment.comment.values.value."#text"') #>> '{}'
         ) end as words,
         case when r.feed_key = any($6::text[]) then jsonb_path_query_array(dp.p, '$.situationRecord[*].cause[*].causeDescription[*].values[*].value[*]') end as cause_values,
         case when r.feed_key = any($6::text[]) then jsonb_path_query_array(dp.p, '$.situationRecord[*].generalPublicComment[*].comment[*].values[*].value[*]') end as comment_values,
         r.window_from, r.window_to,
         road_event_in_force(r.overall_start, r.overall_end, dp.p, r.valid_windows, $3::timestamptz) as now_in_force,
         r.on_road, r.lat, r.lon,
         case when r.feed_key <> all($6::text[]) then (coalesce(r.entry_static, jsonb_build_object(
    'pri', coalesce(jsonb_path_query_first(dp.p, '$.situationRecord[0].groupOfLocations.**.alertCMethod4PrimaryPointLocation.alertCLocation.specificLocation."#text"'),
                    jsonb_path_query_first(dp.p, '$.situationRecord[0].groupOfLocations.**.alertCMethod2PrimaryPointLocation.alertCLocation.specificLocation."#text"')),
    'sec', coalesce(jsonb_path_query_first(dp.p, '$.situationRecord[0].groupOfLocations.**.alertCMethod4SecondaryPointLocation.alertCLocation.specificLocation."#text"'),
                    jsonb_path_query_first(dp.p, '$.situationRecord[0].groupOfLocations.**.alertCMethod2SecondaryPointLocation.alertCLocation.specificLocation."#text"')),
    'senses', jsonb_path_query_array(dp.p, '$.situationRecord[*].groupOfLocations.**.alertCDirectionCoded."#text"'),
    'works', jsonb_path_query_first(dp.p, '$.situationRecord[0].generalPublicComment ? (@.commentExtension.commentExtended.commentType2."#text" == "roadworksType").comment.values.value."#text"'),
    'line', jsonb_path_query_first(dp.p, '$.situationRecord[0].groupOfLocations.**.gmlLineString.posList."#text"')
  )) || coalesce((
    select jsonb_build_object('restricted', x.lanes_restricted, 'total', x.lanes_total, 'inuse', x.lanes_in_use, 'width', x.residual_width,
                              'closed', x.closed_lanes, 'shoulder', x.hard_shoulder, 'speed', x.speed_limit, 'mgmt', x.mgmt_now, 'sensesNow', x.senses_now)
      from (select f.lanes_restricted, f.lanes_total, f.lanes_in_use, f.residual_width, f.closed_lanes, f.hard_shoulder, f.speed_limit, (count(*) over ())::int as phases_now,
           jsonb_agg(f.mgmt) over () as mgmt_now, jsonb_agg(f.senses) over () as senses_now
      from (
        select r.n,
               case when r.rec -> 'validity' -> 0 -> 'validityTimeSpecification' -> 0 -> 'overallStartTime' -> 0 ->> '#text' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T' then (r.rec -> 'validity' -> 0 -> 'validityTimeSpecification' -> 0 -> 'overallStartTime' -> 0 ->> '#text')::timestamptz end as phase_start,
               case when r.rec -> 'validity' -> 0 -> 'validityTimeSpecification' -> 0 -> 'overallEndTime' -> 0 ->> '#text' ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T' then (r.rec -> 'validity' -> 0 -> 'validityTimeSpecification' -> 0 -> 'overallEndTime' -> 0 ->> '#text')::timestamptz end as phase_end,
               r.rec -> 'impact' -> 0 -> 'numberOfLanesRestricted' -> 0 ->> '#text' as lanes_restricted,
               r.rec -> 'impact' -> 0 -> 'originalNumberOfLanes' -> 0 ->> '#text' as lanes_total,
               nullif((select count(*) from jsonb_path_query(r.rec, '$.impact[0].impactExtension[0].impactExtended[0].laneRestriction[*].lane."#text"') l(v)
                        where l.v #>> '{}' ~ '^lane[0-9]+$'), 0)::int as lanes_in_use,
               r.rec -> 'impact' -> 0 -> 'residualRoadWidth' -> 0 ->> '#text' as residual_width,
               r.rec -> 'impact' -> 0 -> 'impactExtension' -> 0 -> 'impactExtended' -> 0 -> 'hardShoulderStatus' -> 0 ->> '#text' as hard_shoulder,
               r.rec -> 'operatorActionExtension' -> 0 -> 'operatorActionExtended' -> 0 -> 'mainSpeedLimit' -> 0 ->> '#text' as speed_limit,
               jsonb_path_query_array(r.rec, '$.impact.**.closedLanes."#text"') as closed_lanes,
               jsonb_path_query_array(r.rec, '$.roadOrCarriagewayOrLaneManagementType."#text"') as mgmt,
               jsonb_path_query_array(r.rec, '$.groupOfLocations.**.alertCDirectionCoded."#text"') as senses
          from jsonb_array_elements(case when jsonb_typeof(dp.p -> 'situationRecord') = 'array' then dp.p -> 'situationRecord' else '[]'::jsonb end) with ordinality as r(rec, n)
      ) f
     where (f.phase_start is null or f.phase_start <= greatest(least($3::timestamptz, r.window_to - interval '1 second'), r.window_from))
       and (f.phase_end   is null or f.phase_end   >= greatest(least($3::timestamptz, r.window_to - interval '1 second'), r.window_from))
     order by case when f.lanes_in_use is null and case when f.residual_width ~ '^[0-9]+([.][0-9]+)?$' then (f.residual_width)::numeric end = 0 then case when f.lanes_total ~ '^[0-9]+([.][0-9]+)?$' then (f.lanes_total)::numeric end
                   when f.lanes_in_use is not null then case when f.lanes_total ~ '^[0-9]+([.][0-9]+)?$' then (f.lanes_total)::numeric end - f.lanes_in_use end desc nulls last,
              case when f.lanes_restricted ~ '^[0-9]+([.][0-9]+)?$' then (f.lanes_restricted)::numeric end desc nulls last,
              f.n
     limit 1) x
  ), '{}'::jsonb)) end as entry_facts,
         r.windows
    from (
      select e.feed_key, e.source_id, e.publication_id, e.road_number, e.event_type,
             `+strategyPayloadColumn+`, e.entry_static, e.overall_end, e.valid_windows,
             ST_Y(e.geom::geometry) as lat, ST_X(e.geom::geometry) as lon,
             (select count(*) from unnest(e.valid_windows) x where ((upper_inf(x) or upper(x) > $3::timestamptz) and lower(x) <= $4::timestamptz) and (upper_inf(x) or upper(x) > $3::timestamptz))::int as windows,
             coalesce(lower(w.x), e.overall_start) as window_from,
             case when w.x is null then e.overall_end else upper(w.x) end as window_to,
             (count(*) filter (where e.feed_key <> all($6::text[])) over (partition by e.road_number))::int as on_road,
             row_number() over (
               partition by e.road_number
               order by (e.feed_key = any($6::text[])) desc, array_position($1::text[], e.feed_key),
                        coalesce(lower(w.x), e.overall_start) nulls last, e.publication_id
             ) as rn
        from road_events e
        cross join lateral (select (select x from unnest(e.valid_windows) x where (upper_inf(x) or upper(x) > $3::timestamptz) and lower(x) <= $4::timestamptz order by (upper_inf(x) or upper(x) > $3::timestamptz) desc, lower(x) limit 1) as x) w
       where e.deleted_at is null and e.feed_key = any($1::text[]) and e.closed_at is null and e.road_number = any($2::text[])
         and coalesce(e.confidentiality, 'noRestriction') = 'noRestriction'
         and case
               when e.feed_key = any($6::text[]) then
                 e.event_type = any(array['generalNetworkManagement', 'reroutingManagement', 'reroutingManagementEnhanced']::text[])
                 and `+planInForce+`
               when e.valid_windows is null then
                 coalesce(e.overall_start, '-infinity'::timestamptz) <= $4::timestamptz
                 and coalesce(e.overall_end, 'infinity'::timestamptz) > $3::timestamptz
               else w.x is not null
             end
    ) r
    cross join lateral (
      `+strategyPayloadOnce+`
      offset 0) dp
    cross join lateral (
      select coalesce(dp.p -> 'situationExtension' -> 0 -> 'situationExtended' -> 0 -> 'overallSituation' -> 0,
                      dp.p -> 'situationRecord' -> 0) as n
    ) o
   where r.rn <= $5::int
   order by array_position($2::text[], r.road_number), r.rn`, "text[]", "text[]", "timestamptz", "timestamptz", "int", "text[]")
)
