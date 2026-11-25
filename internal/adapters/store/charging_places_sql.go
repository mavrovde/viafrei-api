// The place-grouped charging read: the platform's statements of its charging
// search (find_charging_station), on the base tables, with every parameter
// cast at each use.

package store

// chargingPlacesNear is the platform's charging-sites-near statement on the
// base tables (the live views' rule written out): the sites of the master
// feeds $5 within $3 metres of ($1 lon, $2 lat), one record per feed and
// coordinate, the register's twins of an operator's site left out, ranked
// by the feeds' ranks $12/$13, nearest first, at most $4; under $6 (a
// minimum power) and $7 (plug spellings) only the points that meet them;
// each with its points (price, terms, newest status of the status feeds $8
// within the window $9 seconds before $10, a change-only report back to
// $11, the status feeds $14 that speak for the master feeds $15) and the
// record ids. $16 are the twin feeds and $17 their radius.
//
//nolint:misspell // materialized is the SQL keyword
var chargingPlacesNear = named("api_charging_places_near", `
with near as materialized (
  select s.source_feed, s.site_id, s.name, s.operator, s.street, s.house_number, s.postcode, s.city,
         s.source_version, s.geom, s.operator_tokens, s.street_key, s.house_key,
         ST_Y(s.geom::geometry) as lat, ST_X(s.geom::geometry) as lon,
         ST_Distance(s.geom, ST_SetSRID(ST_MakePoint($1::float8, $2::float8), 4326)::geography) as distance_m,
         coalesce((s.source_feed = any($16::text[])
    and exists (
      select 1 from unnest(s.cands) a
       where a.closed_at is null
         and a.source_feed = any($5::text[])
         and not (a.source_feed = any($16::text[]))
         and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
         and ST_DWithin(a.geom, ST_SetSRID(ST_MakePoint($1::float8, $2::float8), 4326)::geography, $3::float8)
         and ST_DWithin(a.geom, s.geom, greatest($17::float8, 150))
         and ((ST_DWithin(a.geom, s.geom, $17::float8)
         and (a.operator_tokens && s.operator_tokens
              or (a.street_key <> ''
                  and a.street_key = s.street_key
                  and (a.postcode is null or s.postcode is null or a.postcode = s.postcode)
                  and (a.house_key is null or s.house_key is null or a.house_key = s.house_key)
                  and not exists (
                    select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) r
                     where r.closed_at is null
                       and r.source_feed = any($16::text[])
                       and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                       and ST_DWithin(r.geom, a.geom, $17::float8)
                       and r.operator_tokens && a.operator_tokens))))
              or (a.operator_tokens && s.operator_tokens
                  and a.street_key <> ''
                  and a.street_key = s.street_key
                  and a.house_key is not null
                  and a.house_key = s.house_key
                  and (a.postcode is null or s.postcode is null or a.postcode = s.postcode)))
         and exists (
           select 1 from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) ap
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
             where s.deleted_at is null and not s.access_private
               and s.closed_at is null
               and s.source_feed = any($5::text[])
               and coalesce(s.confidentiality, 'noRestriction') = 'noRestriction'
               and ST_DWithin(s.geom, ST_SetSRID(ST_MakePoint($1::float8, $2::float8), 4326)::geography, $3::float8)
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
      select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) a
       where a.closed_at is null
         and a.source_feed = any($5::text[])
         and not (a.source_feed = any($16::text[]))
         and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
         and ST_DWithin(a.geom, ST_SetSRID(ST_MakePoint($1::float8, $2::float8), 4326)::geography, $3::float8)
         and ST_DWithin(a.geom, n.geom, greatest($17::float8, 150))
         and ((ST_DWithin(a.geom, n.geom, $17::float8)
         and (a.operator_tokens && n.operator_tokens
              or (a.street_key <> ''
                  and a.street_key = n.street_key
                  and (a.postcode is null or n.postcode is null or a.postcode = n.postcode)
                  and (a.house_key is null or n.house_key is null or a.house_key = n.house_key)
                  and not exists (
                    select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) r
                     where r.closed_at is null
                       and r.source_feed = any($16::text[])
                       and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                       and ST_DWithin(r.geom, a.geom, $17::float8)
                       and r.operator_tokens && a.operator_tokens))))
              or (a.operator_tokens && n.operator_tokens
                  and a.street_key <> ''
                  and a.street_key = n.street_key
                  and a.house_key is not null
                  and a.house_key = n.house_key
                  and (a.postcode is null or n.postcode is null or a.postcode = n.postcode)))
         and exists (
           select 1 from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) ap
            where ap.source_feed = a.source_feed and ap.site_id = a.site_id
              and ap.closed_at is null and coalesce(ap.confidentiality, 'noRestriction') = 'noRestriction' and ap.connector_types && array['mcs']::text[]))), false)
              else n.is_twin end as is_twin, n.name, n.operator, n.street, n.house_number, n.postcode, n.city, n.source_version,
         n.lat, n.lon, n.distance_m
  from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) p
  join near n on n.source_feed = p.source_feed and n.site_id = p.site_id
  left join ranks r on r.feed = p.source_feed
  where p.closed_at is null
    and coalesce(p.confidentiality, 'noRestriction') = 'noRestriction'
),
ranked as (
  select np.source_feed, np.site_id, np.point_key, np.evse_id, np.provider_point_id, np.connector_types, np.max_power_kw, np.evse_norm, np.uuid_norm, np.rank, np.is_twin, np.name, np.operator, np.street, np.house_number, np.postcode, np.city, np.source_version, np.lat, np.lon, np.distance_m,
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
    select distinct on (p.source_feed, p.lat, p.lon) p.source_feed, p.site_id, p.point_key, p.evse_id, p.provider_point_id, p.connector_types, p.max_power_kw, p.name, p.operator, p.street, p.house_number, p.postcode, p.city, p.source_version, p.lat, p.lon, p.distance_m, p.uuid_feeds, p.uuid_ids
    from pts p
    order by p.source_feed, p.lat, p.lon, p.site_id
  ) d
  order by d.distance_m asc, d.site_id asc
  limit $4::bigint
),
kpts as materialized (
  select p.source_feed, p.site_id, p.point_key, p.evse_id, p.provider_point_id, p.connector_types, p.max_power_kw, p.name, p.operator, p.street, p.house_number, p.postcode, p.city, p.source_version, p.lat, p.lon, p.distance_m, p.uuid_feeds, p.uuid_ids from pts p
   where p.distance_m <= (select max(k.distance_m) from kept k)
     and row(p.source_feed, p.lat, p.lon) = any(array(select row(k.source_feed, k.lat, k.lon) from kept k))
)
select k.source_feed, k.site_id,
       case when count(distinct p.name) <= 1 and (bool_and(p.name is null) or bool_and(p.name is not null)) then k.name end as name,
       k.operator, k.street, k.house_number, k.postcode, k.city,
       k.source_version, k.lat, k.lon, k.distance_m,
       (select (select eps.slug from energy_page_slugs eps where eps.kind = 'charging' and eps.source_feed = k.source_feed and eps.item_id = rep.site_id)
          from (select (select coalesce(min(o.site_id) filter (where not ((coalesce((o.source_feed = any($16::text[])
    and exists (
      select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) a
       where a.closed_at is null
         and a.source_feed = any($5::text[])
         and not (a.source_feed = any($16::text[]))
         and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
         and ST_DWithin(a.geom, o.geom, greatest($17::float8, 150))
         and ST_DWithin(a.geom, o.geom, greatest($17::float8, 150))
         and ((ST_DWithin(a.geom, o.geom, $17::float8)
         and (a.operator_tokens && o.operator_tokens
              or (a.street_key <> ''
                  and a.street_key = o.street_key
                  and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)
                  and (a.house_key is null or o.house_key is null or a.house_key = o.house_key)
                  and not exists (
                    select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) r
                     where r.closed_at is null
                       and r.source_feed = any($16::text[])
                       and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                       and ST_DWithin(r.geom, a.geom, $17::float8)
                       and r.operator_tokens && a.operator_tokens))))
              or (a.operator_tokens && o.operator_tokens
                  and a.street_key <> ''
                  and a.street_key = o.street_key
                  and a.house_key is not null
                  and a.house_key = o.house_key
                  and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)))
         and exists (
           select 1 from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) ap
            where ap.source_feed = a.source_feed and ap.site_id = a.site_id
              and ap.closed_at is null and coalesce(ap.confidentiality, 'noRestriction') = 'noRestriction'))), false)
          and not (exists (select 1 from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) kp
                  where kp.source_feed = o.source_feed and kp.site_id = o.site_id
                    and kp.closed_at is null and coalesce(kp.confidentiality, 'noRestriction') = 'noRestriction'
                    and kp.connector_types && array['mcs']::text[])
          and not coalesce((o.source_feed = any($16::text[])
    and exists (
      select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) a
       where a.closed_at is null
         and a.source_feed = any($5::text[])
         and not (a.source_feed = any($16::text[]))
         and coalesce(a.confidentiality, 'noRestriction') = 'noRestriction'
         and ST_DWithin(a.geom, o.geom, greatest($17::float8, 150))
         and ST_DWithin(a.geom, o.geom, greatest($17::float8, 150))
         and ((ST_DWithin(a.geom, o.geom, $17::float8)
         and (a.operator_tokens && o.operator_tokens
              or (a.street_key <> ''
                  and a.street_key = o.street_key
                  and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)
                  and (a.house_key is null or o.house_key is null or a.house_key = o.house_key)
                  and not exists (
                    select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) r
                     where r.closed_at is null
                       and r.source_feed = any($16::text[])
                       and coalesce(r.confidentiality, 'noRestriction') = 'noRestriction'
                       and ST_DWithin(r.geom, a.geom, $17::float8)
                       and r.operator_tokens && a.operator_tokens))))
              or (a.operator_tokens && o.operator_tokens
                  and a.street_key <> ''
                  and a.street_key = o.street_key
                  and a.house_key is not null
                  and a.house_key = o.house_key
                  and (a.postcode is null or o.postcode is null or a.postcode = o.postcode)))
         and exists (
           select 1 from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) ap
            where ap.source_feed = a.source_feed and ap.site_id = a.site_id
              and ap.closed_at is null and coalesce(ap.confidentiality, 'noRestriction') = 'noRestriction' and ap.connector_types && array['mcs']::text[]))), false))) or ((select sr.rank from (select r.feed, r.rank from unnest($12::text[], $13::int[]) as r(feed, rank) where r.feed = any($5::text[])) sr where sr.feed = o.source_feed) is not null
   and not exists (select 1 from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) cp
                    where cp.source_feed = o.source_feed and cp.site_id = o.site_id and cp.closed_at is null and coalesce(cp.confidentiality, 'noRestriction') = 'noRestriction'
                      and not (cp.evse_id is not null and exists (select 1
          from (select cq.source_feed, cq.site_id, cq.closed_at, cq.confidentiality from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) cq
                 where cq.evse_id is not null and upper(translate(cq.evse_id, '*-', '')) = upper(translate(cp.evse_id, '*-', ''))
                   and coalesce(cq.confidentiality, 'noRestriction') = 'noRestriction' offset 0) cq
          join (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) cs on cs.source_feed = cq.source_feed and cs.site_id = cq.site_id
          join (select r.feed, r.rank from unnest($12::text[], $13::int[]) as r(feed, rank) where r.feed = any($5::text[])) cr on cr.feed = cs.source_feed
         where cq.closed_at is null and coalesce(cq.confidentiality, 'noRestriction') = 'noRestriction' and cs.closed_at is null and coalesce(cs.confidentiality, 'noRestriction') = 'noRestriction'
          and ST_DWithin(cs.geom, o.geom, 60) and cr.rank < (select sr.rank from (select r.feed, r.rank from unnest($12::text[], $13::int[]) as r(feed, rank) where r.feed = any($5::text[])) sr where sr.feed = o.source_feed))
    or (case when cp.provider_point_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then lower(cp.provider_point_id) end is not null and exists (select 1 from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) cs
          join (select r.feed, r.rank from unnest($12::text[], $13::int[]) as r(feed, rank) where r.feed = any($5::text[])) cr on cr.feed = cs.source_feed
          join (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) cq on cq.source_feed = cs.source_feed and cq.site_id = cs.site_id
         where (case when cq.provider_point_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then lower(cq.provider_point_id) end) = (case when cp.provider_point_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then lower(cp.provider_point_id) end)
          and cq.closed_at is null and coalesce(cq.confidentiality, 'noRestriction') = 'noRestriction' and cs.closed_at is null and coalesce(cs.confidentiality, 'noRestriction') = 'noRestriction'
          and ST_DWithin(cs.geom, o.geom, 60) and cr.rank < (select sr.rank from (select r.feed, r.rank from unnest($12::text[], $13::int[]) as r(feed, rank) where r.feed = any($5::text[])) sr where sr.feed = o.source_feed)))))))), min(o.site_id)) from (select source_feed, site_id, closed_at, confidentiality, geom, operator_tokens, street_key, house_key, postcode from charging_sites where deleted_at is null and not access_private) o
    where ST_DWithin(o.geom, ST_SetSRID(ST_MakePoint(k.lon, k.lat), 4326)::geography, 1)
      and (row(o.source_feed, ST_X(o.geom::geometry), ST_Y(o.geom::geometry)) = row(k.source_feed, k.lon, k.lat)) is true
      and o.closed_at is null and coalesce(o.confidentiality, 'noRestriction') = 'noRestriction'
   and exists (select 1 from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) p where p.source_feed = o.source_feed and p.site_id = o.site_id and p.closed_at is null and coalesce(p.confidentiality, 'noRestriction') = 'noRestriction')) as site_id) rep) as page_slug,
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
         'terms', array_remove(array[case when (case pw.x_hours when 'always' then true when 'restricted' then false end) then 'open247' end, case when (pw.x_card) then 'card' end, case when (case when pw.x_free is not null then pw.x_free when pw.price_per_kwh > 0 then false when pw.x_price_zero = false then false when pw.x_price_zero and pw.x_price_at >= $10::timestamptz - interval '90 days' then true end) then 'free' end, case when (case pw.x_park when 'public' then true when 'customers' then false end) then 'public' end]::text[], null),
         'termsSaid', array_remove(array[case when (case pw.x_hours when 'always' then true when 'restricted' then false end) is not null then 'open247' end, case when (pw.x_card) is not null then 'card' end, case when (case when pw.x_free is not null then pw.x_free when pw.price_per_kwh > 0 then false when pw.x_price_zero = false then false when pw.x_price_zero and pw.x_price_at >= $10::timestamptz - interval '90 days' then true end) is not null then 'free' end, case when (case pw.x_park when 'public' then true when 'customers' then false end) is not null then 'public' end]::text[], null),
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
    from (select source_feed, site_id, point_key, evse_id, provider_point_id, connector_types, max_power_kw, closed_at, confidentiality, price_per_kwh, price_tax_included, price_tax_rate, price_currency, price_last_updated, price_adhoc_zero, charging_register_opening_hours(extras) register_opening_hours, charging_register_payment_card(extras) register_payment_card, charging_register_free_of_charge(extras) register_free_of_charge, charging_register_parking(extras) register_parking from charging_points where deleted_at is null) w
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
order by k.distance_m asc, k.site_id asc`,
	"float8", "float8", "float8", "bigint", "text[]", "numeric", "text[]", "text[]", "int", "timestamptz", "timestamptz", "text[]", "int[]", "text[]", "text[]", "text[]", "float8")

// chargingStatusLiveness is the platform's status-feed liveness read: per
// status feed $1 (whose points come from $6) the newest conclusive cycle of
// the statuses $2 within $4 days before $3, the jump with no complete
// snapshot since, the validator and since when it holds, and the newest
// change and complete restatement of the last $5 seconds.
//
//nolint:misspell // materialized is the SQL keyword
var chargingStatusLiveness = named("api_charging_status_liveness", `
with extent as materialized (
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
           '-infinity'::timestamptz)) since on true`,
	"text[]", "text[]", "timestamptz", "int", "int", "text[]")

// chargingAtTownPoint is which of the sites at ($1 lon, $2 lat) lie within
// $4 metres of the point of the town $3 they name: a register row placed at
// its town's point rather than at its address.
//
//nolint:misspell // materialized is the SQL keyword
var chargingAtTownPoint = named("api_charging_at_town_point", `
select q.i::int as i
  from unnest($1::float8[], $2::float8[], $3::text[]) with ordinality as q(lon, lat, town, i)
 where q.town <> ''
   and exists (
     select 1 from gazetteer_places g
      where g.closed_at is null and g.kind = 'city' and g.geom is not null
        and g.source_id in ('bkg_gvisys', 'geonames')
        and (g.name_norm = q.town or lower(g.name_en) = q.town)
        and ST_DWithin(g.geom, ST_SetSRID(ST_MakePoint(q.lon, q.lat), 4326)::geography, $4::float8))`,
	"float8[]", "float8[]", "text[]", "float8")

// chargingSiteAddresses are the OpenStreetMap addresses of one site's
// street ($1, $2 the umlaut-free spelling), town ($3, $4), house number $5
// and postcode $6: by postcode, and by town, at most $7 each.
//
//nolint:misspell // materialized is the SQL keyword
var chargingSiteAddresses = named("api_charging_site_addresses", `
select a.i, a.id, a.street, a.house_number, a.postcode, a.place, a.land, a.lat, a.lon, a.extract_date, a.literal_score, a.by_postcode
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
        limit $7::int)) a`,
	"text", "text", "text", "text", "text", "text", "int")

// chargingRegisterAddress tells, of the register rows ($1 feed, $2 id, $3
// the folded town), whether the town they were filed under lies more than
// $4 metres away while no site of the master feeds $5 nearby shares their
// postcode, and the town the site lies in.
//
//nolint:misspell // materialized is the SQL keyword
var chargingRegisterAddress = named("api_charging_register_address", `
select q.source_feed, q.site_id, pc.postcode_seen, named.known, named.nearest_m, near.located_near
  from unnest($1::text[], $2::text[], $3::text[]) as q(source_feed, site_id, town)
  join charging_sites s on s.deleted_at is null and not s.access_private and s.source_feed = q.source_feed and s.site_id = q.site_id and s.closed_at is null and coalesce(s.confidentiality, 'noRestriction') = 'noRestriction'
  cross join lateral (
    select case when s.postcode is null or btrim(s.postcode) = '' then null else exists (
             select 1 from charging_sites o
              where o.deleted_at is null and not o.access_private and o.closed_at is null
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
     order by g.geom <-> s.geom limit 1) near on true`,
	"text[]", "text[]", "text[]", "float8", "text[]")
