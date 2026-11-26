//go:build integration

package store

// The frozen view statement of the place read (legacyReads).

func init() {
	legacyReads["api_place_by_id"] = `
select place_id, name, place_kind, st_y(location), st_x(location), source_ref
from contract.place
where place_id = $1::text and withdrawn_at is null
  -- carried on purpose (#285): a station of the stop families $3 opens too
  and (source_ref = any($2::text[]) or place_kind = 'station_area' and source_ref = any($3::text[])
       and exists (select from public.gazetteer_places g where g.id::text = place_id and g.kind = 'station'))
  and ` + permitted("place")
}
