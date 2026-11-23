//go:build integration

package store

import (
	"strconv"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// The frozen view statements of the parking reads (legacyReads).

const legacyParkingHere = `with here as (select st_setsrid(st_makepoint($2::float8, $1::float8), 4326)::geography g)`

func legacyParkingWhere(q parking.Query) (where, order string) {
	filters := `($7::text is null or f.stored_kind = $7) and ($8::text[] is null or f.amenities @> $8)`
	if q.Road == "" {
		return `$9 = '' and st_dwithin(f.location::geography, here.g, $5) and ` + filters, `8, f.facility_id`
	}
	return `$5::float8 >= 0 and f.facility_id like 'p:%' and (f.road_ref = $9 or exists (
    select 1 from st_dumppoints($10::text::geometry) d
    where st_dwithin(f.location::geography, d.geom::geography, ` + strconv.Itoa(parking.CorridorM) + `))) and ` + filters, `f.name nulls last, f.facility_id`
}

func legacyShiftParams(where string) string {
	return strings.NewReplacer("$10", "$12", "$7", "$8", "$8", "$9", "$9", "$10").Replace(where)
}

func legacyParkingDistance(q parking.Query) string {
	if q.Road != "" {
		return `0`
	}
	return `round(st_distance(f.location::geography, here.g))::int`
}

func legacyParking(road bool) (next, list string) {
	q := parking.Query{}
	if road {
		q.Road = "road"
	}
	where, order := legacyParkingWhere(q)
	next = legacyParkingHere + `
select min(nx.next_at)
from contract.parking_facility f
cross join here
cross join lateral (
  select min(o.observed_at) next_at
  from contract.occupancy_observation o
  where o.facility_id = f.facility_id and o.source_ref = any($4::text[]) and o.observed_at > $6 and ` + held("o") + `) nx
where f.withdrawn_at is null
  and f.source_ref = any($3::text[])
  and ` + held("f") + `
  and ` + where
	list = legacyParkingHere + `
select f.facility_id, f.source_ref, f.name, f.operator_name, f.parking_kind,
       st_y(f.location), st_x(f.location),
       ` + legacyParkingDistance(q) + `,
       o.free_spaces, o.total_spaces, o.occupancy_status, o.observed_at, o.source_ref,
       f.amenities, f.amenities_absent
from contract.parking_facility f
cross join here
left join lateral (
  select free_spaces, total_spaces, occupancy_status, observed_at, source_ref
  from contract.occupancy_observation o
  where o.facility_id = f.facility_id and o.source_ref = any($4::text[]) and o.observed_at <= $7 and ` + held("o") + `
  order by o.observed_at desc
  limit 1) o on true
where f.withdrawn_at is null
  and f.source_ref = any($3::text[])
  and ` + held("f") + `
  and ` + legacyShiftParams(where) + `
  and ($6::int <= 0 or (o.free_spaces >= $6 and (
        select s.stale_after_s is null or o.observed_at >= $7::timestamptz - make_interval(secs => s.stale_after_s)
        from contract.source s where s.source_ref = o.source_ref)))
order by ` + order + `
limit $11`
	return next, list
}

func init() {
	legacyReads["api_parking_next_near"], legacyReads["api_parking_near"] = legacyParking(false)
	legacyReads["api_parking_next_road"], legacyReads["api_parking_road"] = legacyParking(true)
}
