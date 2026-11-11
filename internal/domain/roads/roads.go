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

// Canonical writes a road reference without spaces and in upper case. A
// stored reference is shown in this form too.
func Canonical(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, " ", ""))
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
	// decimal; nil where it cannot be placed on the road.
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

// LampOf is the lamp rule (FR-ROAD-004). It reads the severities of every
// situation that answers the question, not only those on the page:
//
//   - red when any situation closes the road (road_closed);
//   - amber when the worst is major or lane_closed, or when a situation's
//     severity cannot be graded (other), since an effect the service cannot
//     grade is never shown as green;
//   - green when the worst is minor or info, or when there is none.
//
// The reason is the severity that decided the lamp: the worst graded one, or
// other when an ungraded one made the lamp amber; nil for a green lamp with
// no situation.
func LampOf(severities []Severity) (Lamp, *Severity) {
	var worst *Severity
	ungraded := false
	for _, s := range severities {
		if s.Rank() == 0 {
			ungraded = true
			continue
		}
		if worst == nil || s.Rank() > worst.Rank() {
			v := s
			worst = &v
		}
	}
	switch {
	case worst != nil && *worst == SeverityRoadClosed:
		return LampRed, worst
	case worst != nil && worst.Rank() >= SeverityLaneClosed.Rank():
		return LampAmber, worst
	case ungraded:
		other := SeverityOther
		return LampAmber, &other
	default:
		return LampGreen, worst
	}
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
