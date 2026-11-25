//go:build integration

package store

// The reads below had no view: each answers as the platform's own read
// of the same answer (the pattern of #222), its statement text as the
// platform states it, with the key's families where the named statement
// reads them, so the replay compares the named statement with the read
// it was carried from.
func init() {
	legacyReads["api_alerts_coverage"] = `  select l.land,
         (count(*) filter (where a.last_seen_at >= $2::timestamptz))::int as alerts,



         max(a.last_seen_at) as last_seen_at
    from delfi_service_alerts a
    cross join lateral unnest(a.lands) as l(land)
   where a.source_feed = any($3::text[]) and l.land = any((array[left($1::text, 2)])::text[])
     and a.placed_by in ('stop', 'route')
     and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
   group by l.land
   order by l.land`
	legacyReads["api_charging_at_town_point"] = `  select q.i::int as i
    from unnest($1::float8[], $2::float8[], $3::text[]) with ordinality as q(lon, lat, town, i)
   where q.town <> ''
     and exists (
       select 1 from gazetteer_places g
        where g.closed_at is null and g.kind = 'city' and g.geom is not null
          and g.source_id in ('bkg_gvisys', 'geonames')
          and (g.name_norm = q.town or lower(g.name_en) = q.town)
          and ST_DWithin(g.geom, ST_SetSRID(ST_MakePoint(q.lon, q.lat), 4326)::geography, $4::float8))`
	//nolint:misspell // materialized is the SQL keyword
	legacyReads["api_charging_places_near"] = `  with near as materialized (
    select s.source_feed, s.site_id, s.name, s.operator, s.street, s.house_number, s.postcode, s.city,
           s.source_version, s.geom, s.operator_tokens, s.street_key, s.house_key,
           ST_Y(s.geom::geometry) as lat, ST_X(s.geom::geometry) as lon,
           ST_Distance(s.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) as distance_m,





           coalesce((s.source_feed = any($16::text[])
      and exists (
        select 1 from unnest(s.cands) a
         where a.closed_at is null
           and a.source_feed = any($5::text[])
           and not (a.source_feed = any($16::text[]))
           and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
           and ST_DWithin(a.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
           and ST_DWithin(a.geom, s.geom, greatest($17::float8, 150))
           and ((ST_DWithin(a.geom, s.geom, $17)
           and (a.operator_tokens && s.operator_tokens
                or (a.street_key <> ''
                    and a.street_key = s.street_key
                    and (a.postcode is null or s.postcode is null or a.postcode = s.postcode)
                    and (a.house_key is null or s.house_key is null or a.house_key = s.house_key)




                    and not exists (
                      select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) r
                       where r.closed_at is null
                         and r.source_feed = any($16::text[])
                         and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                         and ST_DWithin(r.geom, a.geom, $17)
                         and r.operator_tokens && a.operator_tokens))))

                or (a.operator_tokens && s.operator_tokens
                    and a.street_key <> ''
                    and a.street_key = s.street_key
                    and a.house_key is not null
                    and a.house_key = s.house_key
                    and (a.postcode is null or s.postcode is null or a.postcode = s.postcode)))
           and exists (
             select 1 from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) ap
              where ap.source_feed = a.source_feed and ap.site_id = a.site_id
                and ap.closed_at is null and coalesce(ap.confidentiality, 'noRestriction') = 'noRestriction'))), false) as is_twin
    from (
      select (c.site).source_feed, (c.site).site_id, (c.site).name, (c.site).operator, (c.site).street, (c.site).house_number, (c.site).postcode, (c.site).city, (c.site).source_version, (c.site).geom, (c.site).operator_tokens, (c.site).street_key, (c.site).house_key, c.cands
        from (
          select g.site, g.k,
                 array_agg(g.site) filter (where g.k > 0)
                   over (partition by g.cy order by g.cx range between 1 preceding and 1 following) as cands
            from (
              select s as site, o.k,
                     floor(ST_Y(s.geom::geometry) / (greatest($17::float8, 150) / 100000.0)) + case when o.k > 0 then o.k - 2 else 0 end as cy,
                     case when abs($2::float8) + $3::float8 / 100000.0 > 80 or abs($1::float8) > 170 then 0
                          else floor(ST_X(s.geom::geometry) / (greatest($17::float8, 150) / (100000.0 * cos(radians(abs($2::float8) + $3::float8 / 100000.0))))) end as cx
                from charging_sites s

                cross join lateral unnest(case when s.source_feed = any($16::text[]) then '{0}'::int[] else '{0,1,2,3}'::int[] end) as o(k)
               where s.deleted_at is null and not s.access_private and s.closed_at is null
                 and s.source_feed = any($5::text[])
                 and coalesce(s.confidentiality, 'noRestriction') = 'noRestriction'
                 and ST_DWithin(s.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
            ) g
        ) c
       where c.k = 0
    ) s
  ),
  ranks as (
    select r.feed, r.rank from unnest($12::text[], $13::int[]) as r(feed, rank)
  ),
  near_pts as (







    select p.source_feed, p.site_id, p.point_key, p.evse_id, p.provider_point_id, p.connector_types,
           p.max_power_kw,
           case when p.evse_id is null then null else upper(translate(p.evse_id, '*-', '')) end as evse_norm,
           case when p.provider_point_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then lower(p.provider_point_id) end as uuid_norm,
           r.rank,
           case when n.is_twin and p.connector_types && array['mcs']::text[]
                then coalesce((n.source_feed = any($16::text[])
      and exists (
        select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) a
         where a.closed_at is null
           and a.source_feed = any($5::text[])
           and not (a.source_feed = any($16::text[]))
           and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
           and ST_DWithin(a.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
           and ST_DWithin(a.geom, n.geom, greatest($17::float8, 150))
           and ((ST_DWithin(a.geom, n.geom, $17)
           and (a.operator_tokens && n.operator_tokens
                or (a.street_key <> ''
                    and a.street_key = n.street_key
                    and (a.postcode is null or n.postcode is null or a.postcode = n.postcode)
                    and (a.house_key is null or n.house_key is null or a.house_key = n.house_key)




                    and not exists (
                      select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) r
                       where r.closed_at is null
                         and r.source_feed = any($16::text[])
                         and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                         and ST_DWithin(r.geom, a.geom, $17)
                         and r.operator_tokens && a.operator_tokens))))

                or (a.operator_tokens && n.operator_tokens
                    and a.street_key <> ''
                    and a.street_key = n.street_key
                    and a.house_key is not null
                    and a.house_key = n.house_key
                    and (a.postcode is null or n.postcode is null or a.postcode = n.postcode)))
           and exists (
             select 1 from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) ap
              where ap.source_feed = a.source_feed and ap.site_id = a.site_id
                and ap.closed_at is null and coalesce(ap.confidentiality, 'noRestriction') = 'noRestriction' and ap.connector_types && array['mcs']::text[]))), false)
                else n.is_twin end as is_twin, n.name, n.operator, n.street, n.house_number, n.postcode, n.city, n.source_version,
           n.lat, n.lon, n.distance_m
    from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) p
    join near n on n.source_feed = p.source_feed and n.site_id = p.site_id
    left join ranks r on r.feed = p.source_feed
    where p.closed_at is null
      and coalesce(p.confidentiality, 'noRestriction') = 'noRestriction'
  ),
  ranked as (




    select np.*,
           min(np.rank) over (partition by np.evse_norm) as best_rank,
           min(np.rank) over (partition by np.uuid_norm) as best_rank_uuid,



           array_agg(np.source_feed) filter (where np.uuid_norm is not null) over (partition by np.uuid_norm) as uuid_feeds,
           array_agg(np.provider_point_id) filter (where np.uuid_norm is not null) over (partition by np.uuid_norm) as uuid_ids
    from near_pts np
  ),
  pts as (
    select p.source_feed, p.site_id, p.point_key, p.evse_id, p.provider_point_id, p.connector_types,
           p.max_power_kw,
           p.name, p.operator, p.street, p.house_number, p.postcode, p.city, p.source_version,
           p.lat, p.lon, p.distance_m, p.uuid_feeds, p.uuid_ids
    from ranked p
    where ($6::numeric is null or p.max_power_kw >= $6::numeric)
      and ($7::text[] is null or p.connector_types && $7::text[])



      and not ((p.evse_norm is not null and p.rank is not null and p.best_rank < p.rank) or (p.uuid_norm is not null and p.rank is not null and p.best_rank_uuid < p.rank))

      and not p.is_twin
  ),
  kept as (






    select d.source_feed, d.site_id, d.name, d.operator, d.street, d.house_number, d.postcode, d.city,
           d.source_version, d.lat, d.lon, d.distance_m
    from (
      select distinct on (p.source_feed, p.lat, p.lon) p.*
      from pts p
      order by p.source_feed, p.lat, p.lon, p.site_id
    ) d
    order by d.distance_m asc, d.site_id asc
    limit $4
  ),
  kpts as materialized (









    select p.* from pts p
     where p.distance_m <= (select max(k.distance_m) from kept k)
       and row(p.source_feed, p.lat, p.lon) = any(array(select row(k.source_feed, k.lat, k.lon) from kept k))
  )




  select k.source_feed, k.site_id,
         case when count(distinct p.name) <= 1 and (bool_and(p.name is null) or bool_and(p.name is not null)) then k.name end as name,
         k.operator, k.street, k.house_number, k.postcode, k.city,
         k.source_version, k.lat, k.lon, k.distance_m,



         (select (select eps.slug from energy_page_slugs eps where eps.kind = 'charging' and eps.source_feed = k.source_feed and eps.item_id = rep.site_id)
            from (select (select coalesce(min(o.site_id) filter (where not ((coalesce((o.source_feed = any('{"bnetza_lsr"}'::text[]::text[])
      and exists (
        select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) a
         where a.closed_at is null
           and a.source_feed = any($5::text[])
           and not (a.source_feed = any('{"bnetza_lsr"}'::text[]::text[]))
           and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
           and ST_DWithin(a.geom, o.geom, greatest(50::float8, 150))
           and ST_DWithin(a.geom, o.geom, greatest(50::float8, 150))
           and ((ST_DWithin(a.geom, o.geom, 50)
           and (a.operator_tokens && o.operator_tokens
                or (a.street_key <> ''
                    and a.street_key = o.street_key
                    and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)
                    and (a.house_key is null or o.house_key is null or a.house_key = o.house_key)




                    and not exists (
                      select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) r
                       where r.closed_at is null
                         and r.source_feed = any('{"bnetza_lsr"}'::text[]::text[])
                         and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                         and ST_DWithin(r.geom, a.geom, 50)
                         and r.operator_tokens && a.operator_tokens))))

                or (a.operator_tokens && o.operator_tokens
                    and a.street_key <> ''
                    and a.street_key = o.street_key
                    and a.house_key is not null
                    and a.house_key = o.house_key
                    and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)))
           and exists (
             select 1 from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) ap
              where ap.source_feed = a.source_feed and ap.site_id = a.site_id
                and ap.closed_at is null and coalesce(ap.confidentiality, 'noRestriction') = 'noRestriction'))), false)
            and not (exists (select 1 from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) kp
                    where kp.source_feed = o.source_feed and kp.site_id = o.site_id
                      and kp.closed_at is null and coalesce(kp.confidentiality, 'noRestriction') = 'noRestriction'
                      and kp.connector_types && array['mcs']::text[])
            and not coalesce((o.source_feed = any('{"bnetza_lsr"}'::text[]::text[])
      and exists (
        select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) a
         where a.closed_at is null
           and a.source_feed = any($5::text[])
           and not (a.source_feed = any('{"bnetza_lsr"}'::text[]::text[]))
           and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
           and ST_DWithin(a.geom, o.geom, greatest(50::float8, 150))
           and ST_DWithin(a.geom, o.geom, greatest(50::float8, 150))
           and ((ST_DWithin(a.geom, o.geom, 50)
           and (a.operator_tokens && o.operator_tokens
                or (a.street_key <> ''
                    and a.street_key = o.street_key
                    and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)
                    and (a.house_key is null or o.house_key is null or a.house_key = o.house_key)




                    and not exists (
                      select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) r
                       where r.closed_at is null
                         and r.source_feed = any('{"bnetza_lsr"}'::text[]::text[])
                         and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                         and ST_DWithin(r.geom, a.geom, 50)
                         and r.operator_tokens && a.operator_tokens))))

                or (a.operator_tokens && o.operator_tokens
                    and a.street_key <> ''
                    and a.street_key = o.street_key
                    and a.house_key is not null
                    and a.house_key = o.house_key
                    and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)))
           and exists (
             select 1 from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) ap
              where ap.source_feed = a.source_feed and ap.site_id = a.site_id
                and ap.closed_at is null and coalesce(ap.confidentiality, 'noRestriction') = 'noRestriction' and ap.connector_types && array['mcs']::text[]))), false))) or ((select sr.rank from (select r.feed, r.rank from unnest('{"afir_enbw_stat","afir_ecomovement","afir_tesla_stat","afir_vw_stat","afir_eclearing_stat","afir_pump_stat","afir_edri_stat","afir_audi_stat","afir_enio_stat","afir_evprice_stat","afir_ewe_stat","afir_gls_stat","afir_gpjoule_stat","afir_gridco_stat","afir_eround_stat","afir_inergia_stat","afir_rudnick_stat","afir_lichtblick_stat","afir_monta_stat","afir_qwello_stat","afir_road_stat","afir_smatrics_stat","afir_wirelane_stat","afir_chargecloud_stat","afir_vaylens_stat","afir_tcbackend_stat","afir_elu_stat","afir_chargepool_stat","afir_eliso_stat","afir_ladenetz_stat","afir_ladebusiness_stat","ocpdb_chargecloud_stat","ocpdb_taubert_stat","bnetza_lsr"}'::text[], '{10,39,11,12,34,13,14,15,16,17,18,19,20,21,23,24,25,26,27,28,29,30,31,32,33,37,22,38,9,35,36,40,42,90}'::int[]) as r(feed, rank) where r.feed = any($5::text[])) sr where sr.feed = o.source_feed) is not null
     and not exists (select 1 from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) cp
                      where cp.source_feed = o.source_feed and cp.site_id = o.site_id and cp.closed_at is null and coalesce(cp.confidentiality, 'noRestriction') = 'noRestriction'
                        and not (cp.evse_id is not null and exists (select 1
            from (select cq.* from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) cq
                   where cq.evse_id is not null and upper(translate(cq.evse_id, '*-', '')) = upper(translate(cp.evse_id, '*-', ''))
                     and coalesce(cq.confidentiality, 'noRestriction') = 'noRestriction' offset 0) cq
            join (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) cs on cs.source_feed = cq.source_feed and cs.site_id = cq.site_id
            join (select r.feed, r.rank from unnest('{"afir_enbw_stat","afir_ecomovement","afir_tesla_stat","afir_vw_stat","afir_eclearing_stat","afir_pump_stat","afir_edri_stat","afir_audi_stat","afir_enio_stat","afir_evprice_stat","afir_ewe_stat","afir_gls_stat","afir_gpjoule_stat","afir_gridco_stat","afir_eround_stat","afir_inergia_stat","afir_rudnick_stat","afir_lichtblick_stat","afir_monta_stat","afir_qwello_stat","afir_road_stat","afir_smatrics_stat","afir_wirelane_stat","afir_chargecloud_stat","afir_vaylens_stat","afir_tcbackend_stat","afir_elu_stat","afir_chargepool_stat","afir_eliso_stat","afir_ladenetz_stat","afir_ladebusiness_stat","ocpdb_chargecloud_stat","ocpdb_taubert_stat","bnetza_lsr"}'::text[], '{10,39,11,12,34,13,14,15,16,17,18,19,20,21,23,24,25,26,27,28,29,30,31,32,33,37,22,38,9,35,36,40,42,90}'::int[]) as r(feed, rank) where r.feed = any($5::text[])) cr on cr.feed = cs.source_feed
           where cq.closed_at is null and coalesce(cq.confidentiality, 'noRestriction') = 'noRestriction' and cs.closed_at is null and coalesce(cs.confidentiality, 'noRestriction') = 'noRestriction'
            and ST_DWithin(cs.geom, o.geom, 60) and cr.rank < (select sr.rank from (select r.feed, r.rank from unnest('{"afir_enbw_stat","afir_ecomovement","afir_tesla_stat","afir_vw_stat","afir_eclearing_stat","afir_pump_stat","afir_edri_stat","afir_audi_stat","afir_enio_stat","afir_evprice_stat","afir_ewe_stat","afir_gls_stat","afir_gpjoule_stat","afir_gridco_stat","afir_eround_stat","afir_inergia_stat","afir_rudnick_stat","afir_lichtblick_stat","afir_monta_stat","afir_qwello_stat","afir_road_stat","afir_smatrics_stat","afir_wirelane_stat","afir_chargecloud_stat","afir_vaylens_stat","afir_tcbackend_stat","afir_elu_stat","afir_chargepool_stat","afir_eliso_stat","afir_ladenetz_stat","afir_ladebusiness_stat","ocpdb_chargecloud_stat","ocpdb_taubert_stat","bnetza_lsr"}'::text[], '{10,39,11,12,34,13,14,15,16,17,18,19,20,21,23,24,25,26,27,28,29,30,31,32,33,37,22,38,9,35,36,40,42,90}'::int[]) as r(feed, rank) where r.feed = any($5::text[])) sr where sr.feed = o.source_feed))
      or (case when cp.provider_point_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then lower(cp.provider_point_id) end is not null and exists (select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) cs
            join (select r.feed, r.rank from unnest('{"afir_enbw_stat","afir_ecomovement","afir_tesla_stat","afir_vw_stat","afir_eclearing_stat","afir_pump_stat","afir_edri_stat","afir_audi_stat","afir_enio_stat","afir_evprice_stat","afir_ewe_stat","afir_gls_stat","afir_gpjoule_stat","afir_gridco_stat","afir_eround_stat","afir_inergia_stat","afir_rudnick_stat","afir_lichtblick_stat","afir_monta_stat","afir_qwello_stat","afir_road_stat","afir_smatrics_stat","afir_wirelane_stat","afir_chargecloud_stat","afir_vaylens_stat","afir_tcbackend_stat","afir_elu_stat","afir_chargepool_stat","afir_eliso_stat","afir_ladenetz_stat","afir_ladebusiness_stat","ocpdb_chargecloud_stat","ocpdb_taubert_stat","bnetza_lsr"}'::text[], '{10,39,11,12,34,13,14,15,16,17,18,19,20,21,23,24,25,26,27,28,29,30,31,32,33,37,22,38,9,35,36,40,42,90}'::int[]) as r(feed, rank) where r.feed = any($5::text[])) cr on cr.feed = cs.source_feed
            join (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) cq on cq.source_feed = cs.source_feed and cq.site_id = cs.site_id
           where (case when cq.provider_point_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then lower(cq.provider_point_id) end) = (case when cp.provider_point_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then lower(cp.provider_point_id) end)
            and cq.closed_at is null and coalesce(cq.confidentiality, 'noRestriction') = 'noRestriction' and cs.closed_at is null and coalesce(cs.confidentiality, 'noRestriction') = 'noRestriction'
            and ST_DWithin(cs.geom, o.geom, 60) and cr.rank < (select sr.rank from (select r.feed, r.rank from unnest('{"afir_enbw_stat","afir_ecomovement","afir_tesla_stat","afir_vw_stat","afir_eclearing_stat","afir_pump_stat","afir_edri_stat","afir_audi_stat","afir_enio_stat","afir_evprice_stat","afir_ewe_stat","afir_gls_stat","afir_gpjoule_stat","afir_gridco_stat","afir_eround_stat","afir_inergia_stat","afir_rudnick_stat","afir_lichtblick_stat","afir_monta_stat","afir_qwello_stat","afir_road_stat","afir_smatrics_stat","afir_wirelane_stat","afir_chargecloud_stat","afir_vaylens_stat","afir_tcbackend_stat","afir_elu_stat","afir_chargepool_stat","afir_eliso_stat","afir_ladenetz_stat","afir_ladebusiness_stat","ocpdb_chargecloud_stat","ocpdb_taubert_stat","bnetza_lsr"}'::text[], '{10,39,11,12,34,13,14,15,16,17,18,19,20,21,23,24,25,26,27,28,29,30,31,32,33,37,22,38,9,35,36,40,42,90}'::int[]) as r(feed, rank) where r.feed = any($5::text[])) sr where sr.feed = o.source_feed)))))))), min(o.site_id)) from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) o
      where ST_DWithin(o.geom, ST_SetSRID(ST_MakePoint(k.lon, k.lat), 4326)::geography, 1)
        and (row(o.source_feed, ST_X(o.geom::geometry), ST_Y(o.geom::geometry)) = row(k.source_feed, k.lon, k.lat)) is true
        and o.closed_at is null and coalesce(o.confidentiality, 'noRestriction') = 'noRestriction'
     and exists (select 1 from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) p where p.source_feed = o.source_feed and p.site_id = o.site_id and p.closed_at is null and coalesce(p.confidentiality, 'noRestriction') = 'noRestriction')) as site_id) rep) as page_slug,
         coalesce(json_agg(json_build_object(
           'pointKey', p.point_key,
           'siteId', p.site_id,
           'evseId', p.evse_id,
           'connectorTypes', p.connector_types,
           'maxPowerKw', p.max_power_kw,
           'pricePerKwh', pw.price_per_kwh,
           'priceTaxIncluded', pw.price_tax_included,
           'priceTaxRate', pw.price_tax_rate,
           'priceCurrency', pw.price_currency,
           'terms', array_remove(array[case when (case pw.x_hours when 'always' then true when 'restricted' then false end) then 'open247' end, case when (pw.x_card) then 'card' end, case when (case when pw.x_free is not null then pw.x_free when pw.price_per_kwh > 0 then false when pw.x_price_zero = false then false when pw.x_price_zero and pw.x_price_at >= now() - interval '90 days' then true end) then 'free' end, case when (case pw.x_park when 'public' then true when 'customers' then false end) then 'public' end]::text[], null),
           'termsSaid', array_remove(array[case when (case pw.x_hours when 'always' then true when 'restricted' then false end) is not null then 'open247' end, case when (pw.x_card) is not null then 'card' end, case when (case when pw.x_free is not null then pw.x_free when pw.price_per_kwh > 0 then false when pw.x_price_zero = false then false when pw.x_price_zero and pw.x_price_at >= now() - interval '90 days' then true end) is not null then 'free' end, case when (case pw.x_park when 'public' then true when 'customers' then false end) is not null then 'public' end]::text[], null),
           'statusFeed', s.source_feed,
           'status', s.status,
           'operationStatus', s.operation_status,
           'openingStatus', s.opening_status,
           'observedAt', s.observed_at,
           'feedTs', s.feed_ts,
           'lastUpdated', s.last_updated,
           'exchangeProtocol', s.exchange_protocol
         ) order by p.max_power_kw desc nulls last, p.point_key), '[]'::json) as points,
         array_agg(distinct p.site_id order by p.site_id) as site_ids
  from kept k
  join kpts p on k.source_feed = p.source_feed and k.lat = p.lat and k.lon = p.lon
  join lateral (
    select w.price_per_kwh, w.price_tax_included, w.price_tax_rate, w.price_currency,
           w.register_opening_hours as x_hours, w.register_payment_card as x_card, w.register_free_of_charge as x_free, w.register_parking as x_park, w.price_adhoc_zero as x_price_zero, w.price_last_updated as x_price_at
      from (SELECT source_feed,
    point_key,
    evse_id,
    point_key_synthetic,
    site_id,
    station_id,
    provider_point_id,
    operator,
    geom,
    connector_types,
    current_type,
    max_power_kw,
    price_per_kwh,
    price_tax_included,
    price_tax_rate,
    price_currency,
    price_last_updated,
    source_version,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    extras,
    price_adhoc_zero,
    charging_register_opening_hours(extras) AS register_opening_hours,
    charging_register_payment_card(extras) AS register_payment_card,
    charging_register_free_of_charge(extras) AS register_free_of_charge,
    charging_register_parking(extras) AS register_parking
   FROM charging_points
  WHERE (deleted_at IS NULL)) w
     where w.source_feed = p.source_feed and w.point_key = p.point_key
       and w.closed_at is null and coalesce(w.confidentiality, 'noRestriction') = 'noRestriction'
  ) pw on true
  left join lateral (
    select t.evse_id, t.status, t.operation_status, t.opening_status, t.observed_at, t.feed_ts, t.last_updated,
           t.exchange_protocol, t.source_feed, t.confidentiality
      from (
        select p.evse_id as match_key, null::text as only_feed
         where p.evse_id is not null
        union all
        select p.provider_point_id, pr.status_feed
          from unnest($14::text[], $15::text[]) as pr(status_feed, master_feed)
         where pr.master_feed = p.source_feed and p.provider_point_id is not null
        union all
        select h.provider_point_id, pr.status_feed
          from unnest(p.uuid_feeds, p.uuid_ids) as h(source_feed, provider_point_id)
          join unnest($14::text[], $15::text[]) as pr(status_feed, master_feed) on pr.master_feed = h.source_feed
         where h.source_feed <> p.source_feed and h.provider_point_id is not null
      ) k
      cross join lateral (
        select t.evse_id, t.status, t.operation_status, t.opening_status, t.observed_at, t.feed_ts, t.last_updated,
               t.exchange_protocol, t.source_feed, t.confidentiality
          from charging_point_status t
         where t.evse_id = k.match_key
           and (k.only_feed is null or t.source_feed = k.only_feed)
           and t.source_feed = any($8::text[])
           and t.observed_at > least($10::timestamptz - greatest(make_interval(secs => $9::int), make_interval(hours => 48)), $11::timestamptz)
           and t.observed_at <= $10::timestamptz
           and (t.observed_at > $10::timestamptz - greatest(make_interval(secs => $9::int), make_interval(hours => 48))
                or (lower(t.exchange_protocol) = 'deltapull' and t.observed_at > $11::timestamptz))
         order by t.observed_at desc
         limit 1
      ) t
     order by t.observed_at desc
     limit 1
  ) s on coalesce(s.confidentiality, 'noRestriction') = 'noRestriction'
  group by k.source_feed, k.site_id, k.name, k.operator, k.street, k.house_number, k.postcode, k.city,
           k.source_version, k.lat, k.lon, k.distance_m
  order by k.distance_m asc, k.site_id asc`
	legacyReads["api_charging_register_address"] = `  select q.source_feed, q.site_id, pc.postcode_seen, named.known, named.nearest_m, near.located_near
    from unnest($1::text[], $2::text[], $3::text[]) as q(source_feed, site_id, town)
    join (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) s on s.source_feed = q.source_feed and s.site_id = q.site_id and s.closed_at is null and coalesce(s.confidentiality, 'noRestriction') = 'noRestriction'
    cross join lateral (
      select case when s.postcode is null or btrim(s.postcode) = '' then null else exists (
               select 1 from (SELECT source_feed,
    site_id,
    source_version,
    name,
    operator,
    operator_id,
    street,
    house_number,
    postcode,
    city,
    country_code,
    geom,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    road,
    point_count,
    confidentiality,
    operator_tokens,
    street_key,
    house_key,
    access_private
   FROM charging_sites
  WHERE ((deleted_at IS NULL) AND (NOT access_private))) o
                where o.closed_at is null
                  and o.source_feed = any($5::text[])
                  and coalesce(o.confidentiality, 'noRestriction') = 'noRestriction'
                  and ST_DWithin(o.geom, s.geom, $4::float8)
                  and o.postcode = s.postcode
                  and charging_street_key(o.street) <> charging_street_key(s.street)) end as postcode_seen) pc




    cross join lateral (
      select count(*) > 0 as known, min(ST_Distance(g.geom, s.geom)) as nearest_m
        from gazetteer_places g
       where pc.postcode_seen = false
         and q.town <> ''
         and (g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' or g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'district') and g.geom is not null
         and (g.name_norm = q.town or g.name_norm like q.town || ' %')) named
    left join lateral (
      select g.name as located_near from gazetteer_places g
       where pc.postcode_seen = false
         and g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.geom is not null
       order by g.geom <-> s.geom limit 1) near on true`
	legacyReads["api_charging_site_addresses"] = `select a.i, a.id, a.street, a.house_number, a.postcode, a.place, a.land, a.lat, a.lon, a.extract_date, a.literal_score, a.by_postcode
    from (
      (select 1 as i, o.id::text as id, o.street, o.house_number, o.postcode, o.place, o.land,
              st_y(o.geom::geometry) as lat, st_x(o.geom::geometry) as lon,
              to_char(o.extract_date, 'YYYY-MM-DD') as extract_date,
              (o.street_norm = $1::text)::int as literal_score, true as by_postcode
         from osm_addresses o
        where o.closed_at is null and $6::text <> ''
          and o.street_norm = any(array[$1::text, $2::text])
          and o.postcode = $6::text
          and o.house_number_norm = $5::text
        order by literal_score desc, o.osm_type, o.osm_id
        limit $7::int)
      union all
      (select 1 as i, o.id::text as id, o.street, o.house_number, o.postcode, o.place, o.land,
              st_y(o.geom::geometry) as lat, st_x(o.geom::geometry) as lon,
              to_char(o.extract_date, 'YYYY-MM-DD') as extract_date,
              (o.street_norm = $1::text)::int * 2 + (o.place_norm = $3::text)::int as literal_score, false as by_postcode
         from osm_addresses o
        where o.closed_at is null
          and o.street_norm = any(array[$1::text, $2::text])
          and o.place_norm = any(array[$3::text, $4::text])
          and o.house_number_norm = $5::text
        order by literal_score desc, o.osm_type, o.osm_id
        limit $7::int)) a`
	//nolint:misspell // materialized is the SQL keyword
	legacyReads["api_charging_status_liveness"] = `  with extent as materialized (
    select f.feed_key,
           greatest(
             (select case when kept.feed_key is not null then kept.max_items
                          else (select max(x.items_seen)
                                  from feed_runs x
                                 where x.feed_key = f.feed_key
                                   and x.status = any($2::text[])
                                   and x.started_at > $3::timestamptz - make_interval(hours => 48)
                                   and x.started_at >= $3::timestamptz - make_interval(days => $4::int)
                                   and x.started_at <= $3::timestamptz) end
                from (select 1) one
                left join feed_run_extents kept
                  on kept.feed_key = f.feed_key
                 and kept.computed_at <= $3::timestamptz
                 and (kept.max_started_at > $3::timestamptz - make_interval(hours => 48)
                      or kept.max_items is null)
                 and $4::int >= 2
                 and $2::text[] = array['ok', 'unchanged', 'degraded']::text[]
                 and kept.version = coalesce((select v.version from feed_versions v where v.ledger = 'feed' and v.key = f.feed_key), 0)),
             (select m.items_seen
                from feed_runs m
               where m.feed_key = f.points_from
                 and m.status = 'ok'
                 and m.items_seen is not null
                 and m.started_at >= $3::timestamptz - make_interval(days => $4::int)
                 and m.started_at <= $3::timestamptz
               order by m.started_at desc limit 1)) as points
      from unnest($1::text[], $6::text[]) as f(feed_key, points_from)),
  ledger as materialized (
    select e.feed_key,
           landed.last_conclusive_at,
           case when jump.started_at > coalesce(closed.closed_at, '-infinity'::timestamptz) then jump.started_at end as gap_at
      from extent e
      left join lateral (
        select x.finished_at as last_conclusive_at
          from feed_runs x
         where x.feed_key = e.feed_key
           and x.finished_at is not null
           and x.status = any($2::text[])
           and (x.status <> 'degraded' or x.items_seen is not null)
           and tstzrange($3::timestamptz - make_interval(days => $4::int), $3::timestamptz, '[]') @> x.started_at
         order by x.finished_at desc limit 1) landed on true
      left join lateral (
        select c.started_at as closed_at
          from feed_runs c
         where c.feed_key = e.feed_key
           and coalesce(lower(btrim(c.exchange_protocol)) like 'snapshot%', false)
           and c.status = any($2::text[])
           and c.items_seen is not null
           and e.points > 0
           and c.items_seen >= e.points * 0.95
           and c.started_at >= $3::timestamptz - make_interval(days => $4::int)
           and c.started_at <= $3::timestamptz
         order by c.started_at desc limit 1) closed on true
      left join lateral (
        select j.started_at
          from feed_runs j
         where j.feed_key = e.feed_key
           and j.jumped
           and j.started_at >= $3::timestamptz - make_interval(days => $4::int)
           and j.started_at <= $3::timestamptz
         order by j.started_at desc limit 1) jump on true),
  restating as materialized (
    select array_agg(c.id) as ids
      from extent e
      join feed_runs c on c.feed_key = e.feed_key
     where c.started_at > $3::timestamptz - make_interval(secs => $5::int)
       and c.started_at >= $3::timestamptz - make_interval(days => $4::int)
       and c.started_at <= $3::timestamptz
       and c.status = any($2::text[])
       and c.items_seen is not null
       and e.points > 0
       and c.items_seen >= e.points * 0.95),
  moved as materialized (
    select t.source_feed,
           max(t.stated_at) filter (where t.is_delta) as moved_at,
           max(t.observed_at) filter (where t.is_delta) as received_at,
           max(t.stated_at) filter (where not t.is_delta and t.source_run_id = any((select rs.ids from restating rs)::bigint[])) as restated_at,
           max(t.observed_at) filter (where not t.is_delta and t.source_run_id = any((select rs.ids from restating rs)::bigint[])) as restated_received_at
      from charging_status_runs t
     where t.observed_at > $3::timestamptz - make_interval(secs => $5::int)
       and t.observed_at <= $3::timestamptz
     group by t.source_feed)
  select f.feed_key,
         moved.moved_at,
         moved.received_at,
         moved.restated_at,
         moved.restated_received_at,
         ledger.gap_at,
         ledger.last_conclusive_at,
         cur.validator,
         since.validator_since
    from unnest($1::text[]) as f(feed_key)
    left join moved on moved.source_feed = f.feed_key
    left join ledger on ledger.feed_key = f.feed_key
    left join lateral (
      select coalesce(r.etag, r.last_modified) as validator
        from feed_runs r
       where r.feed_key = f.feed_key
         and coalesce(r.etag, r.last_modified) is not null
         and r.started_at >= $3::timestamptz - make_interval(days => $4::int)
       order by r.started_at desc limit 1) cur on true
    left join lateral (
      select min(h.started_at) as validator_since
        from feed_runs h
       where h.feed_key = f.feed_key
         and coalesce(h.etag, h.last_modified) is not null
         and h.started_at >= $3::timestamptz - make_interval(days => $4::int)
         and h.started_at > coalesce((
               select max(m.started_at) from feed_runs m
                where m.feed_key = f.feed_key
                  and coalesce(m.etag, m.last_modified) is not null
                  and m.started_at >= $3::timestamptz - make_interval(days => $4::int)
                  and coalesce(m.etag, m.last_modified) is distinct from cur.validator),
             '-infinity'::timestamptz)) since on true`
	legacyReads["api_feed_check"] = `select m.checked_at, m.cadence_s, m.paused from (
  select f.feed_key, f.checked_at, f.cadence_s, coalesce(d.paused, false) as paused
    from feed_checks f
    left join control.data_status d on d.ledger = 'feed' and d.key = f.feed_key
   where f.feed_key = any((array[$1::text])::text[])) m`
	legacyReads["api_location_gemeinde"] = `select m.pref = 1, m.admin_code, m.name, m.kreis, m.bezirk, m.source_id from (
  with pt as (select ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography as g),
  gem as (
    select * from (
      (select 1 as pref, g.admin_code, g.name, g.source_id
         from gazetteer_places g, pt
        where $1::text is not null
          and g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$' and g.name_norm = $1::text
          and ST_DWithin(g.geom, pt.g, $4)
        order by g.geom <-> pt.g
        limit 1)
      union all
      (select 2 as pref, g.admin_code, g.name, g.source_id
         from gazetteer_places g, pt
        where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$'
          and ST_DWithin(g.geom, pt.g, $5)
        order by g.geom <-> pt.g
        limit 1)
    ) x
    order by pref
    limit 1)
  select gem.source_id, gem.pref::int as pref, gem.admin_code, gem.name,
         (select d.name from gazetteer_places d
           where d.closed_at is null and d.source_id = 'bkg_gvisys' and d.kind = 'district'
             and d.admin_code = left(gem.admin_code, 5)
           limit 1) as kreis,
         (select r.name from gazetteer_places r
           where r.closed_at is null and r.source_id = 'bkg_gvisys' and r.kind = 'admin'
             and r.admin_code = left(gem.admin_code, 3)
           limit 1) as bezirk
    from gem) m where m.source_id = any($6::text[])`
	legacyReads["api_motorway_counts"] = `  select e.road, e.kind,



         (count(distinct k.site) filter (where not nf.off))::int as n,
         (count(distinct k.site) filter (where e.abnormal_traffic_type is not null and not nf.off))::int as jams,



         (count(distinct k.site) filter (where nf.off and ((s.future or coalesce(s.starts_at > $3::timestamptz, false)) or not coalesce(not autobahn_validity_overlaps(rr.raw->'description', $3::timestamptz, 'infinity'::timestamptz), false))))::int as announced,
         min(s.starts_at) filter (where s.starts_at > $3::timestamptz) as announced_from

  from (
      select distinct on (u.road, u.ext_id)
             u.source, u.road, u.filed_road, u.kind, u.ext_id, u.title, u.subtitle, u.abnormal_traffic_type,
             u.delay_s, u.extent, u.geom, u.raw, u.raw_hash, u.observed_at
        from (
          select a.source, a.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 0 as foreign_rank
            from autobahn_events a
            join unnest($1::text[], $2::timestamptz[]) as p(road, t) on p.road = a.road and p.t = a.observed_at
           where a.road = any($1::text[])
             and (true is not true or (select p is null or p ~ ('(^|[^0-9])' || substr(a.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))
          union all
          select a.source, q.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 1 as foreign_rank
            from (select c.road,
                         coalesce((select p.t from unnest($1::text[], $2::timestamptz[]) as p(road, t) where p.road = c.road limit 1),
                                  (select max(x.observed_at) from autobahn_events x
                                    where x.road = c.road and x.observed_at > $3::timestamptz - interval '30 minutes' and x.observed_at <= $3::timestamptz)) as t
                    from (select distinct c0.road from autobahn_events c0
                            join (select lt0.road, max(lt0.observed_at) as t from autobahn_events lt0
                                   where lt0.observed_at > $3::timestamptz - interval '30 minutes' and lt0.observed_at <= $3::timestamptz
                                   group by lt0.road) lt on lt.road = c0.road and lt.t = c0.observed_at
                           where true is true
                             and exists (select 1 from unnest($1::text[]) as q0(road)
                                          where q0.road <> c0.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q0.road, 2) || '([^0-9]|$)') from substring(coalesce(c0.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))) c) ft
            join autobahn_events a on a.road = ft.road and a.observed_at = ft.t
            join unnest($1::text[]) as q(road) on q.road <> a.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p)
        ) u
       order by u.road, u.ext_id, u.foreign_rank, u.filed_road) e
  cross join lateral (select autobahn_raw_resolve(e.raw, e.raw_hash, e.ext_id, e.observed_at) as raw offset 0) rr
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
  group by e.road, e.kind`
	legacyReads["api_motorway_events"] = `  with f as (
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
      select distinct on (u.road, u.ext_id)
             u.source, u.road, u.filed_road, u.kind, u.ext_id, u.title, u.subtitle, u.abnormal_traffic_type,
             u.delay_s, u.extent, u.geom, u.raw, u.raw_hash, u.observed_at
        from (
          select a.source, a.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 0 as foreign_rank
            from autobahn_events a
            join unnest($1::text[], $2::timestamptz[]) as p(road, t) on p.road = a.road and p.t = a.observed_at
           where a.road = any($1::text[])
             and (true is not true or (select p is null or p ~ ('(^|[^0-9])' || substr(a.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))
          union all
          select a.source, q.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 1 as foreign_rank
            from (select c.road,
                         coalesce((select p.t from unnest($1::text[], $2::timestamptz[]) as p(road, t) where p.road = c.road limit 1),
                                  (select max(x.observed_at) from autobahn_events x
                                    where x.road = c.road and x.observed_at > $6::timestamptz - interval '30 minutes' and x.observed_at <= $6::timestamptz)) as t
                    from (select distinct c0.road from autobahn_events c0
                            join (select lt0.road, max(lt0.observed_at) as t from autobahn_events lt0
                                   where lt0.observed_at > $6::timestamptz - interval '30 minutes' and lt0.observed_at <= $6::timestamptz
                                   group by lt0.road) lt on lt.road = c0.road and lt.t = c0.observed_at
                           where true is true
                             and exists (select 1 from unnest($1::text[]) as q0(road)
                                          where q0.road <> c0.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q0.road, 2) || '([^0-9]|$)') from substring(coalesce(c0.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))) c) ft
            join autobahn_events a on a.road = ft.road and a.observed_at = ft.t
            join unnest($1::text[]) as q(road) on q.road <> a.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p)
        ) u
       order by u.road, u.ext_id, u.foreign_rank, u.filed_road) e








    cross join lateral (select autobahn_raw_resolve(e.raw, e.raw_hash, e.ext_id, e.observed_at) as raw offset 0) rr
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
    select f.*,
           case when kind = 'warning' and abnormal_traffic_type is not null then 0
                when kind = 'closure' then 1
                when kind = 'warning' then 2
                else 3 end as tier
    from f
    where rn = 1
  ), h as (
    select g.*,
           row_number() over (
             partition by road, tier
             order by case abnormal_traffic_type when 'QUEUING_TRAFFIC' then 0 when 'STATIONARY_TRAFFIC' then 1 when 'HEAVY_TRAFFIC' then 2 when 'SLOW_TRAFFIC' then 3 else 4 end,
                      delay_s desc nulls last, title, ext_id
           ) as turn
    from g
  ), l as (
    select h.*,

           case when tier < 4 and row_number() over (partition by road order by tier, turn) = 1 then 0 else 1 end as lead
    from h
  )
  select road, kind, ext_id, title, subtitle, abnormal_traffic_type, delay_s, extent, lat, lon,
         start_ts, starts_at, description, is_blocked, avg_speed, segments, filed_road, site_ext_ids, site_extents, route_recommendation
  from l
  order by lead, tier, turn, array_position($1::text[], road)
  limit $4 offset $5`
	legacyReads["api_motorway_jam_history"] = `select min(hour_start) as oldest from autobahn_jam_hours`
	legacyReads["api_motorway_jam_ticks"] = `  with q as (
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
  order by q.grp, e.observed_at`
	legacyReads["api_motorway_jam_usual"] = `  with q as (
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
    select m.*,
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
  order by q.i`
	legacyReads["api_motorway_latest_ticks"] = `  select road, max(observed_at) as t
  from autobahn_events
  where road = any($1::text[]) and observed_at > $2::timestamptz - interval '30 minutes' and observed_at <= $2::timestamptz
  group by road`
	legacyReads["api_motorway_measured"] = `  with cross_section as (
    select site_id, road, status, geom, measured_at
      from (SELECT s.site_id,
    s.road,
    s.direction,
    s.section,
    s.geom,
    t.status,
    t.measured_at,
    t.status_since,
    t.last_seen_at
   FROM (los_sites s
     JOIN los_state t ON ((t.site_id = s.site_id)))
  WHERE ((s.closed_at IS NULL) AND (s.carriageway = 'hfb'::text) AND (s.road IS NOT NULL) AND (s.geom IS NOT NULL) AND (COALESCE(s.confidentiality, 'noRestriction'::text) = 'noRestriction'::text) AND (COALESCE(t.confidentiality, 'noRestriction'::text) = 'noRestriction'::text)))
     where road = any($1::text[])
       and last_seen_at >= $2
       and status in ('freeFlow', 'heavy', 'congested', 'impossible')
  ), by_lane as (
    select distinct on (l.site_id) l.site_id, l.road, l.status, l.geom, l.measured_at
      from (SELECT t.lane_id,
    t.site_id,
    c.road,
    c.direction,
    c.geom,
    t.status,
    t.measured_at,
    t.last_seen_at
   FROM ((los_lane_state t
     JOIN los_lane_sites s ON ((s.lane_id = t.lane_id)))
     JOIN los_sites c ON ((c.site_id = t.site_id)))
  WHERE ((s.closed_at IS NULL) AND (c.closed_at IS NULL) AND (c.carriageway = 'hfb'::text) AND (c.road IS NOT NULL) AND (c.geom IS NOT NULL) AND (COALESCE(s.confidentiality, 'noRestriction'::text) = 'noRestriction'::text) AND (COALESCE(t.confidentiality, 'noRestriction'::text) = 'noRestriction'::text) AND (COALESCE(c.confidentiality, 'noRestriction'::text) = 'noRestriction'::text))) l
     where l.road = any($1::text[])
       and l.last_seen_at >= $2
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
        select m.name from (SELECT road_number AS road,
    lcd,
    tcd,
    stcd,
    junction_no,
    name,
    neg_off_lcd,
    pos_off_lcd,
    geom
   FROM tmc_locations t
  WHERE ((cid = 58) AND (tabcd = 1) AND (road_number ~ '^A[1-9][0-9]{0,2}$'::text))) m
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
   order by f.road`
	legacyReads["api_motorway_plans_now"] = `  select e.feed_key, e.publication_id, e.source_id, e.road_number, e.event_type,
         jsonb_path_query_first(o.n, '$.generalPublicComment[*] ? (@.commentExtension[0].commentExtended[0].commentType2[0]."#text" == "roadworksName").comment[0].values[0].value[0]."#text"') #>> '{}' as name,
         jsonb_path_query_first(o.n, '$.groupOfLocations[0].**.roadName[0].values[0].value[0]."#text"') #>> '{}' as section,
         jsonb_path_query_first(o.n, '$.cause[0].causeDescription[0].values[0].value[0]."#text"') #>> '{}' as cause,
         coalesce(lower(w.x), e.overall_start) as window_from,
         case when w.x is null then e.overall_end else upper(w.x) end as window_to,
         (count(*) over (partition by e.road_number))::int as on_road
    from (SELECT id,
    feed_key,
    source_id,
    publication_id,
    version,
    publisher,
    event_type,
    road_number,
    referent_from,
    referent_to,
    distance_from_m,
    distance_to_m,
    geom,
    feed_ts,
    overall_start,
    overall_end,
    payload,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_feed,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    closure_starts_at,
    closure_ends_at,
    valid_windows,
    suspended,
    entry_static,
    land_code
   FROM road_events
  WHERE (deleted_at IS NULL)) e
    cross join lateral (
      select coalesce(e.payload -> 'situationExtension' -> 0 -> 'situationExtended' -> 0 -> 'overallSituation' -> 0,
                      e.payload -> 'situationRecord' -> 0) as n
    ) o
    left join lateral (select x from unnest(e.valid_windows) x where x @> $3::timestamptz limit 1) w on true
   where e.feed_key = any($1::text[]) and e.closed_at is null and e.road_number = any($2::text[])
     and coalesce(e.confidentiality, 'noRestriction') = 'noRestriction'
     and road_event_in_force(e.overall_start, e.overall_end, e.payload, e.valid_windows, $3::timestamptz)
   order by array_position($2::text[], e.road_number), coalesce(lower(w.x), e.overall_start) nulls last, e.publication_id
   limit $4`
	legacyReads["api_osm_fuel_unpriced"] = `select c.name, c.brand, c.street, c.house_number, c.postcode, c.place, c.lat, c.lon, round(c.distance_m)::int, c.source_id from (
  select p.name, p.brand, p.street, p.house_number, p.postcode, p.place,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         st_distance(p.geom, st_point($1, $2)::geography) as distance_m, p.source_id, p.geom as held_geom
    from osm_pois p
   where p.closed_at is null and p.category = 'amenity' and p.category_value = 'fuel'
     and exists (select 1 from osm_boundary_de de_b where st_covers(de_b.geom, p.geom::geometry))
     and p.name !~* '(propan|fl(ü|ue)ssiggas(handel|vertrieb)|gashandel|gasflaschen)'
     and st_dwithin(p.geom, st_point($1, $2)::geography, $3)
     and p.source_id = any($4::text[])
     and (($6)::text is null or coalesce(p.brand, '') ilike '%' || ($6) || '%' or p.name ilike '%' || ($6) || '%')
     and (($7)::text is null or p.name ilike '%' || ($7) || '%' or coalesce(p.brand, '') ilike '%' || ($7) || '%')
   order by p.geom <-> st_point($1, $2)::geography
   limit (40)) c where not exists (select 1 from fuel_stations s where s.deleted_at is null and s.geom is not null and st_dwithin(s.geom, c.held_geom, 300)) order by c.distance_m limit $5`
	legacyReads["api_parking_nearby_osm"] = `  select s.ord::int as site, x.amenity, x.name, x.metres, x.lat, x.lon, x.extract_date
    from unnest($1::float8[], $2::float8[], $3::float8[]) with ordinality as s(lon, lat, radius, ord)
    cross join lateral (
      select distinct on (k.amenity) k.amenity, k.name, k.metres, k.lat, k.lon, k.extract_date
        from (
          select case when o.category || '=' || o.category_value in ('amenity=toilets') then 'toilet' when o.category || '=' || o.category_value in ('amenity=shower') then 'shower' when o.category || '=' || o.category_value in ('amenity=restaurant') then 'restaurant' when o.category || '=' || o.category_value in ('amenity=fast_food') then 'snack' when o.category || '=' || o.category_value in ('amenity=cafe') then 'cafe' when o.category || '=' || o.category_value in ('shop=convenience', 'shop=kiosk', 'shop=supermarket') then 'shop' when o.category || '=' || o.category_value in ('amenity=drinking_water') then 'drinking_water' end as amenity, o.name,
                 round(st_distance(o.geom, st_setsrid(st_makepoint(s.lon, s.lat), 4326)::geography))::int as metres,
                 st_y(o.geom::geometry) as lat, st_x(o.geom::geometry) as lon, o.extract_date::text as extract_date
            from osm_pois o
           where o.closed_at is null and (o.category || '=' || o.category_value) in ('amenity=toilets', 'amenity=shower', 'amenity=restaurant', 'amenity=fast_food', 'amenity=cafe', 'shop=convenience', 'shop=kiosk', 'shop=supermarket', 'amenity=drinking_water')
   and o.source_id = any($4::text[])
             and st_dwithin(o.geom, st_setsrid(st_makepoint(s.lon, s.lat), 4326)::geography, s.radius)
        ) k
       order by k.amenity, k.metres, k.name
    ) x
   order by s.ord, x.metres, x.amenity`
	legacyReads["api_parking_osm_near"] = `  select o.osm_type || '/' || o.osm_id::text as id, o.name, o.operator, o.street, o.house_number, o.postcode, o.place,
         ST_Y(o.geom::geometry) as lat, ST_X(o.geom::geometry) as lon,
         o.extract_date::text as extract_date,
         ST_Distance(o.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) as distance_m
  from osm_pois o
  where o.closed_at is null
    and o.category = 'amenity'
    and o.category_value = 'parking'
    and ($4::text is null or o.name ~* $4::text)
    and o.name !~* $6::text
    and o.source_id = any($7::text[])
    and (cardinality(array_remove(array(select lower(btrim(v)) from unnest(string_to_array(o.access, ';')) v), '')) = 0 or not (array_remove(array(select lower(btrim(v)) from unnest(string_to_array(o.access, ';')) v), '') <@ array['private', 'no', 'customers', 'permit', 'residents', 'delivery', 'agricultural', 'forestry', 'military', 'emergency', 'employees', 'staff']::text[]))
    and ST_DWithin(o.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
  order by distance_m asc, o.id asc
  limit $5`
	legacyReads["api_parking_road_context"] = `  select k.kind, x.source_feed, x.feature_id, x.variant, x.name, x.ref, x.detail_text, x.props,
         st_y(x.at) as lat, st_x(x.at) as lon, x.distance_m, x.last_seen_at, x.total, x.variants, x.parts_in, x.flagged
    from unnest($3::text[], $4::float8[], $5::int[], $7::text[], $8::boolean[], $9::boolean[], $10::text[]) with ordinality as k(kind, radius_m, lim, prefer, one_per_name, count_parts, flag, ord)
    cross join lateral (
      with m as (
        select w.source_feed, w.feature_id, w.variant, w.name, w.ref, w.detail_text, w.props, w.last_seen_at,
               st_closestpoint(w.geom, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)) as at,
               ST_Distance(w.geom::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography) as distance_m,

               case when k.count_parts
                    then (select count(*)::int from st_dump(w.geom) d where ST_DWithin(d.geom::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, k.radius_m))
                    else 1 end as parts_in
          from (SELECT source_feed,
    feature_id,
    kind,
    variant,
    name,
    ref,
    detail_text,
    props,
    geom,
    anchor,
    last_seen_at
   FROM road_context_features
  WHERE ((closed_at IS NULL) AND answerable)) w
         where w.kind = k.kind
           and w.source_feed = any($11::text[])
           and w.geom && st_expand(st_setsrid(st_makepoint($1::float8, $2::float8), 4326), k.radius_m / 111320.0 / greatest(cos(radians($2::float8)), 0.2))
           and ST_DWithin(w.geom::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, k.radius_m)

           and (w.props->>'valid_from' is null or w.props->>'valid_from' <= to_char($6::timestamptz at time zone 'Europe/Berlin', 'YYYY-MM-DD'))
           and (w.props->>'valid_to' is null or w.props->>'valid_to' >= to_char($6::timestamptz at time zone 'Europe/Berlin', 'YYYY-MM-DD'))
           and exists (select 1 from feed_checks c
                        where c.feed_key = w.source_feed
                          and (c.checked_at > $6::timestamptz - make_interval(secs => 3 * coalesce(c.cadence_s, 86400)) or (c.paused and c.checked_at is not null)))
      ),




      n as (
        select distinct on (case when k.one_per_name then m.name else m.source_feed || ':' || m.feature_id end) m.*
          from m
         order by case when k.one_per_name then m.name else m.source_feed || ':' || m.feature_id end,
                  (m.variant is not distinct from k.prefer) desc, m.distance_m, m.props->>'valid_to' desc nulls first, m.source_feed, m.feature_id
      )
      select n.*, (select sum(m.parts_in)::int from m) as total,
             (select jsonb_object_agg(v.variant, v.n) from (select variant, sum(parts_in)::int as n from m group by variant) v) as variants,

             case when k.flag is null then null else (select count(*)::int from m where m.props->>k.flag = 'true') end as flagged
        from n
       order by (n.variant is not distinct from k.prefer) desc, n.distance_m, n.source_feed, n.feature_id
       limit k.lim
    ) x
   order by k.ord, (x.variant is not distinct from k.prefer) desc, x.distance_m, x.source_feed, x.feature_id`
	legacyReads["api_place_address_by_place"] = `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date,
         (street_norm = $1)::int * 2 + (place_norm = $3)::int as literal_score
    from osm_addresses
   where closed_at is null
   and source_id = any($7::text[])
     and street_norm = any(array[$1::text, $2::text])
     and place_norm = any(array[$3::text, $4::text])
     and house_number_norm = $5
   order by literal_score desc, osm_type, osm_id
   limit $6`
	legacyReads["api_place_address_by_place_prefix"] = `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date,
         (street_norm = $1)::int as literal_score
    from osm_addresses
   where closed_at is null
   and source_id = any($6::text[])
     and street_norm = any(array[$1::text, $2::text])
     and house_number_norm = $4
     and place_norm ~>=~ ($3::text || ' ')
     and place_norm ~<~ ($3::text || '!')
     and starts_with(place_norm, $3 || ' ')
   order by literal_score desc, place, osm_type, osm_id
   limit $5`
	legacyReads["api_place_address_by_postcode"] = `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date,
         (street_norm = $1)::int as literal_score
    from osm_addresses
   where closed_at is null
   and source_id = any($6::text[])
     and street_norm = any(array[$1::text, $2::text])
     and postcode = $3 and house_number_norm = $4
   order by literal_score desc, osm_type, osm_id
   limit $5`
	legacyReads["api_place_address_street_by_place"] = `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date, house_number_norm,
         (street_norm = $1)::int * 2 + (place_norm = $3)::int as literal_score
    from osm_addresses
   where closed_at is null
   and source_id = any($5::text[])
     and street_norm = any(array[$1::text, $2::text])
     and place_norm = any(array[$3::text, $4::text])
   order by literal_score desc, osm_type, osm_id
   limit 2000`
	legacyReads["api_place_address_street_by_postcode"] = `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date, house_number_norm,
         (street_norm = $1)::int as literal_score
    from osm_addresses
   where closed_at is null
   and source_id = any($4::text[])
     and street_norm = any(array[$1::text, $2::text])
     and postcode = $3
   order by literal_score desc, osm_type, osm_id
   limit 2000`
	legacyReads["api_place_addresses_loaded"] = `select exists (select 1 from osm_addresses where closed_at is null) as loaded`
	//nolint:misspell // materialized is the SQL keyword
	legacyReads["api_place_candidates"] = `  -- #1215 review: the largest Gemeinde named exactly as asked — a prefix-
  -- qualified namesake is promoted only when it is at least as large, so
  -- "Homburg" asks about Bad Homburg v.d. Hoehe while "Ulm" (Neu-Ulm) and
  -- "Essen" (Bad Essen) still answer alone. One btree read, computed once.
  with exact as materialized (
    select coalesce(max(population), 0) as population
      from gazetteer_places
     where closed_at is null and name_norm in ($1, $2) and admin_code ~ '^[0-9]{8}$'
  ),
  -- #1264 review: the candidate ROWS, as a union of reads each served by an
  -- index of its own, so no read of this statement scans the table. One OR
  -- over every arm cost more than a sequential scan on the planner's own
  -- estimate (80 000 rows: ~835 ms, Seq Scan), the qualifier patterns going
  -- through the trigram GIN at ~125 ms per array. Now:
  --  * the trigram GIN on name_norm for the fuzzy match, with the suffix rule
  --    ("<query> <more>") as byte ranges on the text_pattern_ops btree
  --    (migration 0275), one flat arm per spelling so each is a BitmapOr leg;
  --  * the partial trigram GIN on lower(name_en), one read per spelling
  --    (an OR of the two was priced as a scan);
  --  * the prefix-qualified namesakes ("bad homburg", "bad homburg %") as one
  --    byte-range probe per pattern on the same btree: a pattern carries no
  --    wildcard but its trailing " %" (a normalizeName fold is letters,
  --    digits and single spaces), so "<p>" is the range [p, p || chr(1)) and
  --    "<p> %" is [p + " ", p + "!") — exactly the rows LIKE matched.
  -- The rows are then read once by primary key, never joined by a hash over
  -- the table.
  hits as (
    select id from gazetteer_places
     where closed_at is null
       and (name_norm % $1 or name_norm % $2
            or ($5::boolean and name_norm like $1 || ' %' and population > 0 and admin_code ~ '^[0-9]{8}$')
            or ($5::boolean and name_norm like $2 || ' %' and population > 0 and admin_code ~ '^[0-9]{8}$'))
    -- One read per spelling: the partial index on lower(name_en) serves each
    -- alone, and an OR of the two was planned as a scan (measured, "homburg"
    -- with "hoemburg").
    union
    select id from gazetteer_places where closed_at is null and name_en is not null and lower(name_en) % $1
    union
    select id from gazetteer_places where closed_at is null and name_en is not null and lower(name_en) % $2
    union
    -- #1215: a namesake SMALLER than the exact-named Gemeinde (the exact
    -- CTE) is not admitted, so "Ulm" is not "Ulm or Neu-Ulm?".
    select g.id
      from (select distinct pat from unnest($8::text[] || $9::text[]) as u(pat)) as p
      join gazetteer_places g
        on g.name_norm ~>=~ replace(p.pat, '%', '')
       and g.name_norm ~<~ (case when right(p.pat, 1) = '%' then left(p.pat, -2) || '!' else p.pat || chr(1) end)
     where $5::boolean and g.closed_at is null
       and g.population > 0 and g.population >= (select population from exact) and g.admin_code ~ '^[0-9]{8}$'
  ),
  scored as (
    select id, name_norm,
           greatest(
             similarity(name_norm, $1),
             coalesce(similarity(lower(name_en), $1), 0),
             similarity(name_norm, $2) - $3::double precision,
             coalesce(similarity(lower(name_en), $2), 0) - $3::double precision
           ) as score,
           -- #1215: the SAME Gemeinde rule, for a PREFIX qualifier ("Bad
           -- Homburg v d Hohe", "Neu Isenburg") rather than only a suffix one.
           -- A town named "<prefix word> <query>[ <more>]" gets the identical
           -- promotion, from the closed list PREFIX_QUALIFIER_WORDS ($8 for $1,
           -- $9 for $2, LIKE patterns built by prefixQualifiedPatterns), so a
           -- smaller exact-named town ("Homburg", Saarland) and a larger
           -- qualified namesake score a tie and the margin rule asks; a
           -- namesake SMALLER than the exact-named Gemeinde (the exact CTE) is
           -- not promoted. Here the LIKE only scores rows already read.
           case when $5::boolean and population > 0 and admin_code ~ '^[0-9]{8}$' then
             case when name_norm like $1 || ' %' then 1::double precision
                  when name_norm like $2 || ' %' then 1::double precision - $3::double precision
                  when population < (select population from exact) then 0::double precision
                  when name_norm like any($8::text[]) then 1::double precision
                  when name_norm like any($9::text[]) then 1::double precision - $3::double precision
                  else 0::double precision end
           else 0::double precision end as qualified
      from gazetteer_places
     where id = any (array(select id from hits))
  ),
  folds as (
    select name_norm, max(score) as score, max(qualified) as qualified from scored group by name_norm
  )
  select g.id::text as id, g.kind, g.name, g.name_norm, g.name_en, g.admin_code, g.population, g.road, g.rank,
         g.source_id, g.external_id,
         st_y(g.geom::geometry) as lat,
         st_x(g.geom::geometry) as lon,
         greatest(f.score, case when g.kind in ('station', 'stop') then 0::double precision else f.qualified end) as score
    from gazetteer_places g
    join folds f on f.name_norm = g.name_norm
   where g.closed_at is null
     -- 2.4.5: the rows of the folded names fetched through the name_norm btree. Joined on folds alone, the planner
     -- read its trigram estimate (15 660 folds for "berlin", 2 008 real) and hash-joined a sequential scan of all
     -- 390 711 rows: 21 197 buffers and ~250 ms of every place question on prod, many times more when cold.
     and g.name_norm = any (array(select name_norm from folds))
     and ($6::text is null or (g.kind = $6::text and g.source_id = $7::text))
   and g.source_id = any($10::text[])
   order by score desc, g.rank desc, g.name asc, g.source_id asc, g.external_id asc
   limit $4`
	legacyReads["api_place_gazetteer_loaded"] = `select exists (select 1 from gazetteer_places) as loaded`
	legacyReads["api_place_junctions_named"] = `  select o.osm_type || '/' || o.osm_id::text as id, o.name, o.name_norm,
         ST_Y(o.geom::geometry) as lat, ST_X(o.geom::geometry) as lon,
         s.score
  from osm_pois o
  cross join lateral (
    select greatest(similarity(o.name_norm, $1),
                    similarity(regexp_replace(o.name_norm, '^(autobahnkreuz|autobahndreieck|autobahnanschlussstelle|anschlussstelle|kreuz|dreieck|ak|ad|as) ', ''), $1)) as score
  ) s
  where o.closed_at is null
    and o.category = 'highway'
    and o.category_value = 'motorway_junction'
   and o.source_id = any($4::text[])
    and s.score >= $2
  order by s.score desc, o.name asc, o.id asc
  limit $3`
	legacyReads["api_place_kreis_names"] = `  select g.admin_code, g.name
    from gazetteer_places g
   where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'district' and g.admin_code = any($1::text[])
   and g.source_id = any($2::text[])`
	legacyReads["api_place_lands_near"] = `  select p.i::int as i,
         coalesce(
           (select left(o.part_of, 2)
              from gazetteer_places o
             where o.id = (case when p.id ~ '^[0-9]{1,18}$' then p.id::bigint end)
               and o.closed_at is null and o.part_of is not null),
           (select left(k.admin_code, 2)
              from gazetteer_places k
             where k.closed_at is null and k.kind in ('stop', 'station')
               and k.admin_code ~ '^[0-9]{8}$'
               and ST_DWithin(k.geom, ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography, 1500)
             order by k.geom <-> ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography
             limit 1),
           (select left(g.admin_code, 2)
              from gazetteer_places g
             where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$'
               and ST_DWithin(g.geom, ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography, $3)
             order by g.geom <-> ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326)::geography
             limit 1)) as land
    from unnest($1::float8[], $2::float8[], $4::text[]) with ordinality as p(lon, lat, id, i)`
	//nolint:misspell // materialized is the SQL keyword
	legacyReads["api_place_poi_anywhere"] = `with p0 as materialized (
  select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, p.geom, p.osm_type, p.osm_id
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($7::text[])
     and true
     and (($5::text is null and $6::text[] is null) or p.category = $5::text or (p.category || '=' || p.category_value) = any($6::text[]))
     and (p.name_norm like '%' || $1 || '%' or p.brand ilike '%' || $2 || '%')
     and (greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) >= $3 or p.name_norm ~ ('\m' || $1 || '\M'))
   order by tier desc, score desc, p.name, p.osm_type, p.osm_id
   limit greatest($4, 60)
), h as (
  select p0.*, row_number() over (order by p0.tier desc, p0.score desc, p0.name, p0.osm_type, p0.osm_id) as rn
    from p0
)
select h.id, h.name, h.category, h.category_value, h.brand, h.operator,
         h.street, h.house_number, h.postcode, h.place, h.land, h.lat, h.lon, h.extract_date, h.score, h.tier, null::float8 as distance_m, (case when h.tier >= 6 and h.rn <= 60 then (select count(*) from (select 1 from osm_pois q
                                where q.closed_at is null and st_dwithin(q.geom, h.geom, 2000)
                                limit 8000) v)
                   else 0 end)::int as vicinity
  from h
 order by h.tier desc, h.score desc, vicinity desc, h.name, h.osm_type, h.osm_id
 limit $4`
	//nolint:misspell // materialized is the SQL keyword
	legacyReads["api_place_poi_by_place"] = `with p0 as materialized (
  select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, p.geom, p.osm_type, p.osm_id
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($9::text[])
     and p.place_norm = any(array[$3::text, $4::text])
     and (($7::text is null and $8::text[] is null) or p.category = $7::text or (p.category || '=' || p.category_value) = any($8::text[]))
     and (p.name_norm like '%' || $1 || '%' or p.brand ilike '%' || $2 || '%')
     and (greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) >= $5 or p.name_norm ~ ('\m' || $1 || '\M'))
   order by tier desc, score desc, p.name, p.osm_type, p.osm_id
   limit greatest($6, 60)
), h as (
  select p0.*, row_number() over (order by p0.tier desc, p0.score desc, p0.name, p0.osm_type, p0.osm_id) as rn
    from p0
)
select h.id, h.name, h.category, h.category_value, h.brand, h.operator,
         h.street, h.house_number, h.postcode, h.place, h.land, h.lat, h.lon, h.extract_date, h.score, h.tier, null::float8 as distance_m, (case when h.tier >= 6 and h.rn <= 60 then (select count(*) from (select 1 from osm_pois q
                                where q.closed_at is null and st_dwithin(q.geom, h.geom, 2000)
                                limit 8000) v)
                   else 0 end)::int as vicinity
  from h
 order by h.tier desc, h.score desc, vicinity desc, h.name, h.osm_type, h.osm_id
 limit $6`
	//nolint:misspell // materialized is the SQL keyword
	legacyReads["api_place_poi_by_place_fuzzy"] = `with p0 as materialized (
  select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, p.geom, p.osm_type, p.osm_id
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($10::text[])
     and p.place_norm = any(array[$3::text, $4::text])
     and (($7::text is null and $8::text[] is null) or p.category = $7::text or (p.category || '=' || p.category_value) = any($8::text[]))
     and (p.name_norm % $1 or p.brand % $2)
     and ($9::boolean or not ((p.category = 'amenity' and p.category_value in ('fuel', 'charging_station', 'car_wash', 'parking', 'parking_entrance', 'parking_space', 'motorcycle_parking', 'bicycle_parking')) or (p.category = 'building' and p.category_value in ('parking', 'garages', 'carport')) or (p.category = 'shop' and p.category_value in ('fuel'))))
     and (greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) >= $5)
   order by tier desc, score desc, p.name, p.osm_type, p.osm_id
   limit greatest($6, 60)
), h as (
  select p0.*, row_number() over (order by p0.tier desc, p0.score desc, p0.name, p0.osm_type, p0.osm_id) as rn
    from p0
)
select h.id, h.name, h.category, h.category_value, h.brand, h.operator,
         h.street, h.house_number, h.postcode, h.place, h.land, h.lat, h.lon, h.extract_date, h.score, h.tier, null::float8 as distance_m, (case when h.tier >= 6 and h.rn <= 60 then (select count(*) from (select 1 from osm_pois q
                                where q.closed_at is null and st_dwithin(q.geom, h.geom, 2000)
                                limit 8000) v)
                   else 0 end)::int as vicinity
  from h
 order by h.tier desc, h.score desc, vicinity desc, h.name, h.osm_type, h.osm_id
 limit $6`
	legacyReads["api_place_poi_category_by_place"] = `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, null::float8 as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($10::text[])
     and p.category = any($5::text[])
     and p.category_value = any($6::text[])
     and ($8::text[] is null or p.category_value <> all($8::text[]) or p.name_norm ~ $9::text)
     and p.place_norm = any(array[$3::text, $4::text])
   order by similarity(p.name_norm, $1) desc, word_similarity($1, p.name_norm) desc, p.osm_type, p.osm_id
   limit $7`
	legacyReads["api_place_poi_category_near"] = `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3, $4)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($11::text[])
     and p.category = any($6::text[])
     and p.category_value = any($7::text[])
     and ($9::text[] is null or p.category_value <> all($9::text[]) or p.name_norm ~ $10::text)
     and st_dwithin(p.geom, st_point($3, $4)::geography, $5)
   order by similarity(p.name_norm, $1) desc, word_similarity($1, p.name_norm) desc, distance_m asc, p.osm_type, p.osm_id
   limit $8`
	legacyReads["api_place_poi_category_nearest"] = `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3, $4)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($11::text[])
     and p.category = any($6::text[])
     and p.category_value = any($7::text[])
     and ($9::text[] is null or p.category_value <> all($9::text[]) or p.name_norm ~ $10::text)
     and st_dwithin(p.geom, st_point($3, $4)::geography, $5)
   order by distance_m asc, p.osm_type, p.osm_id
   limit $8`
	legacyReads["api_place_poi_near"] = `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3, $4)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($10::text[])
     and st_dwithin(p.geom, st_point($3, $4)::geography, $5)
     and (($8::text is null and $9::text[] is null) or p.category = $8::text or (p.category || '=' || p.category_value) = any($9::text[]))
     and (p.name_norm like '%' || $1 || '%' or p.brand ilike '%' || $2 || '%')
     and (greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) >= $6 or p.name_norm ~ ('\m' || $1 || '\M'))
   order by tier desc, score desc, distance_m asc, p.osm_type, p.osm_id
   limit $7`
	legacyReads["api_place_poi_near_fuzzy"] = `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1 or coalesce(lower(p.brand) = lower($2), false)) or p.name_norm ~ ('\m' || $1 || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1 || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3, $4)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
   and p.source_id = any($11::text[])
     and st_dwithin(p.geom, st_point($3, $4)::geography, $5)
     and (($8::text is null and $9::text[] is null) or p.category = $8::text or (p.category || '=' || p.category_value) = any($9::text[]))
     and (p.name_norm % $1 or p.brand % $2)
     and ($10::boolean or not ((p.category = 'amenity' and p.category_value in ('fuel', 'charging_station', 'car_wash', 'parking', 'parking_entrance', 'parking_space', 'motorcycle_parking', 'bicycle_parking')) or (p.category = 'building' and p.category_value in ('parking', 'garages', 'carport')) or (p.category = 'shop' and p.category_value in ('fuel'))))
     and (greatest(similarity(p.name_norm, $1), coalesce(similarity(p.brand, $2), 0)) >= $6)
   order by tier desc, score desc, distance_m asc, p.osm_type, p.osm_id
   limit $7`
	legacyReads["api_place_poi_place_words"] = `  select array(
           select distinct g.name_norm
             from gazetteer_places g
            where g.name_norm = any($1::text[])
              and g.kind in ('city', 'district', 'admin')
              and g.closed_at is null) as words,
         exists (
           select 1
             from gazetteer_places g
            where g.kind in ('city', 'district', 'admin')
              and g.closed_at is null) as loaded`
	legacyReads["api_place_road_rows"] = `  select id::text as id, kind, name, name_norm, name_en, admin_code, population, road, rank, source_id, external_id,
         st_y(geom::geometry) as lat,
         st_x(geom::geometry) as lon,
         1::double precision as score
    from gazetteer_places
   where closed_at is null and road = $1::text
     and source_id = any($3::text[])
   order by rank desc, name asc
   limit $2`
	legacyReads["api_place_shown_stops"] = `  select id::text as id, kind, name, name_norm, name_en, admin_code, population, road, rank, source_id, external_id,
         st_y(geom::geometry) as lat,
         st_x(geom::geometry) as lon,
         1::double precision as score
    from gazetteer_places
   where closed_at is null and kind = 'stop' and name_norm like '% ' || $1::text
     and source_id = any($3::text[])
   order by rank desc, name asc, source_id asc, external_id asc
   limit $2`
	legacyReads["api_place_street_by_place"] = `  with s as (
    select street, postcode, place, geom, extract_date, literal_score
    from (
      select street, postcode, place, geom, extract_date,
             (street_norm = $1)::int * 2 + (place_norm = $3)::int as literal_score
        from osm_addresses
       where closed_at is null
   and source_id = any($6::text[])
         and street_norm = any(array[$1::text, $2::text])
         and place_norm = any(array[$3::text, $4::text])
       limit 5000) x
  ), best as (
    select * from s where literal_score = (select max(literal_score) from s)
  ), c as (
    select *, ST_ClusterDBSCAN(geom::geometry, eps := 0.015, minpoints := 1) over () as cid from best
  ), k as (
    select cid, ST_Centroid(ST_Collect(geom::geometry)) as g, count(*)::int as n,
           mode() within group (order by postcode) as postcode,
           mode() within group (order by place) as place,
           mode() within group (order by street) as street,
           max(extract_date) as extract_date
    from c group by cid
  )
  select k.street, k.postcode, k.place, k.n as addresses, r.lat, r.lon, to_char(k.extract_date, 'YYYY-MM-DD') as extract_date
  from k
  cross join lateral (
    select st_y(c.geom::geometry) as lat, st_x(c.geom::geometry) as lon
    from c where c.cid = k.cid
    order by c.geom::geometry <-> k.g
    limit 1
  ) r
  order by k.n desc, r.lat, r.lon
  limit $5`
	legacyReads["api_place_street_by_postcode"] = `  with s as (
    select street, postcode, place, geom, extract_date, literal_score
    from (
      select street, postcode, place, geom, extract_date,
             (street_norm = $1)::int as literal_score
        from osm_addresses
       where closed_at is null
   and source_id = any($5::text[])
         and street_norm = any(array[$1::text, $2::text])
         and postcode = $3
       limit 5000) x
  ), best as (
    select * from s where literal_score = (select max(literal_score) from s)
  ), c as (
    select *, ST_ClusterDBSCAN(geom::geometry, eps := 0.015, minpoints := 1) over () as cid from best
  ), k as (
    select cid, ST_Centroid(ST_Collect(geom::geometry)) as g, count(*)::int as n,
           mode() within group (order by postcode) as postcode,
           mode() within group (order by place) as place,
           mode() within group (order by street) as street,
           max(extract_date) as extract_date
    from c group by cid
  )
  select k.street, k.postcode, k.place, k.n as addresses, r.lat, r.lon, to_char(k.extract_date, 'YYYY-MM-DD') as extract_date
  from k
  cross join lateral (
    select st_y(c.geom::geometry) as lat, st_x(c.geom::geometry) as lon
    from c where c.cid = k.cid
    order by c.geom::geometry <-> k.g
    limit 1
  ) r
  order by k.n desc, r.lat, r.lon
  limit $4`
	legacyReads["api_platform_access"] = `select m.element_id, m.element_kind = 'stop_place', m.stop_place_id, m.station_name, m.quay_name, m.plate_code, m.parent_quay_id, m.platform_height_cm::int, m.has_assessment, m.step_free_access, m.wheelchair_access, m.tactile_guidance, m.level_access_into_vehicle, m.provider_updated_at, 'openstation_netex'::text from (
  select a.element_id, a.element_kind, a.stop_place_id, a.eva_no, a.eva_nos, a.station_name, a.quay_type,
         a.quay_name, a.plate_code, a.parent_quay_id, a.platform_height_cm, a.has_assessment,
         a.step_free_access, a.wheelchair_access, a.tactile_guidance, a.level_access_into_vehicle,
         a.provider_updated_at
    from (SELECT element_id,
    element_kind,
    stop_place_id,
    eva_no,
    eva_nos,
    station_name,
    quay_type,
    quay_name,
    plate_code,
    parent_quay_id,
    platform_height_cm,
    has_assessment,
    mobility_impaired_access,
    step_free_access,
    wheelchair_access,
    lift_free_access,
    escalator_free_access,
    stair_free_access,
    ramp_free_access,
    tactile_guidance,
    audible_signals,
    visual_signs,
    guide_dog_access,
    level_access_into_vehicle,
    provider_updated_at,
    source_feed
   FROM station_accessibility
  WHERE (closed_at IS NULL)) a
   where a.source_feed = 'openstation_netex' and a.eva_nos && $1::text[] and a.source_feed = any($2::text[])
   order by a.stop_place_id, a.element_kind desc, a.element_id
   limit 2000) m`
	legacyReads["api_road_status_autobahn"] = `  with m as (
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
      select distinct on (u.road, u.ext_id)
             u.source, u.road, u.filed_road, u.kind, u.ext_id, u.title, u.subtitle, u.abnormal_traffic_type,
             u.delay_s, u.extent, u.geom, u.raw, u.raw_hash, u.observed_at
        from (
          select a.source, a.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 0 as foreign_rank
            from autobahn_events a
            join unnest($1::text[], $2::timestamptz[]) as p(road, t) on p.road = a.road and p.t = a.observed_at
           where a.road = any($1::text[])
             and ($11::boolean is not true or (select p is null or p ~ ('(^|[^0-9])' || substr(a.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))
          union all
          select a.source, q.road, a.road as filed_road, a.kind, a.ext_id, a.title, a.subtitle, a.abnormal_traffic_type,
                 a.delay_s, a.extent, a.geom, a.raw, a.raw_hash, a.observed_at, 1 as foreign_rank
            from (select c.road,
                         coalesce((select p.t from unnest($1::text[], $2::timestamptz[]) as p(road, t) where p.road = c.road limit 1),
                                  (select max(x.observed_at) from autobahn_events x
                                    where x.road = c.road and x.observed_at > $10::timestamptz - interval '30 minutes' and x.observed_at <= $10::timestamptz)) as t
                    from (select distinct c0.road from autobahn_events c0
                            join (select lt0.road, max(lt0.observed_at) as t from autobahn_events lt0
                                   where lt0.observed_at > $10::timestamptz - interval '30 minutes' and lt0.observed_at <= $10::timestamptz
                                   group by lt0.road) lt on lt.road = c0.road and lt.t = c0.observed_at
                           where $11::boolean is true
                             and exists (select 1 from unnest($1::text[]) as q0(road)
                                          where q0.road <> c0.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q0.road, 2) || '([^0-9]|$)') from substring(coalesce(c0.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p))) c) ft
            join autobahn_events a on a.road = ft.road and a.observed_at = ft.t
            join unnest($1::text[]) as q(road) on q.road <> a.road and (select p is not null and p ~ ('(^|[^0-9])' || substr(q.road, 2) || '([^0-9]|$)') from substring(coalesce(a.title, '') from '^\s*(A\s?[0-9]{1,3}[a-z]?(?:(?:\s*[-,/_&+]\s*|\s+(?:und\s+)?)A\s?[0-9]{1,3}[a-z]?)*)') as p)
        ) u
       order by u.road, u.ext_id, u.foreign_rank, u.filed_road) e





    cross join lateral (select autobahn_raw_resolve(e.raw, e.raw_hash, e.ext_id, e.observed_at) as raw offset 0) rr
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
    select m.*,
           row_number() over (partition by m.site order by m.distance_m nulls last, (m.lat is null), m.ext_id) as rn,
           (count(*) over (partition by m.site))::int as segments
    from m
  )
  select * from (
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
  where m.class_rn <= $5






  order by case when $6::float8 is null then array_position($1::text[], m.road) end,
           case when $6::float8 is not null then m.distance_m end,
           case m.kind when 'closure' then 0 else 1 end,
           m.distance_m nulls last,
           m.title, m.ext_id`
	legacyReads["api_road_status_checked"] = `  select k as feed_key,
         (select r.finished_at
            from feed_runs r
           where r.feed_key = k
             and r.status in ('ok', 'unchanged', 'degraded')
             and r.finished_at is not null
           order by r.started_at desc
           limit 1) as checked_at
    from unnest($1::text[]) as k`
	legacyReads["api_road_status_plan_checks"] = `select feed_key, cadence_s, checked_at, paused from feed_checks where feed_key = any($1::text[])`
	legacyReads["api_road_status_planned"] = `  with m0 as (
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
           (case when e.feed_key in ('sperrinfosys_sn') then e.payload ->> 'twinOf' when e.feed_key in ('viz_berlin', 'roadworks_mv_planned', 'ald_14d', 'roadworks_akd', 'roadworks_he_gesammelt', 'traffic_nrw', 'traffic_nrw_einzel', 'traffic_hamburg') then (select o.publication_id from road_event_twins l join (SELECT id,
    feed_key,
    source_id,
    publication_id,
    version,
    publisher,
    event_type,
    road_number,
    referent_from,
    referent_to,
    distance_from_m,
    distance_to_m,
    geom,
    feed_ts,
    overall_start,
    overall_end,
    payload,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_feed,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    closure_starts_at,
    closure_ends_at,
    valid_windows,
    suspended,
    entry_static,
    land_code
   FROM road_events
  WHERE (deleted_at IS NULL)) o on o.id = l.origin_id and o.closed_at is null and coalesce(o.confidentiality, 'noRestriction') = 'noRestriction' where l.twin_id = e.id) end) as twin_of,
           case when $6::float8 is null then null
                else ST_Distance(e.geom::geography, ST_SetSRID(ST_MakePoint($6::float8, $7::float8), 4326)::geography)::int end as distance_m
    
    from (SELECT id,
    feed_key,
    source_id,
    publication_id,
    version,
    publisher,
    event_type,
    road_number,
    referent_from,
    referent_to,
    distance_from_m,
    distance_to_m,
    geom,
    feed_ts,
    overall_start,
    overall_end,
    payload,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_feed,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    closure_starts_at,
    closure_ends_at,
    valid_windows,
    suspended,
    entry_static,
    land_code
   FROM road_events
  WHERE (deleted_at IS NULL)) e
    where e.closed_at is null
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
               when e.feed_key = any($11::text[]) then not jsonb_path_exists(e.payload, '$.situationRecord[*] ? (@.abnormalTrafficType[*]."#text" like_regex "^(stationaryTraffic|queuingTraffic|slowTraffic|heavyTraffic)$")') and not road_event_suspended(e.payload)
               else not jsonb_path_exists(e.payload, '$.situationRecord[*] ? (@.abnormalTrafficType[*]."#text" like_regex "^(stationaryTraffic|queuingTraffic|slowTraffic|heavyTraffic)$")') end
  ),

  m as (select * from m0 where not (((m0.feed_key = 'traffic_berlin' and m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_lms_berlin')) or (m0.feed_key = 'viz_berlin' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_berlin') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'traffic_berlin')))) or (m0.feed_key = 'roadworks_mv_planned' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_mv') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_mv')))) or (m0.feed_key = 'ald_14d' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast')))) or (m0.feed_key = 'roadworks_akd' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_bast')))) or (m0.feed_key = 'sperrinfosys_sn' and m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_ald_sn')) or (m0.feed_key = 'roadworks_he_gesammelt' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_he') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'roadworks_he')))) or (m0.feed_key = 'traffic_nrw' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events')))) or (m0.feed_key = 'traffic_nrw_einzel' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events')))) or (m0.feed_key = 'traffic_hamburg' and (m0.publication_id in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events') or (m0.twin_of is not null and m0.twin_of in (select o2.publication_id from m0 o2 where o2.feed_key = 'nms_events'))))))),
  page as (
    select * from (
    select m.*,
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
    where z.class_rn <= $5
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
           jsonb_path_query_first(x.payload, '$.headerInformation.headerInformationExtension.EventName."#text"') #>> '{}',
           jsonb_path_query_first(x.payload, '$.situationRecord.generalPublicComment ? (@.commentType."#text" == "locationDescriptor").comment.values.value."#text"') #>> '{}',
           jsonb_path_query_first(x.payload, '$.situationRecord.groupOfLocations.**.roadName.values.value."#text"') #>> '{}'
         ) as place_text,
         jsonb_path_query_first(x.payload, '$.situationRecord.generalPublicComment ? (!exists(@.commentType) || @.commentType."#text" == "description").comment.values.value."#text"') #>> '{}' as detail_text,
         (select string_agg(v.n, ', ' order by v.o) from (
             select r.j #>> '{}' as n, min(r.o) as o
               from jsonb_path_query(x.payload, '$.situationRecord[*] ? (@."@xsi:type" == "ReroutingManagement").alternativeRoute.**.roadNumber."#text"') with ordinality as r(j, o)
              group by 1
           ) v) as detour_via,
         jsonb_path_exists(x.payload, '$.situationRecord[*] ? (@."@xsi:type" == "ReroutingManagement" && exists(@.forVehiclesWithCharacteristicsOf))') as detour_vehicles
  from page p


  join (SELECT id,
    feed_key,
    source_id,
    publication_id,
    version,
    publisher,
    event_type,
    road_number,
    referent_from,
    referent_to,
    distance_from_m,
    distance_to_m,
    geom,
    feed_ts,
    overall_start,
    overall_end,
    payload,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_feed,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    closure_starts_at,
    closure_ends_at,
    valid_windows,
    suspended,
    entry_static,
    land_code
   FROM road_events
  WHERE (deleted_at IS NULL)) x on x.id = p.id and coalesce(x.confidentiality, 'noRestriction') = 'noRestriction'
  cross join lateral (
    select coalesce(x.payload -> 'situationExtension' -> 0 -> 'situationExtended' -> 0 -> 'overallSituation' -> 0,
                    x.payload -> 'situationRecord' -> 0) as n
  ) o
  order by case when $6::float8 is null then array_position($1::text[], p.road_number) end,
           p.distance_m nulls last,
           p.overall_start nulls first, p.referent_from, p.publication_id`
	legacyReads["api_road_status_strategies"] = `  select r.feed_key, r.source_id, r.publication_id, r.road_number, r.event_type,
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
      from (select f.*, (count(*) over ())::int as phases_now,
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
             e.referent_from, e.referent_to, e.payload, e.overall_start, e.entry_static, e.overall_end, e.valid_windows,
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
        from (SELECT id,
    feed_key,
    source_id,
    publication_id,
    version,
    publisher,
    event_type,
    road_number,
    referent_from,
    referent_to,
    distance_from_m,
    distance_to_m,
    geom,
    feed_ts,
    overall_start,
    overall_end,
    payload,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_feed,
    source_publisher,
    source_run_id,
    provider_updated_at,
    created_at,
    updated_at,
    deleted_at,
    deleted_reason,
    deleted_run_id,
    confidentiality,
    closure_starts_at,
    closure_ends_at,
    valid_windows,
    suspended,
    entry_static,
    land_code
   FROM road_events
  WHERE (deleted_at IS NULL)) e
        cross join lateral (select (select x from unnest(e.valid_windows) x where (upper_inf(x) or upper(x) > $3::timestamptz) and lower(x) <= $4::timestamptz order by (upper_inf(x) or upper(x) > $3::timestamptz) desc, lower(x) limit 1) as x) w
       where e.feed_key = any($1::text[]) and e.closed_at is null and e.road_number = any($2::text[])
         and coalesce(e.confidentiality, 'noRestriction') = 'noRestriction'
         and case
               when e.feed_key = any($6::text[]) then
                 e.event_type = any(array['generalNetworkManagement', 'reroutingManagement', 'reroutingManagementEnhanced']::text[])
                 and road_event_in_force(e.overall_start, e.overall_end, e.payload, e.valid_windows, $3::timestamptz)
               when e.valid_windows is null then
                 coalesce(e.overall_start, '-infinity'::timestamptz) <= $4::timestamptz
                 and coalesce(e.overall_end, 'infinity'::timestamptz) > $3::timestamptz
               else w.x is not null
             end
    ) r
    cross join lateral (
      select case when jsonb_typeof(r.payload) = 'object' then r.payload || '{}'::jsonb else r.payload end as p
      offset 0) dp
    cross join lateral (
      select coalesce(dp.p -> 'situationExtension' -> 0 -> 'situationExtended' -> 0 -> 'overallSituation' -> 0,
                      dp.p -> 'situationRecord' -> 0) as n
    ) o
   where r.rn <= $5
   order by array_position($2::text[], r.road_number), r.rn`
	legacyReads["api_road_status_ticks"] = `  select road, max(observed_at) as t
  from autobahn_events
  where road = any($1::text[]) and observed_at > $2::timestamptz - interval '30 minutes' and observed_at <= $2::timestamptz
  group by road`
	legacyReads["api_speed_official_cells"] = `  select c.admin_code
    from (SELECT id,
    source_feed,
    source_publisher,
    external_id,
    attribution_id,
    kind,
    enforces,
    geom,
    ags,
    municipality,
    street,
    location_text,
    direction,
    max_speed_kmh,
    max_speed_hgv_kmh,
    valid_from,
    valid_to,
    data_as_of,
    provider_updated_at,
    extras,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_run_id,
    created_at,
    updated_at
   FROM speed_enforcement
  WHERE (closed_at IS NULL)) s
    cross join lateral (
    select k.admin_code
      from (select g.admin_code, g.geom <-> s.geom as d
              from gazetteer_places g
             where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.admin_code ~ '^[0-9]{8}$' and g.geom is not null
             order by g.geom <-> s.geom
             limit 4) k
     order by k.d, k.admin_code
     limit 1) c
   where s.kind in ('fixed', 'section_control') and s.geom is not null and s.source_feed = any($1::text[])
  union
  select s.ags as admin_code
    from (SELECT id,
    source_feed,
    source_publisher,
    external_id,
    attribution_id,
    kind,
    enforces,
    geom,
    ags,
    municipality,
    street,
    location_text,
    direction,
    max_speed_kmh,
    max_speed_hgv_kmh,
    valid_from,
    valid_to,
    data_as_of,
    provider_updated_at,
    extras,
    first_seen_at,
    last_seen_at,
    closed_at,
    source_run_id,
    created_at,
    updated_at
   FROM speed_enforcement
  WHERE (closed_at IS NULL)) s
   where s.kind in ('fixed', 'section_control') and s.geom is null and s.source_feed = any($1::text[]) and s.ags ~ '^[0-9]{8}$'`
	//nolint:misspell // materialized is the SQL keyword
	legacyReads["api_station_alerts"] = `select m.source_id || ':' || m.alert_id, m.header, m.active_to, m.line_names, m.stop_ids, m.message_key, m.carried_by, m.feed from (
  with in_force as materialized (
    select a.source_id, a.source_feed, a.alert_id, a.header, a.active_from, a.active_to,
           a.line_names, a.stop_ids, a.kreise, a.dedupe_key, a.message_ref
      from (SELECT source_id,
    source_feed,
    alert_id,
    header,
    active_from,
    active_to,
    active_periods,
    line_names,
    stop_ids,
    kreise,
    placed_by,
    dedupe_key,
    message_ref,
    last_seen_at
   FROM delfi_service_alerts a
  WHERE ((closed_at IS NULL) AND (COALESCE(confidentiality, 'noRestriction'::text) = 'noRestriction'::text))) a
     where a.source_feed = any($2::text[])
       and (a.source_id <> 'delfi_siri_sx' or a.last_seen_at > $1::timestamptz - make_interval(secs => 86400))
       and (jsonb_array_length(a.active_periods) = 0
            or exists (select 1 from jsonb_array_elements(a.active_periods) p
                        where (p->>'start' is null or (p->>'start')::timestamptz <= $1::timestamptz)
                          and (p->>'end' is null or (p->>'end')::timestamptz > $1::timestamptz)))
  )
  select o.source_id, o.alert_id, o.header, o.active_from, o.active_to, o.line_names, o.stop_ids, o.kreise,
         (select g.source_id || ':' || g.alert_id from in_force g
           where g.dedupe_key = o.dedupe_key or (o.message_ref is not null and g.message_ref = o.message_ref)
           order by (g.source_id <> 'delfi_rt_sa'), g.source_id, g.alert_id
           limit 1) as message_key,
         (select array_agg(distinct g.source_feed order by g.source_feed) from in_force g
           where g.dedupe_key = o.dedupe_key or (o.message_ref is not null and g.message_ref = o.message_ref)) as carried_by, o.source_feed as feed
    from in_force o
   where o.stop_ids && $3::text[]
      or (o.line_names && $4::text[] and o.kreise && $5::text[])
   order by (case when o.stop_ids && $3::text[] then 0 else 1 end), o.active_from desc nulls last, o.source_id, o.alert_id
   limit $6) m`
	//nolint:misspell // the platform's names spell it so (has_travel_center, city center)
	legacyReads["api_station_record"] = `select m.station_no, m.name, m.eva_no, m.eva_nos, m.slug, m.street, m.zipcode, m.city, m.federal_state, m.municipality_code, m.category::int, m.stepless_access, contract_stop_place_of(m.ifopt), m.lat, m.lon, m.last_seen_at, m.source_id, array_remove(array[case when m.has_parking then 'parking' end, case when m.has_bicycle_parking then 'bicycleParking' end, case when m.has_local_public_transport then 'localPublicTransport' end, case when m.has_public_facilities then 'publicFacilities' end, case when m.has_locker_system then 'lockerSystem' end, case when m.has_taxi_rank then 'taxiRank' end, case when m.has_travel_necessities then 'travelNecessities' end, case when m.has_wifi then 'wifi' end, case when m.has_travel_center then 'travelCentre' end, case when m.has_railway_mission then 'railwayMission' end, case when m.has_db_lounge then 'dbLounge' end, case when m.has_lost_and_found then 'lostAndFound' end, case when m.has_car_rental then 'carRental' end], null) from (
  select s.station_no, s.slug, s.name, s.eva_no, s.eva_nos, s.ds100, s.ifopt, s.category, s.federal_state,
         s.municipality_code, s.street, s.zipcode, s.city,
         st_y(s.geom::geometry) as lat, st_x(s.geom::geometry) as lon,
         s.stepless_access, s.has_parking, s.has_bicycle_parking, s.has_local_public_transport, s.has_public_facilities,
         s.has_locker_system, s.has_taxi_rank, s.has_travel_necessities, s.has_wifi, s.has_travel_center,
         s.has_railway_mission, s.has_db_lounge, s.has_lost_and_found, s.has_car_rental,
         s.mobility_service, s.mobility_staff, s.db_information, s.local_service_staff, s.service_phone,
         s.last_seen_at, s.updated_at, s.source_id
    from station_index_live s
   where s.station_no = (coalesce((select s.station_no from station_index_live s where s.station_no = $1::text and s.source_id = any($2::text[])), (
  select s.station_no
    from station_index_live s
   where ($1)::text = any (s.eva_nos) and s.source_id = any($2::text[])
   order by (s.eva_no = ($1)::text) desc, lpad(s.station_no, 9, '0')
   limit 1), (select s.station_no from station_index_live s where s.source_id = any($2::text[]) and s.ifopt = substring(regexp_replace($1::text, '^[0-9]+:', ''), '^(?:dhid:)?([a-z]{2}:[0-9]{5}:[0-9]+)') order by lpad(s.station_no, 9, '0') limit 1)))) m where m.source_id = any($2::text[])`
	legacyReads["api_stops_near"] = `select (m.gtfs_feed_key || ':') || m.root_id, m.name, coalesce(m.modes, '{}'), m.metres, m.lat, m.lon, m.source_feed from (
  select l.loaded, s.gtfs_feed_key, s.source_feed, s.mode, s.root_id, s.name, s.modes, s.lines, s.stop_ids, s.metres, s.evas, s.station_nos, s.category, s.lat, s.lon
    from (select exists (select 1 from transit_stop_modes) as loaded) l
    left join lateral (
      select n.gtfs_feed_key, n.source_feed, m.mode, n.root_id, n.name, n.modes, n.lines, n.stop_ids, n.metres, e.evas, e.station_nos, e.category, m.ord,
             st_y(n.geom::geometry) as lat, st_x(n.geom::geometry) as lon
        from unnest($4::text[]) with ordinality as m(mode, ord)
        cross join lateral (
          select t.gtfs_feed_key, f.source_feed, t.root_id, t.name, t.modes, t.lines, t.stop_ids, t.geom,
                 st_distance(t.geom, st_point($1, $2)::geography) as metres
            from transit_stop_modes t join feeds f on f.gtfs_feed_key = t.gtfs_feed_key
           where t.modes @> array[m.mode]
             and st_dwithin(t.geom, st_point($1, $2)::geography, $3::double precision)
             and f.source_feed = any($6::text[])
           order by t.geom <-> st_point($1, $2)::geography
           limit $5
        ) n
        left join lateral (
          select array_agg(distinct x.eva order by x.eva) as evas,
                 array_agg(distinct d.station_no order by d.station_no) filter (where d.station_no is not null) as station_nos,
                 min(d.category) as category
            from gazetteer_places g
            left join db_station_numbers d on d.eva_no = g.external_id
            cross join lateral unnest(array[g.external_id] || coalesce(d.part_eva_nos, '{}'::text[])) as x(eva)
           where m.mode in ('rail', 'sbahn')
             and g.source_id = 'db_stada'
             and g.kind = 'station'
             and g.closed_at is null
             and g.geom is not null
             and st_dwithin(g.geom, n.geom, (300)::double precision)
        ) e on true
    ) s on true
   order by s.ord, s.metres) m where m.root_id is not null`
	legacyReads["api_timetable_reach"] = `select m.to_utc, m.loaded_at from (
  with run as (
    select started_at,



           substring(note from '"w":"[0-9]{4}-[0-9]{2}-[0-9]{2}[.][.]([0-9]{4}-[0-9]{2}-[0-9]{2})"')::date as wrote_to
      from ingest_runs
     where provider = ('delfi_static')::text
       and status = any((array['ok', 'degraded'])::text[])
     order by started_at desc
     limit 1
  ),
  bounds as (
    select min(service_date) as first_date, max(service_date) as last_date
      from gtfs_service_dates
     where gtfs_feed_key = (1)::smallint
  ),
  win as (
    select (select started_at from run) as loaded_at,
           coalesce(((select started_at from run) at time zone 'Europe/Berlin')::date::timestamp at time zone 'Europe/Berlin',
                    b.first_date::timestamp at time zone 'Europe/Berlin') as from_utc,
           (coalesce((select wrote_to from run), b.last_date) + 1)::timestamp at time zone 'Europe/Berlin' as to_utc
      from bounds b
  ),
  days as (



    select generate_series((w.from_utc at time zone 'Europe/Berlin')::date - 1,
                           ((w.to_utc  at time zone 'Europe/Berlin') - interval '1 microsecond')::date,
                           interval '1 day')::date as d
      from win w
     where w.from_utc is not null
  ),
  held as (
    select d.d,
           exists (select 1
                     from gtfs_service_dates g
                    where g.gtfs_feed_key = (1)::smallint
                      and g.service_date = d.d) as held
      from days d
  )
  select w.loaded_at,
         w.from_utc,
         w.to_utc,
         b.first_date::text as first_date,
         b.last_date::text  as last_date,
         (select array_agg(d::text order by d) from held)            as window_dates,
         (select array_agg(d::text order by d) from held where held) as held_dates
    from win w, bounds b) m`
	legacyReads["api_train_board_stored"] = `select b.eva_nos, b.read_at, b.degraded, b.covers_until, d.line, d.category, d.destination, d."plannedAt", d."delayMin", d."plannedPlatform", d.platform, d."platformChanged", d.cancelled from (
select b.eva_nos, b.read_at, b.degraded, b.covers_until, b.departures
  from db_station_boards b
 where b.eva_nos && (array[$1::text])::text[] and b.source_feed = any((select coalesce(array_agg(x.f), '{}') f from unnest($2::text[]) x(f) where coalesce((select not coalesce(k.excluded, false) and coalesce(k.display_class, 'open') <> 'withhold' and (coalesce(k.audience, 'public') = 'public' or current_setting('viafrei_api.own_key', true) = 'on') from contract_source_rules k where x.f ~ k.pattern order by k.priority limit 1), true))::text[])
   and b.read_at >= ('-infinity'::timestamptz)
 order by b.read_at desc
 limit 1) b left join lateral jsonb_to_recordset(b.departures) as d(line text, category text, destination text, "plannedAt" text, "delayMin" int, "plannedPlatform" text, platform text, "platformChanged" boolean, cancelled boolean) on true`
	legacyReads["api_train_interchange_lines"] = `select ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])) || ':') || x.id, null::text, null::text[], null::text[] from unnest((select t.ids from (
  select coalesce(array_agg(distinct x.id order by x.id), '{}'::text[]) as ids
    from (
      select s.id
        from unnest(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[]) as d(dhid)
        join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s on s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint and s.id = d.dhid
      union
      select s.id
        from unnest(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[]) as d(dhid)
        join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s on s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint and s.id ~>=~ (d.dhid || ':') and s.id ~<~ (d.dhid || ';')
      union
      select s.id
        from unnest(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[]) as d(dhid)
        join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s on s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint and s.id ~>=~ (d.dhid || '_') and s.id ~<~ (d.dhid || '` + "`" + `')
      union
      select s.id
        from (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s
       where s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint
         and s.parent_id = any(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[])
    ) x) t)) x(id) union all select null, l.root_id, l.modes, l.lines from (
  select f.root_id, f.modes, f.lines
    from transit_stop_modes f
   where f.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint
     and f.root_id = any(array(select distinct coalesce(s.parent_id, s.id)
                                 from (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s
                                where s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint
                                  and s.id = any(((select t.ids from (
  select coalesce(array_agg(distinct x.id order by x.id), '{}'::text[]) as ids
    from (
      select s.id
        from unnest(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[]) as d(dhid)
        join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s on s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint and s.id = d.dhid
      union
      select s.id
        from unnest(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[]) as d(dhid)
        join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s on s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint and s.id ~>=~ (d.dhid || ':') and s.id ~<~ (d.dhid || ';')
      union
      select s.id
        from unnest(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[]) as d(dhid)
        join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s on s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint and s.id ~>=~ (d.dhid || '_') and s.id ~<~ (d.dhid || '` + "`" + `')
      union
      select s.id
        from (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s
       where s.gtfs_feed_key = ((select g.gtfs_feed_key from feeds g where g.source_feed = 'delfi_static' and g.source_feed = any($3::text[])))::smallint
         and s.parent_id = any(((select d.dhids from (
  select coalesce(array_agg(distinct z.external_id order by z.external_id), '{}'::text[]) as dhids
    from gazetteer_places s
    join gazetteer_places z
      on z.source_id = 'delfi_zhv'
     and z.kind = 'stop'
     and z.closed_at is null
     and z.geom is not null
     and st_dwithin(z.geom, s.geom, ($2))
   where s.source_id = 'db_stada'
     and s.kind = 'station'
     and s.closed_at is null
     and s.geom is not null
     and s.external_id = any(($1)::text[])) d))::text[])
    ) x) t))::text[])))
   order by f.root_id) l`
	//nolint:misspell // route_color is the column; materialized is the SQL keyword
	legacyReads["api_train_interchange_plan"] = `select m.trip_id, m.stop_id, m.stop_sequence, m.at, m.service_date, m.route_type::int, m.line, m.trip_short_name, m.destination, m.platform, m.route_color from (
  with fam as materialized (
    select f.stop_id, f.grp
      from unnest((select ($2)::text[]), (select (array_fill('urban'::text, array[cardinality($2::text[])]))::text[])) with ordinality as f(stop_id, grp, i)
     where f.i > 0
  ),
  fam_ids as materialized (
    select grp, array_agg(stop_id) as ids from fam group by grp
  ),






  fam_tod as materialized (
    select f.stop_id, f.grp, nullif(btrim(s.platform_code), '') as platform, tod.lo, tod.hi
      from fam f
      join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) s on s.gtfs_feed_key = ($1)::smallint and s.id = f.stop_id
     cross join (select r.lo, r.hi
      from (select mod(ceil(extract(epoch from ((($3)::timestamptz at time zone 'UTC') + interval '1 hour')::time))::bigint, 86400::bigint) as c,
                   ceil(extract(epoch from (($4)::timestamptz - ($3)::timestamptz)) + 3600)::bigint as w) p
     cross join lateral (values
       (case when p.w >= 86400 then 0::bigint else p.c end,
        case when p.w >= 86400 then 86400::bigint else least(p.c + p.w, 86400::bigint) end),
       (0::bigint,
        case when p.w >= 86400 then 0::bigint else greatest(p.c + p.w - 86400, 0::bigint) end)
     ) as r(lo, hi)
     where r.hi > r.lo) tod
  ),
  win as materialized (
    select f.grp,
           st.trip_id,
           st.stop_id,
           st.stop_sequence,
           (case when ((((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour') at time zone 'Europe/Berlin') = (((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second'))
             then ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour' else ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') end) as at,
           sd.service_date,
           r.route_type,
           coalesce(nullif(btrim(r.short_name), ''), nullif(btrim(r.long_name), '')) as line,
           nullif(btrim(t.trip_short_name), '') as trip_short_name,
           nullif(btrim(t.headsign), '') as destination,
           f.platform,

           nullif(btrim(r.extras->>'route_color'), '') as route_color
      from fam_tod f



      join (SELECT arrival,
    departure,
    deleted_at,
    deleted_run_id,
    stop_sequence,
    gtfs_feed_key,
    pickup_type,
    drop_off_type,
    trip_id,
    stop_id,
    deleted_reason
   FROM stop_times
  WHERE (deleted_at IS NULL)) st on st.gtfs_feed_key = ($1)::smallint and st.stop_id = f.stop_id
                             and mod(date_part('epoch', st.departure)::bigint, 86400::bigint) >= f.lo
                             and mod(date_part('epoch', st.departure)::bigint, 86400::bigint) <  f.hi
      join (SELECT deleted_at,
    deleted_run_id,
    loaded_run_id,
    gtfs_feed_key,
    direction_id,
    id,
    route_id,
    service_id,
    headsign,
    shape_id,
    deleted_reason,
    stop_times_fp,
    trip_short_name
   FROM trips
  WHERE (deleted_at IS NULL))  t  on t.gtfs_feed_key = st.gtfs_feed_key and t.id = st.trip_id
      join (SELECT deleted_at,
    deleted_run_id,
    route_type,
    gtfs_feed_key,
    id,
    agency_id,
    short_name,
    long_name,
    deleted_reason,
    extras
   FROM routes
  WHERE (deleted_at IS NULL)) r  on r.gtfs_feed_key = t.gtfs_feed_key and r.id = t.route_id
      join gtfs_service_dates sd on sd.gtfs_feed_key = t.gtfs_feed_key and sd.service_id = t.service_id
     where st.departure is not null
       and st.pickup_type <> 1
       and (r.route_type in (0, 1) or r.route_type between 400 and 405 or r.route_type between 900 and 906)
       and (extract(epoch from (($4)::timestamptz - ($3)::timestamptz)) + 3600 >= 86400
            or mod(mod(date_part('epoch', st.departure)::bigint
                       - ceil(extract(epoch from ((($3)::timestamptz at time zone 'UTC') + interval '1 hour')::time))::bigint, 86400) + 86400, 86400)
               < extract(epoch from (($4)::timestamptz - ($3)::timestamptz)) + 3600)
       and (case when ((((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour') at time zone 'Europe/Berlin') = (((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second'))
             then ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour' else ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') end) >= ($3)::timestamptz
       and (case when ((((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour') at time zone 'Europe/Berlin') = (((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second'))
             then ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') - interval '1 hour' else ((((sd.service_date)::date::timestamp + date_part('epoch', st.departure) * interval '1 second')) at time zone 'Europe/Berlin') end) <  ($4)::timestamptz
  ),
  called as (
    select w.*
      from win w
      join fam_ids g on g.grp = w.grp
     where exists (select 1
                     from (SELECT arrival,
    departure,
    deleted_at,
    deleted_run_id,
    stop_sequence,
    gtfs_feed_key,
    pickup_type,
    drop_off_type,
    trip_id,
    stop_id,
    deleted_reason
   FROM stop_times
  WHERE (deleted_at IS NULL)) nx
                    where nx.gtfs_feed_key = ($1)::smallint
                      and nx.trip_id = w.trip_id
                      and nx.stop_sequence > w.stop_sequence
                      and nx.stop_id <> all(g.ids))
  ),
  same_line as (
    select distinct on (grp, at, line, destination, case when line is null or destination is null then stop_id end) *
      from called
     order by grp, at, line, destination, case when line is null or destination is null then stop_id end, trip_id
  ),
  ranked as (
    select s.*,
           case when s.at >= ($6)::timestamptz
                then row_number() over (partition by s.grp, s.at >= ($6)::timestamptz order by s.at, s.line nulls last, s.trip_id)
                else row_number() over (partition by s.grp, s.at >= ($6)::timestamptz order by s.at desc, s.line nulls last, s.trip_id)
           end as rn
      from same_line s
  )
  select k.grp, k.trip_id, k.stop_id, k.stop_sequence, k.at, k.service_date::text as service_date, k.route_type, k.line, k.trip_short_name,
         coalesce(k.destination,
                  (select nullif(btrim(ls.name), '')
                     from (SELECT arrival,
    departure,
    deleted_at,
    deleted_run_id,
    stop_sequence,
    gtfs_feed_key,
    pickup_type,
    drop_off_type,
    trip_id,
    stop_id,
    deleted_reason
   FROM stop_times
  WHERE (deleted_at IS NULL)) lx
                     join (SELECT deleted_at,
    deleted_run_id,
    gtfs_feed_key,
    location_type,
    id,
    name,
    parent_id,
    platform_code,
    deleted_reason,
    geom,
    extras
   FROM stops
  WHERE (deleted_at IS NULL)) ls on ls.gtfs_feed_key = lx.gtfs_feed_key and ls.id = lx.stop_id
                    where lx.gtfs_feed_key = ($1)::smallint and lx.trip_id = k.trip_id
                    order by lx.stop_sequence desc
                    limit 1)) as destination,
         k.platform, k.route_color
    from ranked k
   where k.rn <= ($5)::int
   order by k.grp, k.at, k.line nulls last, k.trip_id) m`
	legacyReads["api_trip_updates_of"] = `  select u.trip_id, u.stop_id, u.stop_sequence, u.delay_s, u.schedule_relationship, u.event_at, u.start_date, u.observed_at
    from transit_delays_live u
   where u.trip_id = any($1::text[])
   order by u.trip_id, u.stop_sequence nulls first
   limit 5000`
}
