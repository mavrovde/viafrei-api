//go:build integration

package store

// legacyWarningText are the text columns the view never carried (#222), read
// from the warning's own row: the frozen statements gain them as the named
// ones did, so the replay still compares every other column.
const legacyWarningText = `left join lateral (select x.description, x.instruction, x.urgency, x.certainty
  from weather_warnings x where 'dwd:' || x.id = w.warning_id) x on true
`

// legacyFacilitySeen is what the view never carried of a facility (#222),
// read from its own row: when its source last restated its state (seen),
// and the DB equipment number an OpenStation unit carries (eno), which
// pairs it with DB FaSta's row of the same unit.
const legacyFacilitySeen = `left join lateral (select coalesce((select st.last_seen_at from station_equipment_state st where 'equipment:' || st.equipment_id = f.facility_id),
                                     (select e.last_seen_at from station_equipment e where 'equipment:' || e.equipment_id = f.facility_id),
                                     (select d.last_seen_at from db_facilities d where 'fasta:' || d.equipment_no = f.facility_id)) seen,
                            (select e.equipment_no from station_equipment e where 'equipment:' || e.equipment_id = f.facility_id) eno) x on true`
