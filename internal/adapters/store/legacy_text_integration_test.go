//go:build integration

package store

// legacyWarningText are the text columns the view never carried (#222), read
// from the warning's own row: the frozen statements gain them as the named
// ones did, so the replay still compares every other column.
const legacyWarningText = `left join lateral (select x.description, x.instruction, x.urgency, x.certainty
  from weather_warnings x where 'dwd:' || x.id = w.warning_id) x on true
`
