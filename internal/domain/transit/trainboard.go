package transit

import (
	"slices"
	"time"
)

// TrainBoardFamily is the one family a train board is of: DB Timetables,
// CC BY 4.0. No share-alike family is ever blended into a board.
const TrainBoardFamily = "db_timetables"

// TrainBoardFreshS is how old, in seconds, a stored board may be and still be served:
// the MCP server's own cache lifetime of the DB Timetables changes it read
// the board's changed times and platforms from (CHANGES_TTL_MS, 30 s).
const TrainBoardFreshS = 30

// TrainBoardFreshFor is TrainBoardFreshS as a duration.
const TrainBoardFreshFor = TrainBoardFreshS * time.Second

// TrainBoard is one station's DB Timetables board as the MCP server fetched
// it live: the station's EVA number, the fetch time and its departures.
type TrainBoard struct {
	EVA        string
	FetchedAt  time.Time
	Departures []TrainDeparture
	// Stored is a board the stored-boards worker wrote (TrainBoardOf):
	// the station's EVA numbers merged, what it covers, and whether its
	// read was degraded.
	Stored      bool
	EVAs        []string
	CoversUntil *time.Time
	Degraded    bool
}

// TrainDeparture is one departure of a board: the planned time and the
// changed one the board stated, the planned and changed platform, the
// train's category, number and line, its destination and whether the
// board stated it cancelled.
type TrainDeparture struct {
	PlannedAt       time.Time
	ChangedAt       *time.Time
	Platform        *string
	ChangedPlatform *string
	Category        *string
	Number          *string
	Line            *string
	Destination     *string
	Cancelled       bool
	// StationPart is the part of the station the departure leaves from,
	// when the board states it; neither the recorded nor the stored board
	// does yet.
	StationPart *string
}

// ExpectedAt is the time the board expects the departure at: the changed
// time where it stated one, else the planned one.
func (d TrainDeparture) ExpectedAt() time.Time {
	if d.ChangedAt != nil {
		return *d.ChangedAt
	}
	return d.PlannedAt
}

// DelayMin is the board's delay in whole minutes, rounded as the MCP
// server rounds it.
func (d TrainDeparture) DelayMin() int {
	return int(d.ExpectedAt().Sub(d.PlannedAt).Round(time.Minute) / time.Minute)
}

// PlatformChanged is whether the board moved the departure to another
// platform.
func (d TrainDeparture) PlatformChanged() bool {
	return d.ChangedPlatform != nil && (d.Platform == nil || *d.ChangedPlatform != *d.Platform)
}

// StoredBoardFreshMin is how old, in minutes, a board the stored-boards
// worker wrote may be and still be served: the platform's
// STORED_BOARD_FRESH_MIN.
const StoredBoardFreshMin = 20

// StoredBoardFreshFor is StoredBoardFreshMin as a duration.
const StoredBoardFreshFor = StoredBoardFreshMin * time.Minute

// The board's window and size: the MCP server's train departures defaults
// and bounds (duration_min 5 to 120, default 60), and the API's limit
// (1 to 50, default 5).
const (
	BoardDurationMinDefault = 60
	BoardDurationMinMax     = 120
	BoardLimitDefault       = 5
	BoardLimitMax           = 50
)

// StoredBoard is a station's board as the stored-boards worker wrote it:
// the station's EVA numbers merged into one list, the read time, whether
// the read was degraded, the instant before which it lists every departure
// the board held, and its departures. Unreadable counts the departures
// left out because their planned time or line could not be read; the board
// is then degraded, as the platform reads it.
type StoredBoard struct {
	EVAs        []string
	ReadAt      time.Time
	Degraded    bool
	CoversUntil time.Time
	Departures  []StoredDeparture
}

// StoredDeparture is one departure as the stored board holds it.
type StoredDeparture struct {
	Line            *string
	Category        *string
	Destination     *string
	PlannedAt       *string
	DelayMin        *int
	PlannedPlatform *string
	Platform        *string
	PlatformChanged *bool
	Cancelled       *bool
}

// TrainBoardOf reads a stored board as the platform reads it
// (storedDbBoardOf): a departure without a line or a readable planned time
// is left out and the board is then degraded; a missing delay is none.
func TrainBoardOf(eva string, s StoredBoard) TrainBoard {
	b := TrainBoard{EVA: eva, FetchedAt: s.ReadAt.UTC(), Degraded: s.Degraded, Stored: true, EVAs: s.EVAs}
	covers := s.CoversUntil.UTC()
	b.CoversUntil = &covers
	for _, d := range s.Departures {
		at, err := time.Parse(time.RFC3339Nano, deref(d.PlannedAt))
		if d.Line == nil || err != nil {
			b.Degraded = true
			continue
		}
		t := TrainDeparture{PlannedAt: at.UTC(), Line: d.Line, Category: d.Category, Destination: d.Destination,
			Platform: d.PlannedPlatform, Cancelled: d.Cancelled != nil && *d.Cancelled}
		if d.DelayMin != nil && *d.DelayMin != 0 {
			c := t.PlannedAt.Add(time.Duration(*d.DelayMin) * time.Minute)
			t.ChangedAt = &c
		}
		if d.PlatformChanged != nil && *d.PlatformChanged {
			t.ChangedPlatform = d.Platform
		}
		b.Departures = append(b.Departures, t)
	}
	return b
}

// boardTime is when a departure is on the board's window: a cancelled one
// by its plan, any other by its expected time (departuresInWindow).
func (d TrainDeparture) boardTime() time.Time {
	if d.Cancelled {
		return d.PlannedAt
	}
	return d.ExpectedAt()
}

// BoardCap is what a capped board left out: how many departures of the
// window it did not show, and the planned time of the last one.
type BoardCap struct {
	More  int
	Until time.Time
}

// BoardWindow is the board the MCP server shows: the departures whose
// board time is within [from, from+duration] (and, for a stored board,
// before what it covers), the next to leave first, at most limit, then in
// planned order; and what the limit cut, nil when nothing was.
func BoardWindow(b TrainBoard, from time.Time, durationMin, limit int) ([]TrainDeparture, *BoardCap) {
	until := from.Add(time.Duration(durationMin) * time.Minute)
	var held []TrainDeparture
	for _, d := range b.Departures {
		at := d.boardTime()
		if at.Before(from) || at.After(until) || (b.CoversUntil != nil && !at.Before(*b.CoversUntil)) {
			continue
		}
		held = append(held, d)
	}
	byTime := func(x, y TrainDeparture) int { return x.boardTime().Compare(y.boardTime()) }
	slices.SortStableFunc(held, byTime)
	shown := slices.Clone(held[:min(limit, len(held))])
	slices.SortStableFunc(shown, func(x, y TrainDeparture) int {
		if c := x.PlannedAt.Compare(y.PlannedAt); c != 0 {
			return c
		}
		return x.ExpectedAt().Compare(y.ExpectedAt())
	})
	if len(held) <= len(shown) {
		return shown, nil
	}
	last := slices.MaxFunc(held, func(x, y TrainDeparture) int { return x.PlannedAt.Compare(y.PlannedAt) })
	return shown, &BoardCap{More: len(held) - len(shown), Until: last.PlannedAt}
}
