//go:build integration

package store

import (
	"fmt"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// legacyReads are the statements as they read the read contract's views
// before the store read the platform's tables, frozen: a parity test runs
// each beside the named statement that took its place and compares the rows.
// Test-only: the store itself never reads a contract view.
var legacyReads = map[string]string{
	// The main-station spellings ($11) are carried from the platform's
	// station search on purpose (#526): "Berlin Hbf" finds "S+U Berlin
	// Hauptbahnhof"; empty, the statement reads as the view did.
	"api_places_page": `
with here as (
  select case when $4::float8 is null then null
              else st_setsrid(st_makepoint($5::float8, $4::float8), 4326)::geography end g),
hit as (
  select a.place_id, min(case when a.search_fold = any(array[$2::text] || $11::text[]) then 0
                              when a.search_fold like $2 || '%' or a.search_fold like any(array(select x || '%' from unnest($11::text[]) x)) then 1 else 2 end) rank
  from contract.place_alias a
  where (a.search_fold like '%' || $2 || '%' or a.search_fold like any(array(select '%' || x || '%' from unnest($11::text[]) x))) and ` + permitted("a") + `
  group by a.place_id),
found as (
  select p.place_id id, p.name, p.place_kind kind, p.location, p.region_code, p.source_ref, hit.rank, p.prominence, p.official_key,
         true aliased
  from contract.place p
  join hit on hit.place_id = p.place_id
  where p.withdrawn_at is null
    and p.location is not null
    and (p.source_ref = any($1) or (p.place_kind = 'junction' and p.source_ref = any($9))
         -- carried on purpose (#285): a station of the station register, a
         -- stop family's row, answers as the platform's place search does
         or (p.place_kind = 'station_area' and p.source_ref = any($8)
             and exists (select from public.gazetteer_places g where g.id::text = p.place_id and g.kind = 'station')))
    and ` + permitted("p") + `
  union all
  select s.stop_place_id, s.name, 'station_area', s.location, s.region_code, s.source_ref,
         case when s.search_fold = any(array[$2::text] || $11::text[]) then 0
              when s.search_fold like $2 || '%' or s.search_fold like any(array(select x || '%' from unnest($11::text[]) x)) then 1 else 2 end, null, null, false
  from contract.stop_place s
  where (s.search_fold like '%' || $2 || '%' or s.search_fold like any(array(select '%' || x || '%' from unnest($11::text[]) x)))
    and s.withdrawn_at is null
    and s.location is not null
    and s.source_ref = any($8)
    and ` + permitted("s") + `
    -- carried on purpose (#192): a stop of another station is that
    -- station, as the station search collapses it
    and not exists (
        select 1 from contract.stop_place r
         where r.withdrawn_at is null and r.stop_place_id <> s.stop_place_id
           and (r.stop_place_id = split_part(s.stop_place_id, ':', 1) || ':' || substring(s.origin_key from '` + dhidStation + `')
                or (r.stop_place_id like split_part(s.stop_place_id, ':', 1) || ':' || substring(s.origin_key from '` + dhidStation + `') || ':%'
                    and r.stop_place_id ~<~ s.stop_place_id)))
  union all
  select r.road_ref, r.road_ref, 'motorway', pt.location, null, r.source_ref, 0, null, null, false
  from contract.road r
  cross join lateral (
    select coalesce((select j.location from contract.junction j
                      where j.road_ref = r.road_ref and j.location is not null and j.source_ref = any($9) and ` + permitted("j") + `
                      order by j.chainage_km nulls last, j.junction_id limit 1),
                    st_pointonsurface(r.geometry)) location) pt
  where $10::text <> ''
    and r.road_ref = $10::text
    and pt.location is not null
    and r.source_ref = any($9)
    and ` + permitted("r") + `)
select f.id, f.name, f.kind, st_y(f.location), st_x(f.location), f.region_code, f.source_ref, f.dist, f.official_key,
       case when f.aliased then (select min(e.label) from contract.place_alias e
         where e.place_id = f.id and e.lang = 'en' and btrim(e.label) <> '' and e.label <> f.name and ` + permitted("e") + `) end
from (
  select f.*, case when here.g is null then null else round(st_distance(f.location::geography, here.g, false))::int end dist
  from found f
  cross join here
  where ($3::text = '' or f.kind = $3::text)
    and (here.g is null or $6::int = 0 or st_dwithin(f.location::geography, here.g, $6::int, false))
  order by f.rank, f.prominence desc nulls last, dist nulls last, f.name, f.id
  limit $7) f
order by f.rank, f.prominence desc nulls last, f.dist nulls last, f.name, f.id`,
	"api_kreis_names": `
select p.official_key, min(p.name)
from contract.place p
where p.place_kind = 'district' and p.official_key = any($1) and p.withdrawn_at is null
  and p.source_ref = '` + places.KreisFamily + `' and p.source_ref = any($2) and ` + permitted("p") + `
group by p.official_key`,
	// The station's family is the platform board's (fault 3 of the 1.1.2
	// faults): the stops filed under the station's DHID, a parent named or
	// not, not only the stop points naming the station as their parent.
	// A stop whose id is no DHID is its own root here, where the store
	// takes its parent's root (stationFamily): the seed holds no such stop
	// with a parent, so the two forms agree on every station it holds.
	"api_station_by_id": opaque(`
select s.stop_place_id, s.name, coalesce(st_y(s.location), 0), coalesce(st_x(s.location), 0), s.national_stop_code,
       s.region_code, coalesce(s.modes, '{}'), s.source_ref,
       array(select distinct x.id from (
               select p.stop_point_id id, p.stop_place_id parent from contract.stop_point p where p.source_ref = any($2) and `+permitted("p")+`
               union
               select q.stop_place_id, null from contract.stop_place q where q.source_ref = any($2) and `+permitted("q")+`) x,
             (select split_part(s.stop_place_id, ':', 1) || ':' || coalesce(substring(s.origin_key from '`+dhidStation+`'), s.origin_key) root) k
              where (x.id = k.root or x.parent = k.root or x.id like k.root || ':%' or x.id like k.root || '\_%') and x.id <> s.stop_place_id
              order by x.id)
from contract.stop_place s
where s.stop_place_id = $1 and s.withdrawn_at is null and s.source_ref = any($2) and `+permitted("s"), "text", "text[]"),
	// A call's Trip Updates figure is credited to that source's key
	// ('tripupdates'), as the register publishes it, not the licence id the
	// view stamped, and a call carrying a family the key was not given is
	// left out: on purpose (fault A of the 1.1.1 faults).
	"api_board_calls": opaque(`
select d.journey_id, d.stop_point_id, d.call_order, d.line_name, d.transport_mode, d.route_type,
       d.train_number, d.train_category, nullif(btrim(j.destination_label), ''),
       coalesce(nullif(btrim(j.destination_label), ''),
                (select lp.name from contract.scheduled_call lc
                   join contract.stop_point lp on lp.stop_point_id = lc.stop_point_id
                  where lc.journey_id = d.journey_id
                  order by lc.call_order desc limit 1)),
       d.planned_departure_at, d.platform, d.planned_platform, d.platform_changed, d.realtime, case when d.realtime_source = 'delfi_rt_tu' then 'tripupdates' else d.realtime_source end,
       d.cancelled, d.delay_s, d.expected_departure_at, d.no_forecast, d.source_ref, array_replace(d.source_refs, 'delfi_rt_tu', 'tripupdates')
from contract.departure d
left join contract.journey j on j.journey_id = d.journey_id
where d.stop_place_id = $1
  and d.planned_departure_at >= $2 and d.planned_departure_at < $3
  and d.boarding
  and d.source_ref = any($4)
  and array_replace(d.source_refs, 'delfi_rt_tu', 'tripupdates') <@ $4
  and `+permitted("d")+`
  and exists (select 1 from contract.scheduled_call nx
                join contract.stop_point np on np.stop_point_id = nx.stop_point_id
               where nx.journey_id = d.journey_id and nx.call_order > d.call_order
                 and np.stop_place_id <> $1)
order by d.planned_departure_at, d.journey_id, d.call_order`, "text", "timestamp with time zone", "timestamp with time zone", "text[]"),
	// A state is current while its source restates it (its last sighting,
	// which the view never carried), and a unit FaSta lists is FaSta's
	// alone (#222): both read from the facility's own row.
	"api_station_facilities": `
with v as (
  select f.*, x.seen, x.eno, x.listed, x.expl
  from contract.station_facility f
  ` + legacyFacilitySeen + `
  where (f.stop_place_id = $1
         -- the station DB files under the DHID the station's platforms name
         -- as their parent (S+U Berlin Hauptbahnhof, 1.1.2 faults)
         or f.stop_place_id in (select p.stop_place_id from contract.stop_point p
                                 where p.stop_point_id like $1::text || ':%' or p.stop_point_id like $1::text || '\_%'))
    and f.source_ref = any($2) and ` + permitted("f") + `)
select v.facility_id, v.facility_kind, v.position_text,
       case when v.seen is null then 'unknown'
            when s.stale_after_s is not null and v.seen < $3::timestamptz - make_interval(secs => s.stale_after_s) then 'unknown'
            else v.facility_status end,
       v.seen, v.source_ref, case when v.facility_id like 'fasta:%' then substr(v.facility_id, 7) else v.eno end,
       case when v.listed <> v.source_ref and v.listed = any($2) then v.listed else '' end,
       case when lower(btrim(v.expl)) in ('', 'available', 'not available') then null else btrim(v.expl) end
from v
join contract.source s on s.source_ref = v.source_ref
where not exists (select 1 from v g where g.facility_id = 'fasta:' || v.eno)
order by v.facility_id`,
	"api_affected_alerts": opaque(`
select distinct a.alert_id, a.source_ref, u.sid
from contract.service_alert a, unnest(a.stop_ids) u(sid)
where a.source_ref = any($1) and `+"a.display_class <> 'withhold' and (a.audience = 'public' or "+ownKeyOn+")"+`
  and (a.active_from is null or a.active_from <= $2) and (a.active_to is null or a.active_to > $2)
  and `+alertLive("a", "$2"), "text[]", "timestamp with time zone"),
	"api_affected_lifts": `
select f.stop_place_id, count(*)::int, array_agg(distinct f.source_ref order by f.source_ref)
from (
  select f.stop_place_id, f.source_ref
  from contract.station_facility f
  where f.facility_status = 'inactive' and f.source_ref = any($1)
    and ` + "f.display_class <> 'withhold' and (f.audience = 'public' or " + ownKeyOn + ")" + `
  offset 0
) f
where f.stop_place_id is not null
group by 1`,
	"api_affected_calls": `
with d as (
  select d.station_ifopt, d.aimed_minute, d.line_key, d.cancelled, d.late, d.source_ref
  from contract.station_disruption d
  where d.source_ref = any($1) and ` + "d.display_class <> 'withhold' and (d.audience = 'public' or " + ownKeyOn + ")" + `
), f as (
  select station_ifopt, aimed_minute, line_key, max(cancelled) as c, greatest(max(cancelled + late) - max(cancelled), 0) as l
  from d group by 1, 2, 3
), s as (
  select station_ifopt, sum(c)::int as c, sum(l)::int as l from f group by 1 having sum(c) + sum(l) > 0
)
select s.station_ifopt, s.c, s.l, (select array_agg(distinct d.source_ref order by d.source_ref) from d where d.station_ifopt = s.station_ifopt)
from s`,
	"api_affected_places": `
select r.ref, s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code, coalesce(s.modes, '{}'), s.source_ref
from unnest($2::text[]) r(ref)
join contract.stop_place s on s.stop_place_id = r.ref or s.ifopt = r.ref
where s.withdrawn_at is null and s.location is not null and s.source_ref = any($1)
  and ` + "s.display_class <> 'withhold' and (s.audience = 'public' or " + ownKeyOn + ")",
	"api_service_alerts": `with ` + legacyRegionAlerts + `
select o.alert_id, o.header, o.description, o.cause, o.effect, o.url, o.active_from, o.active_to,
       o.line_names, o.placed_by, o.changed_at, o.source_ref
from o where not o.duplicate
order by case o.placed_by when 'stop' then 0 when 'route' then 1 else 2 end, o.active_from desc nulls last, o.alert_id
limit $5`,
	"api_alerts_summary": `with ` + legacyRegionAlerts + `
select exists (select 1 from contract.service_alert a where a.source_ref = any($2) and ` + alertLive("a", "$1") + `),
       (count(*) filter (where not o.duplicate))::int, (count(*) filter (where o.duplicate))::int,
       coalesce(array_agg(distinct o.source_ref order by o.source_ref), '{}')
from o where $5::int >= 0 -- $5, the limit, is the list's alone`,
	"api_area_punctuality": opaque(`select p.area_level, p.area_code, p.area_name, p.trips_observed, p.trips_late, p.trips_skipped_stop, p.trips_untimed,
       p.median_worst_delay_s, p.stops_seen, p.observed_at, p.source_ref
from contract.transit_punctuality p
where p.area_code = $1 and p.source_ref = any($2) and `+permitted("p")+`
order by p.area_level
limit 1`, "text", "text[]"),
	// The trip delays are the Trip Updates' figures, published under that
	// source's key ('tripupdates'), not the licence id the view stamped: on
	// purpose (round 2 of the 1.1.1 faults).
	"api_area_trips": `select count(distinct t.trip_ref)::int, (count(distinct t.trip_ref) filter (where t.worst_delay_s > $2::int * 60))::int,
       max(t.observed_at), coalesce(min(case when t.source_ref = 'delfi_rt_tu' then 'tripupdates' else t.source_ref end), '')
from contract.transit_trip_delay t
where starts_with(t.area_code, $1) and case when t.source_ref = 'delfi_rt_tu' then 'tripupdates' else t.source_ref end = any($3) and ` + permitted("t"),
	"api_line_in_timetable": `select exists (select 1 from contract.transit_line l where l.source_ref = any($2) and ` + permitted("l") + `),
       coalesce((select array_agg(distinct l.source_ref order by l.source_ref) from contract.transit_line l
                  where upper(regexp_replace(l.line_name, '\s+', '', 'g')) = $1::text and l.source_ref = any($2) and ` + permitted("l") + `), '{}')`,
	"api_national_roads": `
select 'situations', e.event_kind, null::text, e.source_ref, count(*)::int, null::int, null::float8, null::float8
from contract.traffic_event e
where ` + nationalInForce + `
group by 2, 4
union all
select 'states', e.state_code, null, e.source_ref, count(*)::int, null, null, null
from contract.traffic_event e
where ` + nationalInForce + ` and e.state_code is not null
group by 2, 4
order by 1, 2, 4`,
	"api_national_energy": `
with latest as (
  select distinct on (o.station_id, o.product_code) o.product_code, o.price, o.source_ref
  from contract.fuel_price_observation o
  join contract.fuel_station s on s.station_id = o.station_id and s.withdrawn_at is null and s.source_ref = any($1) and ` + permitted("s") + `
  where o.price is not null and o.source_ref = any($1) and ` + permitted("o") + `
    and o.observed_at > $2::timestamptz - make_interval(secs => $3::int) and o.observed_at <= $2::timestamptz
  order by o.station_id, o.product_code, o.observed_at desc)
select 'fuel', l.product_code, null::text, l.source_ref, count(*)::int, null::int,
       min(l.price)::float8, (percentile_cont(0.5) within group (order by l.price))::float8
from latest l
group by 2, 4
union all
select 'charging', c.source_ref, null, c.source_ref, count(distinct c.site_id)::int, count(p.point_id)::int, null, null
from contract.charging_site c
left join contract.charge_point p on p.site_id = c.site_id and ` + permitted("p") + `
where c.withdrawn_at is null and c.source_ref = any($1) and ` + permitted("c") + `
group by 2, 4
order by 1, 2, 4`,
	"api_national_parking": `
select 'parking', f.parking_kind, null::text, f.source_ref, count(*)::int, null::int, null::float8, null::float8
from contract.parking_facility f
where f.withdrawn_at is null and f.source_ref = any($1) and ` + permitted("f") + ` and $2::timestamptz is not null
group by 2, 4
order by 1, 2, 4`,
	"api_national_transit": `
select 'punctuality', p.area_level || ':' || p.area_code, p.area_name, p.source_ref, p.trips_observed, p.trips_late,
       null, p.median_worst_delay_s::float8
from contract.transit_punctuality p
where p.source_ref = any($1) and ` + permitted("p") + ` and $2::timestamptz is not null
order by 1, 2, 4`,
	"api_national_accessibility": `
select 'facilities', f.facility_kind || '/' || coalesce(f.facility_status, 'unknown'), null::text, f.source_ref,
       count(*)::int, count(distinct f.stop_place_id)::int, null::float8, null::float8
from contract.station_facility f
where f.source_ref = any($1) and ` + permitted("f") + ` and $2::timestamptz is not null
group by 2, 4
order by 1, 2, 4`,
	"api_national_weather": `
select 'warnings', w.warning_level::text, null::text, w.source_ref, count(*)::int, count(distinct w.warn_cell_id)::int, null::float8, null::float8
from contract.weather_warning w
where w.withdrawn_at is null and w.message_status <> 'cancelled' and w.source_ref = any($1) and ` + permitted("w") + `
  and w.valid_from <= $2 and (w.valid_to is null or w.valid_to > $2)
group by 2, 4
order by 1, 2, 4`,
	"api_storage_answers": `select true`,
}

// legacyRegionAlerts is the alerts CTE as it read the view.
//
//nolint:misspell // materialized is the SQL keyword
var legacyRegionAlerts = strings.NewReplacer("{live}", alertLive("a", "$1"), "{inforce}", alertInForce("a", "$1")).Replace(`in_force as materialized (
  select a.* from contract.service_alert a
  where a.source_ref = any($2) and ` + "a.display_class <> 'withhold' and (a.audience = 'public' or " + ownKeyOn + ")" + `
    and ` + "{live}" + ` and ` + "{inforce}" + `
), o as (
  select f.*, (not f.primary_source and exists (
           select 1 from in_force g where g.primary_source
              and (g.dedupe_key = f.dedupe_key or (f.message_ref is not null and g.message_ref = f.message_ref)))) as duplicate
  from in_force f
  where ($3::text is null
         or exists (select 1 from unnest(f.district_codes) k where k like $3::text || '%' or (length($3::text) > 5 and $3::text like k || '%'))
         or (f.placed_by = 'network' and length($3::text) = 2 and $3::text = any(f.state_codes)))
    and ($4::text is null or exists (select 1 from unnest(f.line_names) n where upper(regexp_replace(n, '\s+', '', 'g')) = $4::text))
)`)

// legacyStationsInTowns is the town search (fault 2 of the 1.1.2 faults)
// as the view answers it: the stations of the districts the town names
// whose name holds the stop name, by name then id, collapsed as the name
// search is (legacyStationsPage).
// legacyStationRegister is the station register's row of a stop place (the
// stations carry its name and family since the register reads, as the
// platform's station read): the open DB station whose IFOPT is the stop's
// id, StaDa's first.
const legacyStationRegister = `left join lateral (select x.name, x.source_id from db_stations x
                    where x.ifopt = substr(s.stop_place_id, strpos(s.stop_place_id, ':') + 1) and x.closed_at is null
                    order by (x.source_id = 'db_stada') desc, x.station_no limit 1) reg on true`

var legacyStationsInTowns = `
select s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code,
       coalesce(s.modes, '{}'), s.source_ref, null::integer, s.placement_source_ref, s.eva_no, s.eva_numbers,
       reg.name, reg.source_id
from contract.stop_place s
` + legacyStationRegister + `
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1)
  and ` + permitted("s") + `
  and left(s.region_code, 5) = any($4::text[])
  and (s.search_fold like '%' || $2::text || '%' or s.search_fold like any(array(select '%' || x || '%' from unnest($3::text[]) x)))
  and not exists (
        select 1 from contract.stop_place r
         where r.withdrawn_at is null and r.stop_place_id <> s.stop_place_id
           and (r.stop_place_id = split_part(s.stop_place_id, ':', 1) || ':' || substring(s.origin_key from '` + dhidStation + `')
                or (r.stop_place_id like split_part(s.stop_place_id, ':', 1) || ':' || substring(s.origin_key from '` + dhidStation + `') || ':%'
                    and r.stop_place_id ~<~ s.stop_place_id)))
order by s.name, ` + legacyStationTies + `, s.stop_place_id
limit $5`

// legacyStationTies is a deliberate rule change carried into the frozen
// station searches (#229 B, #191 A3): the stops of one name a text finds
// come with an EVA number first, then those whose family has a planned call
// of a live trip, then those filed under a DHID, then by id, as the
// platform's stop resolver weighs them; legacyTextTies the same for a text
// search only, a search with no text keeping its order. The keys are the
// statement's own (stationTies, over the view's row): what the replay
// holds to the frozen order is the page, a sort of every match against the
// statement's ranked cut of each name at its limit.
var (
	legacyStationTies = stationTies("s")
	legacyTextTies    = `case when $4::text = '' then null else array[` + legacyStationTies + `] end`
)

func init() {
	legacyReads["api_stations_in_towns"] = legacyStationsInTowns
	legacyReads["api_stations_in_towns_railway"] = strings.Replace(legacyStationsInTowns, "order by s.name", "and s.eva_no is not null\norder by s.name", 1)
	for m := range 1 << 5 {
		legacyReads[fmt.Sprintf("api_stations_page_%02d", m)] = legacyStationsPage
	}
}

// legacyStationsPage is the station search as it read the view, every
// filter optional, for each of its shapes; with the main-station spellings
// ($10) carried from the platform's station search on purpose (#526), empty
// for none, where the statement reads as the view did.
var legacyStationsPage = `
with here as (
  select case when $2::float8 is null then null
              else st_setsrid(st_makepoint($3::float8, $2::float8), 4326)::geography end g)
select s.stop_place_id, s.name, st_y(s.location), st_x(s.location), s.national_stop_code, s.region_code,
       coalesce(s.modes, '{}'), s.source_ref,
       case when here.g is null then null else round(st_distance(s.location::geography, here.g))::int end dist,
       s.placement_source_ref, s.eva_no, s.eva_numbers, reg.name, reg.source_id
from contract.stop_place s cross join here
` + legacyStationRegister + `
where s.withdrawn_at is null
  and s.location is not null
  and s.source_ref = any($1)
  and ` + permitted("s") + `
  and ($4::text = '' or s.search_fold like '%' || $4::text || '%' or s.search_fold like any(array(select '%' || x || '%' from unnest($10::text[]) x)))
  and (here.g is null or st_dwithin(s.location::geography, here.g, $5))
  and ($7::text = '' or s.eva_no = $7::text or s.national_stop_code = $7::text)
  and (not $8::bool or s.eva_no is not null)
  and ($9::text = '' or s.region_code like $9::text || '%')
  and ($4::text = '' or not exists (
        select 1 from contract.stop_place r
         where r.withdrawn_at is null and r.stop_place_id <> s.stop_place_id
           and (r.stop_place_id = split_part(s.stop_place_id, ':', 1) || ':' || substring(s.origin_key from '` + dhidStation + `')
                or (r.stop_place_id like split_part(s.stop_place_id, ':', 1) || ':' || substring(s.origin_key from '` + dhidStation + `') || ':%'
                    and r.stop_place_id ~<~ s.stop_place_id))))
order by case when here.g is null and not (s.search_fold like $4::text || '%' or s.search_fold like any(array(select x || '%' from unnest($10::text[]) x))) then 1 else 0 end,
         dist, s.name, ` + legacyTextTies + `, s.stop_place_id
limit $6`

// LegacyRead is the frozen view statement the named statement name replaced.
func LegacyRead(name string) string { return legacyReads[name] }
