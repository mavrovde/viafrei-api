package transit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
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

// boardDayS is the day, in seconds, a recorded time may lie from the time it
// is checked against.
const boardDayS = 24 * 60 * 60

const boardDay = boardDayS * time.Second

// The bounds of a recorded board: a DB Timetables board lists at most 120
// departures, and each text is as short as the table holds it.
const (
	MaxBoardDepartures = 120
	maxShortText       = 16
	maxLineText        = 32
	maxDestination     = 120
)

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

// ErrBoardInvalid wraps the field a recorded board breaks.
var ErrBoardInvalid = errors.New("transit: the board is invalid")

// BoardFieldError names the field a recorded board breaks, as a JSON
// pointer-like path (departures[3].plannedAt).
type BoardFieldError struct {
	Field, Detail string
}

func (e *BoardFieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Detail) }

func (e *BoardFieldError) Unwrap() error { return ErrBoardInvalid }

var evaNumber = regexp.MustCompile(`^[0-9]{7}$`)

// ValidEVA is whether s is a seven-digit EVA number.
func ValidEVA(s string) bool { return evaNumber.MatchString(s) }

// Validate checks a board the MCP server hands over at now: a seven-digit
// EVA number, a fetch no later than now and no older than a day, at most
// MaxBoardDepartures departures, each planned within a day of the fetch
// and each text non-blank and within its length.
func (b TrainBoard) Validate(now time.Time) error {
	if !ValidEVA(b.EVA) {
		return &BoardFieldError{"eva", "must be a seven-digit EVA number"}
	}
	if b.FetchedAt.IsZero() || b.FetchedAt.After(now.Add(time.Minute)) || b.FetchedAt.Before(now.Add(-boardDay)) {
		return &BoardFieldError{"fetchedAt", "must lie within the last day and not in the future"}
	}
	if len(b.Departures) > MaxBoardDepartures {
		return &BoardFieldError{"departures", fmt.Sprintf("must list at most %d departures", MaxBoardDepartures)}
	}
	for i, d := range b.Departures {
		at := fmt.Sprintf("departures[%d].", i)
		if d.PlannedAt.Before(b.FetchedAt.Add(-boardDay)) || d.PlannedAt.After(b.FetchedAt.Add(boardDay)) {
			return &BoardFieldError{at + "plannedAt", "must lie within a day of fetchedAt"}
		}
		if d.ChangedAt != nil && (d.ChangedAt.Before(d.PlannedAt.Add(-boardDay)) || d.ChangedAt.After(d.PlannedAt.Add(boardDay))) {
			return &BoardFieldError{at + "changedAt", "must lie within a day of plannedAt"}
		}
		for _, f := range []struct {
			name string
			v    *string
			max  int
		}{
			{"platform", d.Platform, maxShortText}, {"changedPlatform", d.ChangedPlatform, maxShortText},
			{"category", d.Category, maxShortText}, {"number", d.Number, maxShortText},
			{"line", d.Line, maxLineText}, {"destination", d.Destination, maxDestination},
		} {
			if f.v != nil && (strings.TrimSpace(*f.v) == "" || len(*f.v) > f.max) {
				return &BoardFieldError{at + f.name, fmt.Sprintf("must be non-blank and at most %d bytes, or absent", f.max)}
			}
		}
	}
	return nil
}
