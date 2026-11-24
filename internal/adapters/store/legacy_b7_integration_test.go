//go:build integration

package store

// The frozen view statement of the place read (legacyReads).

func init() {
	legacyReads["api_place_by_id"] = `
select place_id, name, place_kind, st_y(location), st_x(location), source_ref
from contract.place
where place_id = $1::text and withdrawn_at is null and source_ref = any($2::text[]) and ` + permitted("place")
}
