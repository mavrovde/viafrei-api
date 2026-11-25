package store

// Code generated from the platform's statements (scratch generator); the texts are the platform's,
// each parameter cast at its use and the families the key reads kept.

// poiAnywhere is the platform's POI_ANYWHERE_SQL
var poiAnywhere = named("api_place_poi_anywhere", `with p0 as materialized (
  select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, p.geom, p.osm_type, p.osm_id
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $7::text[])::text[])
     and true
     and (($5::text is null and $6::text[] is null) or p.category = $5::text or (p.category || '=' || p.category_value) = any($6::text[]))
     and (p.name_norm like '%' || $1::text || '%' or p.brand ilike '%' || $2::text || '%')
     and (greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) >= $3::double precision or p.name_norm ~ ('\m' || $1::text || '\M'))
   order by tier desc, score desc, p.name, p.osm_type, p.osm_id
   limit greatest($4::integer, 60)
), h as (
  select p0.id, p0.name, p0.category, p0.category_value, p0.brand, p0.operator, p0.street, p0.house_number, p0.postcode, p0.place, p0.land, p0.lat, p0.lon, p0.extract_date, p0.score, p0.tier, p0.geom, p0.osm_type, p0.osm_id, row_number() over (order by p0.tier desc, p0.score desc, p0.name, p0.osm_type, p0.osm_id) as rn
    from p0
)
select h.id, h.name, h.category, h.category_value, h.brand, h.operator,
         h.street, h.house_number, h.postcode, h.place, h.land, h.lat, h.lon, h.extract_date, h.score, h.tier, null::float8 as distance_m, (case when h.tier >= 6 and h.rn <= 60 then (select count(*) from (select 1 from osm_pois q
                                where q.closed_at is null and st_dwithin(q.geom, h.geom, 2000)
                                limit 8000) v)
                   else 0 end)::int as vicinity
  from h
 order by h.tier desc, h.score desc, vicinity desc, h.name, h.osm_type, h.osm_id
 limit $4::integer`, "text", "text", "double precision", "integer", "text", "text[]", "text[]")

// poiByPlace is the platform's POI_BY_PLACE_SQL
var poiByPlace = named("api_place_poi_by_place", `with p0 as materialized (
  select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, p.geom, p.osm_type, p.osm_id
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $9::text[])::text[])
     and p.place_norm = any(array[$3::text, $4::text])
     and (($7::text is null and $8::text[] is null) or p.category = $7::text or (p.category || '=' || p.category_value) = any($8::text[]))
     and (p.name_norm like '%' || $1::text || '%' or p.brand ilike '%' || $2::text || '%')
     and (greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) >= $5::double precision or p.name_norm ~ ('\m' || $1::text || '\M'))
   order by tier desc, score desc, p.name, p.osm_type, p.osm_id
   limit greatest($6::integer, 60)
), h as (
  select p0.id, p0.name, p0.category, p0.category_value, p0.brand, p0.operator, p0.street, p0.house_number, p0.postcode, p0.place, p0.land, p0.lat, p0.lon, p0.extract_date, p0.score, p0.tier, p0.geom, p0.osm_type, p0.osm_id, row_number() over (order by p0.tier desc, p0.score desc, p0.name, p0.osm_type, p0.osm_id) as rn
    from p0
)
select h.id, h.name, h.category, h.category_value, h.brand, h.operator,
         h.street, h.house_number, h.postcode, h.place, h.land, h.lat, h.lon, h.extract_date, h.score, h.tier, null::float8 as distance_m, (case when h.tier >= 6 and h.rn <= 60 then (select count(*) from (select 1 from osm_pois q
                                where q.closed_at is null and st_dwithin(q.geom, h.geom, 2000)
                                limit 8000) v)
                   else 0 end)::int as vicinity
  from h
 order by h.tier desc, h.score desc, vicinity desc, h.name, h.osm_type, h.osm_id
 limit $6::integer`, "text", "text", "text", "text", "double precision", "integer", "text", "text[]", "text[]")

// poiByPlaceFuzzy is the platform's POI_BY_PLACE_FUZZY_SQL
var poiByPlaceFuzzy = named("api_place_poi_by_place_fuzzy", `with p0 as materialized (
  select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, p.geom, p.osm_type, p.osm_id
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $10::text[])::text[])
     and p.place_norm = any(array[$3::text, $4::text])
     and (($7::text is null and $8::text[] is null) or p.category = $7::text or (p.category || '=' || p.category_value) = any($8::text[]))
     and (p.name_norm % $1::text or p.brand % $2::text)
     and ($9::boolean or not ((p.category = 'amenity' and p.category_value in ('fuel', 'charging_station', 'car_wash', 'parking', 'parking_entrance', 'parking_space', 'motorcycle_parking', 'bicycle_parking')) or (p.category = 'building' and p.category_value in ('parking', 'garages', 'carport')) or (p.category = 'shop' and p.category_value in ('fuel'))))
     and (greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) >= $5::double precision)
   order by tier desc, score desc, p.name, p.osm_type, p.osm_id
   limit greatest($6::integer, 60)
), h as (
  select p0.id, p0.name, p0.category, p0.category_value, p0.brand, p0.operator, p0.street, p0.house_number, p0.postcode, p0.place, p0.land, p0.lat, p0.lon, p0.extract_date, p0.score, p0.tier, p0.geom, p0.osm_type, p0.osm_id, row_number() over (order by p0.tier desc, p0.score desc, p0.name, p0.osm_type, p0.osm_id) as rn
    from p0
)
select h.id, h.name, h.category, h.category_value, h.brand, h.operator,
         h.street, h.house_number, h.postcode, h.place, h.land, h.lat, h.lon, h.extract_date, h.score, h.tier, null::float8 as distance_m, (case when h.tier >= 6 and h.rn <= 60 then (select count(*) from (select 1 from osm_pois q
                                where q.closed_at is null and st_dwithin(q.geom, h.geom, 2000)
                                limit 8000) v)
                   else 0 end)::int as vicinity
  from h
 order by h.tier desc, h.score desc, vicinity desc, h.name, h.osm_type, h.osm_id
 limit $6::integer`, "text", "text", "text", "text", "double precision", "integer", "text", "text[]", "boolean", "text[]")

// poiNear is the platform's POI_NEAR_SQL
var poiNear = named("api_place_poi_near", `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3::double precision, $4::double precision)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $10::text[])::text[])
     and st_dwithin(p.geom, st_point($3::double precision, $4::double precision)::geography, $5::double precision)
     and (($8::text is null and $9::text[] is null) or p.category = $8::text or (p.category || '=' || p.category_value) = any($9::text[]))
     and (p.name_norm like '%' || $1::text || '%' or p.brand ilike '%' || $2::text || '%')
     and (greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) >= $6::double precision or p.name_norm ~ ('\m' || $1::text || '\M'))
   order by tier desc, score desc, distance_m asc, p.osm_type, p.osm_id
   limit $7::integer`, "text", "text", "double precision", "double precision", "double precision", "double precision", "integer", "text", "text[]", "text[]")

// poiNearFuzzy is the platform's POI_NEAR_FUZZY_SQL
var poiNearFuzzy = named("api_place_poi_near_fuzzy", `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3::double precision, $4::double precision)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $11::text[])::text[])
     and st_dwithin(p.geom, st_point($3::double precision, $4::double precision)::geography, $5::double precision)
     and (($8::text is null and $9::text[] is null) or p.category = $8::text or (p.category || '=' || p.category_value) = any($9::text[]))
     and (p.name_norm % $1::text or p.brand % $2::text)
     and ($10::boolean or not ((p.category = 'amenity' and p.category_value in ('fuel', 'charging_station', 'car_wash', 'parking', 'parking_entrance', 'parking_space', 'motorcycle_parking', 'bicycle_parking')) or (p.category = 'building' and p.category_value in ('parking', 'garages', 'carport')) or (p.category = 'shop' and p.category_value in ('fuel'))))
     and (greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) >= $6::double precision)
   order by tier desc, score desc, distance_m asc, p.osm_type, p.osm_id
   limit $7::integer`, "text", "text", "double precision", "double precision", "double precision", "double precision", "integer", "text", "text[]", "boolean", "text[]")

// poiCategoryByPlace is the platform's CATEGORY_BY_PLACE_SQL
var poiCategoryByPlace = named("api_place_poi_category_by_place", `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, null::float8 as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $10::text[])::text[])
     and p.category = any($5::text[])
     and p.category_value = any($6::text[])
     and ($8::text[] is null or p.category_value <> all($8::text[]) or p.name_norm ~ $9::text)
     and p.place_norm = any(array[$3::text, $4::text])
   order by similarity(p.name_norm, $1::text) desc, word_similarity($1::text, p.name_norm) desc, p.osm_type, p.osm_id
   limit $7::integer`, "text", "text", "text", "text", "text[]", "text[]", "integer", "text[]", "text", "text[]")

// poiCategoryNear is the platform's CATEGORY_NEAR_SQL
var poiCategoryNear = named("api_place_poi_category_near", `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3::double precision, $4::double precision)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $11::text[])::text[])
     and p.category = any($6::text[])
     and p.category_value = any($7::text[])
     and ($9::text[] is null or p.category_value <> all($9::text[]) or p.name_norm ~ $10::text)
     and st_dwithin(p.geom, st_point($3::double precision, $4::double precision)::geography, $5::double precision)
   order by similarity(p.name_norm, $1::text) desc, word_similarity($1::text, p.name_norm) desc, distance_m asc, p.osm_type, p.osm_id
   limit $8::integer`, "text", "text", "double precision", "double precision", "double precision", "text[]", "text[]", "integer", "text[]", "text", "text[]")

// poiCategoryNearest is the platform's CATEGORY_NEAREST_SQL
var poiCategoryNearest = named("api_place_poi_category_nearest", `select p.id::text as id, p.name, p.category, p.category_value, p.brand, p.operator,
         p.street, p.house_number, p.postcode, p.place, p.land,
         st_y(p.geom::geometry) as lat, st_x(p.geom::geometry) as lon,
         to_char(p.extract_date, 'YYYY-MM-DD') as extract_date, greatest(similarity(p.name_norm, $1::text), coalesce(similarity(p.brand, $2::text), 0)) as score, (case when ((p.category = 'amenity' and p.category_value in ('taxi', 'parking', 'parking_entrance', 'parking_space', 'bicycle_parking', 'motorcycle_parking', 'charging_station', 'car_sharing', 'bicycle_rental', 'post_box', 'atm', 'vending_machine', 'telephone', 'bench', 'shelter', 'toilets', 'waste_basket', 'recycling', 'drinking_water', 'clock', 'loading_dock', 'parcel_locker', 'ticket_validator'))
         or (p.category = 'tourism' and p.category_value in ('information'))) then 0 else
           (case when (p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) then 4 else 0 end)
         + (case when (p.category in ('historic', 'natural')
         or (p.category = 'tourism' and p.category_value in ('attraction', 'museum', 'viewpoint'))
         or (p.category = 'amenity' and p.category_value in ('townhall', 'place_of_worship'))) and ((p.name_norm = $1::text or coalesce(lower(p.brand) = lower($2::text), false)) or p.name_norm ~ ('\m' || $1::text || '\M')) then 2 else 0 end)
         + (case when p.name_norm ~ ('\m' || $1::text || '\M') then 1 else 0 end) end) as tier, st_distance(p.geom, st_point($3::double precision, $4::double precision)::geography) as distance_m, 0 as vicinity
    from osm_pois p
   where p.closed_at is null
     and p.source_id = any((select $11::text[])::text[])
     and p.category = any($6::text[])
     and p.category_value = any($7::text[])
     and ($9::text[] is null or p.category_value <> all($9::text[]) or p.name_norm ~ $10::text)
     and st_dwithin(p.geom, st_point($3::double precision, $4::double precision)::geography, $5::double precision)
   order by distance_m asc, p.osm_type, p.osm_id
   limit $8::integer`, "text", "text", "double precision", "double precision", "double precision", "text[]", "text[]", "integer", "text[]", "text", "text[]")

// poiPlaceWords is the platform's PLACE_WORDS_SQL
var poiPlaceWords = named("api_place_poi_place_words", `
  select array(
           select distinct g.name_norm
             from gazetteer_places g
            where g.name_norm = any($1::text[])
              and g.kind in ('city', 'district', 'admin')
              and g.closed_at is null) as words,
         exists (
           select 1
             from gazetteer_places g
            where g.kind in ('city', 'district', 'admin')
              and g.closed_at is null) as loaded`, "text[]")
