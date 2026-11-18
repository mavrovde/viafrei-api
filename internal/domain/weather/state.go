package weather

import "time"

// StateWarning is one event of the official warnings in force in one Land,
// as the platform's Land page lists them: the highest level, how many
// warnings (by the publisher's identifier) and how many warn cells they
// cover, and the latest end. The Land is the warn cell's own key: a
// Gemeinde cell (8 and its official key) or a coast or district cell (7 and
// its key), digits 2 and 3 the Land.
type StateWarning struct {
	// Event is the publisher's event name; nil when the warning names none.
	Event   *string
	Level   int
	Notices int
	Cells   int
	// Until is the latest end of the event's warnings; nil when none states
	// one.
	Until    *time.Time
	Families []string
}
