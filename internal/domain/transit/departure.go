package transit

import (
	"slices"
	"time"
)

// Departure is one departure of a station's board: a scheduled call with
// the newest live figure the platform holds for it.
type Departure struct {
	// JourneyID is the trip and its service day; one train is one journey.
	JourneyID   string
	StopPointID string
	CallOrder   int
	Line        *string
	Mode        Mode
	TrainNumber *string
	// Category is the train category (ICE, RE, S); nil off the railway.
	Category    *string
	Destination *string
	PlannedAt   time.Time
	// Platform is the live platform where one is reported, else the
	// planned one; PlannedPlatform is the plan's.
	Platform        *string
	PlannedPlatform *string
	PlatformChanged bool
	// Live is true when a live figure stands for the call; Source names the
	// realtime source it is from, nil without one.
	Live       bool
	Source     *string
	Cancelled  bool
	DelayMin   *int
	ExpectedAt *time.Time
	// NoForecast marks a live source that holds the call but gives no
	// figure for it.
	NoForecast bool
	Family     string
	// Families are every source family that contributed to the row.
	Families []string
}

// DepartureQuery asks the board of a station: the departures planned from
// From for Window, of the given modes (none: every mode) and train
// categories (none: every category), at most Limit.
type DepartureQuery struct {
	StationID  string
	From       time.Time
	Window     time.Duration
	Modes      []Mode
	Categories []string
	Limit      int
}

// Board is the board of a station from its scheduled calls, read in the
// order of their planned instants: one row per train (a journey that calls
// at the station twice is shown at its first call), the rows of the asked
// modes and categories only, and no train that has already left at now (its
// expected departure, else its planned one, before now; a cancelled call has
// not left). It keeps at most limit rows and says whether more stand.
func Board(calls []Departure, q DepartureQuery, now time.Time) ([]Departure, bool) {
	out := []Departure{}
	seen := map[string]bool{}
	for _, d := range calls {
		if seen[d.JourneyID] {
			continue
		}
		seen[d.JourneyID] = true
		if len(q.Modes) > 0 && !slices.Contains(q.Modes, d.Mode) {
			continue
		}
		if len(q.Categories) > 0 && (d.Category == nil || !slices.Contains(q.Categories, *d.Category)) {
			continue
		}
		if departed(d, now) {
			continue
		}
		if len(out) == q.Limit {
			return out, true
		}
		out = append(out, d)
	}
	return out, false
}

// departed is true when the train has left the station at now.
func departed(d Departure, now time.Time) bool {
	if d.Cancelled {
		return false
	}
	at := d.PlannedAt
	if d.ExpectedAt != nil {
		at = *d.ExpectedAt
	}
	return at.Before(now)
}
