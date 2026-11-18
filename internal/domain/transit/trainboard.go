package transit

import "time"

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
