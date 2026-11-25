package store

// Code generated from the platform's statements (scratch generator); the texts are the platform's,
// each parameter cast at its use and the families the key reads kept.

// addrByPostcode is the platform's ADDRESS_BY_POSTCODE_SQL
var addrByPostcode = named("api_place_address_by_postcode", `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date,
         (street_norm = $1::text)::int as literal_score
    from osm_addresses
   where closed_at is null
     and source_id = any((select $6::text[])::text[])
     and street_norm = any(array[$1::text, $2::text])
     and postcode = $3::text and house_number_norm = $4::text
   order by literal_score desc, osm_type, osm_id
   limit $5::integer`, "text", "text", "text", "text", "integer", "text[]")

// addrStreetByPostcode is the platform's ADDRESS_STREET_BY_POSTCODE_SQL
var addrStreetByPostcode = named("api_place_address_street_by_postcode", `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date, house_number_norm,
         (street_norm = $1::text)::int as literal_score
    from osm_addresses
   where closed_at is null
     and source_id = any((select $4::text[])::text[])
     and street_norm = any(array[$1::text, $2::text])
     and postcode = $3::text
   order by literal_score desc, osm_type, osm_id
   limit 2000`, "text", "text", "text", "text[]")

// addrByPlace is the platform's ADDRESS_BY_PLACE_SQL
var addrByPlace = named("api_place_address_by_place", `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date,
         (street_norm = $1::text)::int * 2 + (place_norm = $3::text)::int as literal_score
    from osm_addresses
   where closed_at is null
     and source_id = any((select $7::text[])::text[])
     and street_norm = any(array[$1::text, $2::text])
     and place_norm = any(array[$3::text, $4::text])
     and house_number_norm = $5::text
   order by literal_score desc, osm_type, osm_id
   limit $6::integer`, "text", "text", "text", "text", "text", "integer", "text[]")

// addrByPlacePrefix is the platform's ADDRESS_BY_PLACE_PREFIX_SQL
var addrByPlacePrefix = named("api_place_address_by_place_prefix", `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date,
         (street_norm = $1::text)::int as literal_score
    from osm_addresses
   where closed_at is null
     and source_id = any((select $6::text[])::text[])
     and street_norm = any(array[$1::text, $2::text])
     and house_number_norm = $4::text
     and place_norm ~>=~ ($3::text || ' ')
     and place_norm ~<~ ($3::text || '!')
     and starts_with(place_norm, $3::text || ' ')
   order by literal_score desc, place, osm_type, osm_id
   limit $5::integer`, "text", "text", "text", "text", "integer", "text[]")

// addrStreetByPlace is the platform's ADDRESS_STREET_BY_PLACE_SQL
var addrStreetByPlace = named("api_place_address_street_by_place", `select id::text as id, osm_type, street, house_number, postcode, place, land,
         st_y(geom::geometry) as lat, st_x(geom::geometry) as lon,
         to_char(extract_date, 'YYYY-MM-DD') as extract_date, house_number_norm,
         (street_norm = $1::text)::int * 2 + (place_norm = $3::text)::int as literal_score
    from osm_addresses
   where closed_at is null
     and source_id = any((select $5::text[])::text[])
     and street_norm = any(array[$1::text, $2::text])
     and place_norm = any(array[$3::text, $4::text])
   order by literal_score desc, osm_type, osm_id
   limit 2000`, "text", "text", "text", "text", "text[]")

// streetByPlace is the platform's STREET_BY_PLACE_SQL
var streetByPlace = named("api_place_street_by_place", `
  with s as (
    select street, postcode, place, geom, extract_date, literal_score
    from (
      select street, postcode, place, geom, extract_date,
             (street_norm = $1::text)::int * 2 + (place_norm = $3::text)::int as literal_score
        from osm_addresses
       where closed_at is null
     and source_id = any((select $6::text[])::text[])
         and street_norm = any(array[$1::text, $2::text])
         and place_norm = any(array[$3::text, $4::text])
       limit 5000) x
  ), best as (
    select street, postcode, place, geom, extract_date, literal_score from s where literal_score = (select max(literal_score) from s)
  ), c as (
    select street, postcode, place, geom, extract_date, literal_score, ST_ClusterDBSCAN(geom::geometry, eps := 0.015, minpoints := 1) over () as cid from best
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
  limit $5::integer`, "text", "text", "text", "text", "integer", "text[]")

// streetByPostcode is the platform's STREET_BY_POSTCODE_SQL
var streetByPostcode = named("api_place_street_by_postcode", `
  with s as (
    select street, postcode, place, geom, extract_date, literal_score
    from (
      select street, postcode, place, geom, extract_date,
             (street_norm = $1::text)::int as literal_score
        from osm_addresses
       where closed_at is null
     and source_id = any((select $5::text[])::text[])
         and street_norm = any(array[$1::text, $2::text])
         and postcode = $3::text
       limit 5000) x
  ), best as (
    select street, postcode, place, geom, extract_date, literal_score from s where literal_score = (select max(literal_score) from s)
  ), c as (
    select street, postcode, place, geom, extract_date, literal_score, ST_ClusterDBSCAN(geom::geometry, eps := 0.015, minpoints := 1) over () as cid from best
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
  limit $4::integer`, "text", "text", "text", "integer", "text[]")

// addrLoaded is the platform's ADDRESS_GAZETTEER_LOADED_SQL
var addrLoaded = named("api_place_addresses_loaded", `select exists (select 1 from osm_addresses where closed_at is null) as loaded`)
