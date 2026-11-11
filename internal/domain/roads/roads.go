// Package roads is the road-conditions context: road situations (closures,
// roadworks, jams, hazards, restrictions) on a motorway or near a position,
// and the lamp that sums them up.
package roads

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// Other is the value of every open enumeration for a stored code the service
// cannot map (ENV-051): a raw storage code never reaches an answer.
const Other = "other"

// ErrUnknownRoad means no road with the asked reference is held.
var ErrUnknownRoad = errors.New("roads: no such road")

// ErrUnknownSituation means no situation with the asked id is held.
var ErrUnknownSituation = errors.New("roads: no such situation")

// ErrRoadReference is the error of a road reference that cannot be read.
var ErrRoadReference = errors.New("roads: not a road reference")

// Kind is what a situation is.
type Kind string

// The known kinds.
const (
	KindClosure     Kind = "closure"
	KindRoadworks   Kind = "roadworks"
	KindJam         Kind = "jam"
	KindAccident    Kind = "accident"
	KindHazard      Kind = "hazard"
	KindRestriction Kind = "restriction"
	KindWeather     Kind = "weather"
	KindOther       Kind = Other
)

// Kinds lists every kind an answer can carry; every one but the last is a
// stored code the service maps one to one.
var Kinds = []Kind{KindClosure, KindRoadworks, KindJam, KindAccident, KindHazard, KindRestriction, KindWeather, KindOther}

// MapKind maps a stored kind code.
func MapKind(raw string) Kind { return mapCode(raw, Kinds[:7], KindOther) }

// Severity is the effect of a situation on traffic, ordered from info to
// road_closed. A stored code the service cannot map is other, which has no
// place in the order.
type Severity string

// The known severities, in ascending order.
const (
	SeverityInfo       Severity = "info"
	SeverityMinor      Severity = "minor"
	SeverityLaneClosed Severity = "lane_closed"
	SeverityMajor      Severity = "major"
	SeverityRoadClosed Severity = "road_closed"
	SeverityOther      Severity = Other
)

// Severities lists every severity an answer can carry; the first five in
// ascending order.
var Severities = []Severity{SeverityInfo, SeverityMinor, SeverityLaneClosed, SeverityMajor, SeverityRoadClosed, SeverityOther}

// Ordered returns the five ordered severities, lowest first.
func Ordered() []Severity { return slices.Clone(Severities[:5]) }

// MapSeverity maps a stored severity code.
func MapSeverity(raw string) Severity { return mapCode(raw, Severities[:5], SeverityOther) }

// Rank is the place of a severity in the order, from 1 (info) to 5
// (road_closed); other has the rank 0.
func (s Severity) Rank() int { return slices.Index(Severities[:5], s) + 1 }

// Floor is the lowest rank a min_severity filter keeps: info keeps every
// situation, including one whose severity cannot be graded; any higher
// minimum keeps only the graded situations at or above it.
func (s Severity) Floor() int {
	if s == SeverityInfo {
		return 0
	}
	return s.Rank()
}

// Direction is the carriageway a situation affects.
type Direction string

// The known directions.
const (
	DirectionNorth   Direction = "north"
	DirectionEast    Direction = "east"
	DirectionSouth   Direction = "south"
	DirectionWest    Direction = "west"
	DirectionBoth    Direction = "both"
	DirectionUnknown Direction = "unknown"
)

// Directions lists every direction an answer can carry.
var Directions = []Direction{DirectionNorth, DirectionEast, DirectionSouth, DirectionWest, DirectionBoth, DirectionUnknown}

// Compass lists the four directions of travel a filter can ask for.
func Compass() []Direction { return slices.Clone(Directions[:4]) }

// MapDirection maps a stored direction code; a code the service cannot map
// is unknown, since a direction is a closed enumeration.
func MapDirection(raw string) Direction { return mapCode(raw, Directions[:5], DirectionUnknown) }

// Class is the class of the road a situation is on.
type Class string

// The known road classes.
const (
	ClassMotorway Class = "motorway"
	ClassFederal  Class = "federal"
	ClassState    Class = "state"
	ClassUrban    Class = "urban"
	ClassOther    Class = Other
)

// Classes lists every road class an answer can carry.
var Classes = []Class{ClassMotorway, ClassFederal, ClassState, ClassUrban, ClassOther}

// MapClass maps a stored road class code.
func MapClass(raw string) Class { return mapCode(raw, Classes[:4], ClassOther) }

func mapCode[T ~string](raw string, known []T, other T) T {
	if i := slices.Index(known, T(raw)); i >= 0 {
		return known[i]
	}
	return other
}

// roadReference is a national road designation (CONV-011): a motorway (A)
// or federal road (B) letter, an optional space, one to four digits.
var roadReference = regexp.MustCompile(`^[AaBb] ?[0-9]{1,4}$`)

// ParseRoad reads a road reference case-insensitively, with an optional
// space after the letter, and returns its canonical form (A8, B27).
func ParseRoad(s string) (string, error) {
	if !roadReference.MatchString(s) {
		return "", ErrRoadReference
	}
	return Canonical(s), nil
}

// Canonical writes a road reference without spaces, in upper case and
// without leading zeros in its number (CONV-011: a08 and A 8 are A8). A
// stored reference is shown in this form too.
func Canonical(s string) string {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if s == "" {
		return s
	}
	number := strings.TrimLeft(s[1:], "0")
	if number == "" && len(s) > 1 {
		number = "0"
	}
	return s[:1] + number
}

// Road is a road the service holds.
type Road struct {
	// Ref is the canonical reference (A8).
	Ref   string
	Class Class
	// LengthKm is the road's length; nil when it is not known.
	LengthKm *float64
	// States are the ISO 3166-2 codes of the states the road crosses.
	States []string
}

// Situation is one thing that affects travel on a road.
type Situation struct {
	ID        string
	Family    string
	Kind      Kind
	Severity  Severity
	Road      *string
	Class     Class
	Direction Direction
	// Towards is the signed destination of the carriageway, as published.
	Towards  *string
	Location *geo.Point
	From     time.Time
	// Until is nil when the situation is open-ended.
	Until       *time.Time
	LanesClosed *int
	LanesTotal  *int
	DelayMin    *int
	LengthM     *int
	Headline    string
	Description *string
	// TextLanguage is the language of Headline and Description.
	TextLanguage string
	UpdatedAt    time.Time
	// AtKm is the situation's position along its road in kilometres, one
	// decimal, projected onto the road's line; nil where it cannot be
	// placed on the road.
	AtKm *float64
	// DistanceM is the distance from an asked position; nil when the
	// question asked for none.
	DistanceM *int
}

// Lamp sums up the situations of a road or an area.
type Lamp string

// The lamps.
const (
	LampGreen   Lamp = "green"
	LampAmber   Lamp = "amber"
	LampRed     Lamp = "red"
	LampUnknown Lamp = "unknown"
)

// Lamps lists every lamp an answer can carry.
var Lamps = []Lamp{LampGreen, LampAmber, LampRed, LampUnknown}

// Signal is what the lamp reads of one situation that answers the question.
type Signal struct {
	Kind     Kind
	Severity Severity
	// DelayMin is the published delay; nil when none is published.
	DelayMin *int
}

// SignalOf is the lamp's view of a situation.
func SignalOf(s Situation) Signal {
	return Signal{Kind: s.Kind, Severity: s.Severity, DelayMin: s.DelayMin}
}

// Reason says why the lamp has its colour. Open enumeration.
type Reason string

// The reasons, red ones first, each colour's in the order the rule tests
// them.
const (
	// ReasonRoadClosed: a closure of the carriageway (severity road_closed,
	// whatever the kind).
	ReasonRoadClosed Reason = "road_closed"
	// ReasonSafety: a safety message: a hazard on the road (a wrong-way
	// driver, an object, a person or an animal), or an accident graded major.
	ReasonSafety Reason = "safety"
	// ReasonLongJam: a jam with a published delay of JamRedMin or more.
	ReasonLongJam Reason = "long_jam"
	// ReasonSevereWeather: a weather situation graded major, which stands
	// for an official warning of level 3 or 4.
	ReasonSevereWeather Reason = "severe_weather"
	// ReasonPartial: a roads family the key may not read was left out, so
	// the service cannot say the road is clear (lamp unknown).
	ReasonPartial Reason = "partial"
	// ReasonJam: a shorter jam, or one with no published delay.
	ReasonJam Reason = "jam"
	// ReasonNarrowing: roadworks that close a lane or more (lane_closed or
	// major).
	ReasonNarrowing Reason = "narrowing"
	// ReasonWarning: any other situation the publisher reports, including
	// one whose kind or severity cannot be mapped.
	ReasonWarning Reason = "warning"
	// ReasonStaleSource: a roads family the answer read has no data or
	// stale data, so the sight is limited.
	ReasonStaleSource Reason = "stale_source"
)

// Reasons lists every reason, in the order above.
var Reasons = []Reason{ReasonRoadClosed, ReasonSafety, ReasonLongJam, ReasonSevereWeather, ReasonPartial,
	ReasonJam, ReasonNarrowing, ReasonWarning, ReasonStaleSource}

// JamRedMin is the delay from which a jam makes the lamp red, in minutes;
// the platform's motorway lamp uses the same threshold.
const JamRedMin = 30

// signal returns the colour one situation gives on its own and why; the
// empty lamp when it gives none (roadworks that narrow no lane). This is the
// documented mapping of the service's codes onto the platform's motorway
// lamp:
//
//	severity road_closed                 red    road_closed
//	kind hazard                          red    safety
//	kind accident, severity major        red    safety
//	kind jam, delay >= JamRedMin         red    long_jam
//	kind weather, severity major         red    severe_weather
//	kind jam, otherwise                  amber  jam
//	kind roadworks, lane_closed or major amber  narrowing
//	kind roadworks, info or minor        none
//	anything else, also unmapped codes   amber  warning
//
// A situation the service cannot grade (severity other) is never green.
func signal(s Signal) (Lamp, Reason) {
	if s.Severity == SeverityRoadClosed {
		return LampRed, ReasonRoadClosed
	}
	if s.Kind == KindHazard || s.Kind == KindAccident && s.Severity == SeverityMajor {
		return LampRed, ReasonSafety
	}
	if s.Kind == KindJam && s.DelayMin != nil && *s.DelayMin >= JamRedMin {
		return LampRed, ReasonLongJam
	}
	if s.Kind == KindWeather && s.Severity == SeverityMajor {
		return LampRed, ReasonSevereWeather
	}
	if s.Kind == KindJam {
		return LampAmber, ReasonJam
	}
	if s.Kind == KindRoadworks && (s.Severity == SeverityLaneClosed || s.Severity == SeverityMajor) {
		return LampAmber, ReasonNarrowing
	}
	if s.Kind == KindRoadworks && s.Severity != SeverityOther {
		return "", ""
	}
	return LampAmber, ReasonWarning
}

// LampOf is the lamp rule (FR-ROAD-004), the platform's motorway lamp. It
// reads every situation that answers the question, not only those on the
// page, whether the answer is partial and whether a family it read is stale;
// first match from the top:
//
//   - red when a situation gives red (see signal);
//   - unknown when the answer is partial: a family the key may not read
//     could hold a closure, so the road is never shown amber or green;
//   - amber when a situation gives amber, or when a family read is missing
//     or stale (limited sight);
//   - green otherwise: nothing reported and every family fresh. Green is
//     never a guarantee that the road is free.
//
// The reason is the first in Reasons that decided the colour; nil for green.
func LampOf(signals []Signal, partial, stale bool) (Lamp, *Reason) {
	found := map[Reason]Lamp{}
	for _, s := range signals {
		if lamp, reason := signal(s); lamp != "" {
			found[reason] = lamp
		}
	}
	if partial {
		found[ReasonPartial] = LampUnknown
	}
	if stale {
		found[ReasonStaleSource] = LampAmber
	}
	for _, r := range Reasons {
		if lamp, ok := found[r]; ok {
			return lamp, &r
		}
	}
	return LampGreen, nil
}

// Filter narrows the situations a question reads.
type Filter struct {
	// Kinds keeps the situations of these kinds; empty keeps every kind.
	// KindOther keeps the situations whose kind cannot be mapped.
	Kinds []Kind
	// MinSeverity keeps the situations at or above it (see Severity.Floor).
	MinSeverity Severity
}

// KnownKinds returns the mapped kinds a filter keeps, and whether it keeps
// the kinds that cannot be mapped. No kinds keeps every kind: every mapped
// one and the unmapped ones.
func (f Filter) KnownKinds() ([]Kind, bool) { return split(f.Kinds, Kinds) }

// split separates the mapped values of a filter list from its other, the
// last value of every. An empty list keeps everything.
func split[T comparable](list, every []T) ([]T, bool) {
	other := every[len(every)-1]
	if len(list) == 0 {
		return slices.Clone(every[:len(every)-1]), true
	}
	known := []T{}
	for _, v := range list {
		if v != other {
			known = append(known, v)
		}
	}
	return known, slices.Contains(list, other)
}

// CorridorQuery asks for the situations on one road.
type CorridorQuery struct {
	Road string
	// Direction keeps the situations on this carriageway, on both, and
	// those whose direction is unknown; DirectionBoth keeps every one.
	Direction Direction
	Filter
	Limit int
}

// AreaQuery asks for the situations near a position.
type AreaQuery struct {
	At      geo.Point
	RadiusM int
	// Classes keeps the situations on roads of these classes; empty keeps
	// every class.
	Classes []Class
	Filter
	Limit int
}

// KnownClasses returns the mapped classes the query keeps, and whether it
// keeps the classes that cannot be mapped. No classes keeps every class.
func (q AreaQuery) KnownClasses() ([]Class, bool) { return split(q.Classes, Classes) }
