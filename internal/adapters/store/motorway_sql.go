package store

import "strings"

// Today's statements of the platform's motorway traffic answer, as its tool
// and its motorway page run them; only the parameters' casts are written
// out, and the owner-rights relations it reads are spelled as their
// definitions over the base tables (the measured levels of service, the
// junction points, the road messages), the register's rule on the plan
// sources applied by the families the key may read; the per-lane measured
// state is read only when $3 says the key may read it.
// motorwaySweepRows are the road operator's events of the asked roads at
// their sweeps up to the instant now names, their payload resolved: the statements derive an event's
// fields from it, as the platform does, and never answer it. byRoad, true,
// lists under an asked road the event whose title names it though another
// road files it, and keeps from a road's own the event its title gives to
// another (the road path).
func motorwaySweepRows(now, byRoad string) string {
	return strings.NewReplacer("{now}", now+"::timestamptz", "{byRoad}", byRoad).Replace(`
      select distinct on (u.road, u.ext_id)
             u.source, u.road, u.filed_road, u.kind, u.ext_id, u.title, u.subtitle, u.abnormal_traffic_type,
             u.delay_s, u.extent, u.geom, u.raw, u.raw_hash, u.observed_at
        from (
          select a.source, a.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 0 as foreign_rank
            from autobahn_events a
            join unnest($1::text[], $2::timestamptz[]) as p(road, t) on p.road = a.road and p.t = a.observed_at
           where a.road = any($1::text[])
             and ({byRoad} is not true or (select p is null or p ~ ('(^|[^0-9])' || substr(a.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))
          union all
          select a.source, q.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 1 as foreign_rank
            from (select c.road,
                         coalesce((select p.t from unnest($1::text[], $2::timestamptz[]) as p(road, t) where p.road = c.road limit 1),
                                  (select max(x.observed_at) from autobahn_events x
                                    where x.road = c.road and x.observed_at > {now} - interval '30 minutes' and x.observed_at <= {now})) as t
                    from (select distinct c0.road from autobahn_events c0
                            join (select lt0.road, max(lt0.observed_at) as t from autobahn_events lt0
                                   where lt0.observed_at > {now} - interval '30 minutes' and lt0.observed_at <= {now}
                                   group by lt0.road) lt on lt.road = c0.road and lt.t = c0.observed_at
                           where {byRoad} is true
                             and exists (select 1 from unnest($1::text[]) as q0(road)
                                          where q0.road <> c0.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q0.road, 2) || '([^0-9]|$)') from substring(coalesce(c0.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))) c) ft
            join autobahn_events a on a.road = ft.road and a.observed_at = ft.t
            join unnest($1::text[]) as q(road) on q.road <> a.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p)
        ) u
       order by u.road, u.ext_id, u.foreign_rank, u.filed_road) e
  cross join lateral (select autobahn_raw_resolve(e.raw, e.raw_hash, e.ext_id, e.observed_at) as raw offset 0) rr`)
}

// planInForce is the platform's rule of a plan entry in force now, which
// reads the entry's validity from its payload.
const planInForce = `road_event_in_force(e.overall_start, e.overall_end, e.payload, e.valid_windows, $3::timestamptz)`

var (
	motorwayLatest = named("api_motorway_latest_ticks", `
  select road, max(observed_at) as t
  from autobahn_events
  where road = any($1::text[]) and observed_at > $2::timestamptz - interval '30 minutes' and observed_at <= $2::timestamptz
  group by road`, "text[]", "timestamptz")
	motorwayCounts = named("api_motorway_counts", `
  select e.road, e.kind,
         (count(distinct k.site) filter (where not nf.off))::int as n,
         (count(distinct k.site) filter (where e.abnormal_traffic_type is not null and not nf.off))::int as jams,
         (count(distinct k.site) filter (where nf.off and ((s.future or coalesce(s.starts_at > $3::timestamptz, false)) or not coalesce(not autobahn_validity_overlaps(rr.raw->'description', $3::timestamptz, 'infinity'::timestamptz), false))))::int as announced,
         min(s.starts_at) filter (where s.starts_at > $3::timestamptz) as announced_from
  from (
`+motorwaySweepRows("$3", "true")+`
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
  cross join lateral (select ((s.future or coalesce(s.starts_at > $3::timestamptz, false)) or coalesce(not autobahn_validity_overlaps(rr.raw->'description', $3::timestamptz, $3::timestamptz), false)) as off offset 0) nf
  where e.road = any($1::text[])
  group by e.road, e.kind`, "text[]", "timestamptz[]", "timestamptz")
	motorwayEvents = named("api_motorway_events", `
  with f as (
    select e.road, e.filed_road, e.kind, e.ext_id, e.title, e.subtitle, e.abnormal_traffic_type, e.delay_s, e.extent,
           ST_Y(e.geom::geometry) as lat, ST_X(e.geom::geometry) as lon,
           rr.raw->>'startTimestamp' as start_ts, s.starts_at, rr.raw->'description' as description,
           rr.raw->>'isBlocked' as is_blocked, rr.raw->>'averageSpeed' as avg_speed,
           row_number() over (partition by k.site order by (e.geom is null), e.ext_id) as rn,
           (count(*) over (partition by k.site))::int as segments,
           array_agg(e.ext_id) over (partition by k.site) as site_ext_ids,
           array_agg(e.extent) over (partition by k.site) as site_extents,
           rr.raw->'routeRecommendation' as route_recommendation
    from (
`+motorwaySweepRows("$6", "true")+`
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
    where e.road = any($1::text[]) and e.kind = any($3::text[])
      and not ((s.future or coalesce(s.starts_at > $6::timestamptz, false)) or coalesce(not autobahn_validity_overlaps(rr.raw->'description', $6::timestamptz, $6::timestamptz), false))
  )
  , g as (
    select road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, extent, lat, lon, start_ts, starts_at, description, is_blocked, avg_speed, segments, filed_road, site_ext_ids, site_extents, route_recommendation,
           case when kind = 'warning' and abnormal_traffic_type is not null then 0
                when kind = 'closure' then 1
                when kind = 'warning' then 2
                else 3 end as tier
    from f
    where rn = 1
  ), h as (
    select road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, extent, lat, lon, start_ts, starts_at, description, is_blocked, avg_speed, segments, filed_road, site_ext_ids, site_extents, route_recommendation, tier,
           row_number() over (
             partition by road, tier
             order by case abnormal_traffic_type when 'QUEUING_TRAFFIC' then 0 when 'STATIONARY_TRAFFIC' then 1 when 'HEAVY_TRAFFIC' then 2 when 'SLOW_TRAFFIC' then 3 else 4 end,
                      delay_s desc nulls last, title, ext_id
           ) as turn
    from g
  ), l as (
    select road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, extent, lat, lon, start_ts, starts_at, description, is_blocked, avg_speed, segments, filed_road, site_ext_ids, site_extents, route_recommendation, tier, turn,
           case when tier < 4 and row_number() over (partition by road order by tier, turn) = 1 then 0 else 1 end as lead
    from h
  )
  select road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, extent, lat, lon,
         start_ts, starts_at, description, is_blocked, avg_speed, segments, filed_road, site_ext_ids, site_extents, route_recommendation
  from l
  order by lead, tier, turn, array_position($1::text[], road)
  limit $4::int offset $5::int`, "text[]", "timestamptz[]", "text[]", "int", "int", "timestamptz")
	motorwayLos = named("api_motorway_measured", `
  with cross_section as (
    select site_id, road, status, geom, measured_at
      from (select s.site_id, s.road, s.geom, t.status, t.measured_at, t.last_seen_at
           from los_sites s join los_state t on t.site_id = s.site_id
          where s.closed_at is null and s.carriageway = 'hfb' and s.road is not null and s.geom is not null
            and coalesce(s.confidentiality, 'noRestriction') = 'noRestriction' and coalesce(t.confidentiality, 'noRestriction') = 'noRestriction') los_now
     where road = any($1::text[])
       and last_seen_at >= $2::timestamptz
       and status in ('freeFlow', 'heavy', 'congested', 'impossible')
  ), by_lane as (
    select distinct on (l.site_id) l.site_id, l.road, l.status, l.geom, l.measured_at
      from (select t.site_id, c.road, c.geom, t.status, t.measured_at, t.last_seen_at
           from los_lane_state t join los_lane_sites s on s.lane_id = t.lane_id join los_sites c on c.site_id = t.site_id
          where s.closed_at is null and c.closed_at is null and c.carriageway = 'hfb' and c.road is not null and c.geom is not null
            and coalesce(s.confidentiality, 'noRestriction') = 'noRestriction' and coalesce(t.confidentiality, 'noRestriction') = 'noRestriction'
            and coalesce(c.confidentiality, 'noRestriction') = 'noRestriction') l
     where l.road = any($1::text[])
       and l.last_seen_at >= $2::timestamptz
       and l.status in ('freeFlow', 'heavy', 'congested', 'impossible')
       and not exists (select 1 from cross_section c where c.site_id = l.site_id)
       and $3::boolean
     order by l.site_id, case l.status when 'impossible' then 0 when 'congested' then 1 when 'heavy' then 2 when 'freeFlow' then 3 end, l.measured_at desc
  ), fresh as (
    select road, status, geom, measured_at, false as lane_read from cross_section
    union all
    select road, status, geom, measured_at, true as lane_read from by_lane
  ), named as (
    select f.road, p.name, min(case f.status when 'impossible' then 0 else 1 end) as rank
      from fresh f
      cross join lateral (
        select m.name from (select road_number as road, name, geom from tmc_locations where cid = 58 and tabcd = 1 and road_number ~ '^A[1-9][0-9]{0,2}$') m
         where m.road = f.road and m.name is not null and m.name <> ''
         order by m.geom <-> f.geom
         limit 1
      ) p
     where f.status in ('impossible', 'congested')
     group by f.road, p.name
  )
  select f.road,
         count(*)::int as sites,
         count(*) filter (where f.status = 'impossible')::int as standstill,
         count(*) filter (where f.status = 'congested')::int as slow,
         count(*) filter (where f.status = 'heavy')::int as heavy,
         count(*) filter (where f.status = 'freeFlow')::int as free,
         count(*) filter (where f.lane_read)::int as lane_sites,
         max(f.measured_at) as measured_at,
         coalesce((select array_agg(n.name order by n.rank, n.name) from named n where n.road = f.road), '{}') as slow_near
    from fresh f
   group by f.road
   order by f.road`, "text[]", "timestamptz", "boolean")
	motorwayPlans = named("api_motorway_plans_now", `
  select e.feed_key, e.publication_id, e.source_id, e.road_number, e.event_type,
         jsonb_path_query_first(o.n, '$.generalPublicComment[*] ? (@.commentExtension[0].commentExtended[0].commentType2[0]."#text" == "roadworksName").comment[0].values[0].value[0]."#text"') #>> '{}' as name,
         jsonb_path_query_first(o.n, '$.groupOfLocations[0].**.roadName[0].values[0].value[0]."#text"') #>> '{}' as section,
         jsonb_path_query_first(o.n, '$.cause[0].causeDescription[0].values[0].value[0]."#text"') #>> '{}' as cause,
         coalesce(lower(w.x), e.overall_start) as window_from,
         case when w.x is null then e.overall_end else upper(w.x) end as window_to,
         (count(*) over (partition by e.road_number))::int as on_road
    from road_events e
    cross join lateral (
      select coalesce(e.payload -> 'situationExtension' -> 0 -> 'situationExtended' -> 0 -> 'overallSituation' -> 0,
                      e.payload -> 'situationRecord' -> 0) as n
    ) o
    left join lateral (select x from unnest(e.valid_windows) x where x @> $3::timestamptz limit 1) w on true
   where e.deleted_at is null and e.feed_key = any($1::text[]) and e.closed_at is null and e.road_number = any($2::text[])
     and coalesce(e.confidentiality, 'noRestriction') = 'noRestriction'
     and `+planInForce+`
   order by array_position($2::text[], e.road_number), coalesce(lower(w.x), e.overall_start) nulls last, e.publication_id
   limit $4::int`, "text[]", "text[]", "timestamptz", "int")
	motorwayJamTicks = named("api_motorway_jam_ticks", `
  with q as (
    select q.road, q.stem, q.title, q.subtitle, (q.i - 1)::int as grp
    from unnest($1::text[], $2::text[], $3::text[], $4::text[]) with ordinality as q(road, stem, title, subtitle, i)
  )
  select q.grp, e.observed_at as at, array_agg(e.extent order by e.ext_id) as extents, max(e.delay_s)::int as delay_s
  from autobahn_events e
  join q
    on e.road = q.road
   and e.title is not distinct from q.title
   and e.subtitle is not distinct from q.subtitle
   and regexp_replace(e.ext_id, '[.]de[0-9]+$', '') = q.stem
  where e.road = any($1::text[])
    and e.observed_at > $5::timestamptz - interval '30 minutes'
    and e.observed_at <= $5::timestamptz
    and e.kind = 'warning'
    and e.abnormal_traffic_type is not null
  group by q.grp, e.observed_at
  order by q.grp, e.observed_at`, "text[]", "text[]", "text[]", "text[]", "timestamptz")
	motorwayJamUsual = named("api_motorway_jam_usual", `
  with q as (
    select q.road, q.title, q.subtitle, q.i
    from unnest($1::text[], $2::text[], $3::text[]) with ordinality as q(road, title, subtitle, i)
  ), w as (
    select g, ((date_trunc('hour', $4::timestamptz at time zone 'Europe/Berlin') - make_interval(days => 7 * g)) at time zone 'Europe/Berlin') as t0
    from generate_series(1, $5::int) g
  ), h as (
    select h.road, w.g, h.jam_titles, h.jam_subtitles
    from w join autobahn_jam_hours h on h.hour_start = w.t0
    where h.road = any($1::text[]) and h.hour_start >= $6::timestamptz and h.hour_start < $4::timestamptz
  ), r as (
    select q.i, x.hour_start,
           exists (select 1 from unnest(x.jam_titles, x.jam_subtitles) s(title, subtitle)
                   where s.title = q.title and s.subtitle is not distinct from q.subtitle) as jam
    from q join autobahn_jam_hours x on x.road = q.road
    where x.hour_start >= $6::timestamptz
      and x.hour_start < date_trunc('hour', $4::timestamptz, 'UTC')
  ), m as (
    select i, hour_start, jam,
           lag(hour_start) over p as prev_h, lag(jam) over p as prev_jam,
           lead(hour_start) over p as next_h, lead(jam) over p as next_jam
    from r
    window p as (partition by i order by hour_start)
  ), n as (
    select i, hour_start, jam, prev_h, prev_jam, next_h, next_jam,
           sum(case when jam and (prev_h is distinct from hour_start - interval '1 hour' or not prev_jam) then 1 else 0 end)
             over (partition by i order by hour_start) as run
    from m
  ), runs as (
    select i, run, min(hour_start) as start_h, count(*)::int as hours,
           coalesce(bool_or(prev_h = hour_start - interval '1 hour' and not prev_jam), false) as clean_start,
           coalesce(bool_or(next_h = hour_start + interval '1 hour' and not next_jam), false) as clean_end
    from n
    where jam
    group by i, run
  ), how as (
    select i, count(*)::int as how_cases,
           percentile_disc(0.25) within group (order by hours)::int as how_from,
           percentile_disc(0.75) within group (order by hours)::int as how_to
    from runs
    where clean_start and clean_end
      and (extract(isodow from start_h at time zone 'Europe/Berlin') >= 6) = (extract(isodow from $4::timestamptz at time zone 'Europe/Berlin') >= 6)
    group by i
  )
  select q.i::int as i,
         (select count(*) from h where h.road = q.road)::int as weeks_observed,
         (select count(*) from h
           where h.road = q.road
             and exists (select 1 from unnest(h.jam_titles, h.jam_subtitles) s(title, subtitle)
                         where s.title = q.title and s.subtitle is not distinct from q.subtitle))::int as weeks_with_jam,
         coalesce(how.how_cases, 0)::int as how_cases, how.how_from, how.how_to
  from q
  left join how on how.i = q.i
  order by q.i`, "text[]", "text[]", "text[]", "timestamptz", "int", "timestamptz")
	motorwayJamHistory = named("api_motorway_jam_history", `select min(hour_start) as oldest from autobahn_jam_hours`)
)
