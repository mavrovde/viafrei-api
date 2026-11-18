package transit

import (
	"cmp"
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
	// RouteType is the timetable's route type code.
	RouteType   int
	TrainNumber *string
	// Category is the train category (ICE, RE, S); nil off the railway.
	Category *string
	// Headsign is the destination the trip is signed for; nil when it is
	// signed for none. Destination is what the board shows: the headsign,
	// else the name of the trip's last stop.
	Headsign    *string
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

// DepartureQuery asks the board of a station: the departures planned in
// [From, From+Window), of the given modes (none: every mode) and train
// categories (none: every category), at most Limit.
type DepartureQuery struct {
	StationID  string
	From       time.Time
	Window     time.Duration
	Modes      []Mode
	Categories []string
	Limit      int
}

// BoardAnswer is a station's board: the rows shown, how many departures
// of the asked modes and categories the window holds (Matched), how many it
// holds of every mode and category (MatchedAll), and how many of those the
// filter left out, per route type (Dropped).
type BoardAnswer struct {
	Rows       []Departure
	Matched    int
	MatchedAll int
	Dropped    map[int]int
}

// Board is the board of a station from its departing calls in the window
// (calls a passenger can board, of trips that call somewhere outside the
// station after it), as the platform's departure board reads them:
//
//   - one line to one destination at one instant is one departure, whatever
//     stop point published it (a row with no line or no headsign keeps its
//     stop point in its key);
//   - one destination from one platform at one instant is one vehicle,
//     whatever the line is called (a row with no headsign or no platform is
//     its own key);
//   - in both, the lowest journey id is kept;
//   - the rows are those of the asked modes and categories, by planned
//     instant, then line (none last), then journey, then call; at most
//     limit.
func Board(calls []Departure, q DepartureQuery) BoardAnswer {
	sorted := slices.Clone(calls)
	slices.SortStableFunc(sorted, func(a, b Departure) int {
		return cmp.Or(a.PlannedAt.Compare(b.PlannedAt), cmp.Compare(a.JourneyID, b.JourneyID), cmp.Compare(a.CallOrder, b.CallOrder))
	})
	sameLine := keep(sorted, func(d Departure) string {
		k := text(d.Line) + "\x00" + text(d.Headsign)
		if d.Line == nil || d.Headsign == nil {
			k += "\x00" + d.StopPointID
		}
		return k
	})
	uniq := keep(sameLine, func(d Departure) string {
		dest, plat := "j"+d.JourneyID, "j"+d.JourneyID
		if d.Headsign != nil {
			dest = "h" + *d.Headsign
		}
		if d.PlannedPlatform != nil {
			plat = "p" + *d.PlannedPlatform
		}
		return dest + "\x00" + plat
	})
	out := BoardAnswer{Rows: []Departure{}, MatchedAll: len(uniq), Dropped: map[int]int{}}
	for _, d := range uniq {
		if !asked(d, q) {
			out.Dropped[d.RouteType]++
			continue
		}
		out.Matched++
		out.Rows = append(out.Rows, d)
	}
	slices.SortStableFunc(out.Rows, func(a, b Departure) int {
		return cmp.Or(a.PlannedAt.Compare(b.PlannedAt), lineOrder(a.Line, b.Line), cmp.Compare(a.JourneyID, b.JourneyID), cmp.Compare(a.CallOrder, b.CallOrder))
	})
	if len(out.Rows) > q.Limit {
		out.Rows = out.Rows[:q.Limit]
	}
	return out
}

// keep keeps, of the rows that share an instant and a key, the first: rows
// come in instant then journey order, so the lowest journey id.
func keep(rows []Departure, key func(Departure) string) []Departure {
	out := []Departure{}
	seen := map[string]bool{}
	for _, d := range rows {
		k := d.PlannedAt.UTC().Format(time.RFC3339Nano) + "\x00" + key(d)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, d)
	}
	return out
}

func asked(d Departure, q DepartureQuery) bool {
	if len(q.Modes) > 0 && !slices.Contains(q.Modes, d.Mode) {
		return false
	}
	return len(q.Categories) == 0 || (d.Category != nil && slices.Contains(q.Categories, *d.Category))
}

func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// lineOrder orders lines ascending, a missing line last.
func lineOrder(a, b *string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return cmp.Compare(*a, *b)
}
