package transit

import "time"

// Reach is how far the loaded timetable reaches: Until the end of its last
// service day (a Berlin midnight, excluded) and LoadedAt when it was loaded;
// both nil when no timetable is loaded, LoadedAt nil when no load was
// recorded.
type Reach struct {
	Until    *time.Time
	LoadedAt *time.Time
}
