// Package weather is the weather-risk context: the official weather
// warnings in force at a position, and the lamp that sums them up.
package weather

import (
	"errors"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// Other is the hazard of a stored code the service cannot map (ENV-051).
const Other = "other"

// ErrLevel is the error of a stored warning level outside 1 to 4.
var ErrLevel = errors.New("weather: level outside 1 to 4")

// Hazard is what a warning warns of.
type Hazard string

// Hazards lists every hazard an answer can carry; every one but the last is
// a stored code the service maps one to one.
var Hazards = []Hazard{"thunderstorm", "wind", "rain", "snow", "ice", "fog", "heat", "frost", "uv", "flood", Other}

// MapHazard maps a stored hazard code.
func MapHazard(raw string) Hazard {
	if i := slices.Index(Hazards[:len(Hazards)-1], Hazard(raw)); i >= 0 {
		return Hazards[i]
	}
	return Other
}

// Level is the issuer's warning level, 1 (minor) to 4 (extreme), as issued
// (FR-WEATHER-002): the service never re-grades it.
type Level int

// The bounds of a level.
const (
	MinLevel Level = 1
	MaxLevel Level = 4
)

// CheckLevel refuses a stored level outside 1 to 4 rather than grading it.
func CheckLevel(raw int) (Level, error) {
	if raw < int(MinLevel) || raw > int(MaxLevel) {
		return 0, ErrLevel
	}
	return Level(raw), nil
}

// Warning is one official weather warning.
type Warning struct {
	ID        string
	Family    string
	Hazard    Hazard
	Level     Level
	LevelName string
	Issuer    string
	Headline  string
	// TextLanguage is the language of Headline, as issued.
	TextLanguage string
	From         time.Time
	// Until is nil when the warning is open-ended.
	Until    *time.Time
	AreaName string
}

// Query asks for the warnings in force at a position.
type Query struct {
	At       geo.Point
	MinLevel Level
	// Hazards keeps the warnings of these hazards; empty keeps every
	// hazard. Other keeps the hazards that cannot be mapped.
	Hazards []Hazard
}

// KnownHazards returns the mapped hazards the query keeps, and whether it
// keeps the hazards that cannot be mapped. No hazards keeps every hazard.
func (q Query) KnownHazards() ([]Hazard, bool) {
	if len(q.Hazards) == 0 {
		return slices.Clone(Hazards[:len(Hazards)-1]), true
	}
	known := []Hazard{}
	for _, h := range q.Hazards {
		if h != Other {
			known = append(known, h)
		}
	}
	return known, slices.Contains(q.Hazards, Other)
}

// Lamp sums up the warnings at a position.
type Lamp string

// The lamps.
const (
	LampGreen   Lamp = "green"
	LampAmber   Lamp = "amber"
	LampRed     Lamp = "red"
	LampUnknown Lamp = "unknown"
)

// RedLevel is the lowest level that makes the lamp red.
const RedLevel Level = 3

// MaxWarnings caps the warnings one answer reads. Warnings come worst first,
// so the cap never hides the warning that decides the lamp.
const MaxWarnings = 100

// LampOf is the weather lamp, the platform's warning lamp (its dwd signal
// and sight), in the order of the roads lamp. It reads the levels of every
// warning in force at the position, whatever the list's level floor and
// hazards keep, whether the answer is partial and whether a weather family
// read is missing or stale; first match from the top:
//
//   - red when a warning of level 3 or 4 is in force;
//   - unknown when the answer is partial: a family the key may not read
//     could hold a severe warning;
//   - amber for a warning of level 1 or 2, or, with no warning in force,
//     when a family read has no data or stale data: the sight is limited
//     and "no warning" cannot be told from "not heard";
//   - green when no warning is in force and every family is fresh.
func LampOf(levels []Level, partial, stale bool) Lamp {
	worst := Level(0)
	for _, l := range levels {
		worst = max(worst, l)
	}
	if worst >= RedLevel {
		return LampRed
	}
	if partial {
		return LampUnknown
	}
	if worst >= MinLevel || stale {
		return LampAmber
	}
	return LampGreen
}
