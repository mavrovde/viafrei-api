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
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
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

// Lamp sums up the situations of a road or an area: the platform's motorway
// Ampel, whose yellow is amber here.
type Lamp string

// The lamps.
const (
	LampGreen Lamp = "green"
	LampAmber Lamp = "amber"
	LampRed   Lamp = "red"
)

// Lamps lists every lamp an answer can carry.
var Lamps = []Lamp{LampGreen, LampAmber, LampRed}

// Message is what the lamp reads of one situation in the question's scope.
type Message struct {
	Kind        Kind
	Headline    string
	Description *string
	// TextLanguage is the language of Headline and Description.
	TextLanguage string
	// DelayMin is the published delay; nil when none is published.
	DelayMin *int
}

// MessageOf is the lamp's view of a situation.
func MessageOf(s Situation) Message {
	return Message{Kind: s.Kind, Headline: s.Headline, Description: s.Description, TextLanguage: s.TextLanguage, DelayMin: s.DelayMin}
}

// german reports whether a text language is German: the platform's word
// rules are keyword rules over the publisher's German.
func german(lang string) bool {
	lang = strings.ToLower(lang)
	return lang == "de" || strings.HasPrefix(lang, "de-")
}

// Signal is the platform's signal of the situation. The read contract
// carries every publisher's situation in one shape (a kind, a headline, a
// description, a delay), the shape of the platform's police and Land
// message, so it is judged by MessageSignal over the headline and the
// description (What and DetailText), the delay in seconds. A closure or
// roadworks whose description carries the Autobahn GmbH's location line
// ("Auffahrt auf die A9: …", "A45: Hagen -> Gießen, …") takes that line's
// scope, as closureScopeOf reads it. The words are read only in a German
// text; any other is matched by no word, the platform's reading of an
// unmatched text: no safety word, the carriageway's scope.
func (m Message) Signal() Signal {
	am := AmpelMessage{Kind: string(m.Kind)}
	if m.DelayMin != nil {
		d := float64(*m.DelayMin) * 60
		am.DelayS = &d
	}
	de := german(m.TextLanguage)
	if de {
		am.What, am.DetailText = m.Headline, m.Description
	}
	s := MessageSignal(am)
	if de && m.Description != nil && (s.Kind == SignalClosure || s.Kind == SignalRoadworks) {
		var lines []*string
		for _, l := range strings.Split(*m.Description, "\n") {
			lines = append(lines, &l)
		}
		if scope, ok := LineScope(lines); ok {
			s.Scope = scope
		}
	}
	return s
}

// Reason says why the lamp has its colour: the key of the lamp's first
// reason (reasonKeyOf), a jam split by its colour. Open enumeration.
type Reason string

// The reasons.
const (
	// ReasonRoadClosed: a closure of the carriageway (red).
	ReasonRoadClosed Reason = "road_closed"
	// ReasonSafety: a safety message by its words, on any kind: a wrong-way
	// driver, an object, a person or an animal on the road, an accident
	// with a closure of the road (red).
	ReasonSafety Reason = "safety"
	// ReasonBridgeClosed: a bridge closed for wind (red). The read contract
	// holds no bridge states, so it is never given today.
	ReasonBridgeClosed Reason = "bridge_closed"
	// ReasonLongJam: a jam with a delay of 30 minutes or more (red).
	ReasonLongJam Reason = "long_jam"
	// ReasonWeatherWarning: a DWD warning in force on the road or within
	// the radius: red from level 3, amber at levels 1 and 2.
	ReasonWeatherWarning Reason = "weather_warning"
	// ReasonRampClosed: an entry, an exit or a junction's link closed
	// (amber).
	ReasonRampClosed Reason = "ramp_closed"
	// ReasonJam: a shorter jam, or one with no delay (amber).
	ReasonJam Reason = "jam"
	// ReasonSlippery: slippery road from the road weather (amber). The read
	// contract holds no road-weather forecast, so it is never given today.
	ReasonSlippery Reason = "slippery"
	// ReasonWarning: any other message of the publisher, including an
	// accident with no closure of the road (amber).
	ReasonWarning Reason = "warning"
	// ReasonNarrowing: roadworks or a lane restriction on the carriageway
	// (amber); works on a ramp give no colour.
	ReasonNarrowing Reason = "narrowing"
	// ReasonLimitedSight: a group the lamp reads is stale or missing (a
	// roads family stale or without data, a roads family the key may not
	// read, the DWD warnings unread or their source not fresh) (amber).
	ReasonLimitedSight Reason = "limited_sight"
)

// Reasons lists every reason: red ones in the platform's order, then amber
// ones.
var Reasons = []Reason{ReasonRoadClosed, ReasonSafety, ReasonBridgeClosed, ReasonLongJam, ReasonWeatherWarning,
	ReasonRampClosed, ReasonJam, ReasonSlippery, ReasonWarning, ReasonNarrowing, ReasonLimitedSight}

// reasonOfKey names a reason by its key; a jam is named by its colour.
var reasonOfKey = map[ReasonKey]Reason{
	ReasonKey(SignalClosure): ReasonRoadClosed, ReasonKey(SignalSafety): ReasonSafety,
	ReasonKey(SignalBridgeWind): ReasonBridgeClosed, ReasonKey(SignalDWD): ReasonWeatherWarning,
	KeyRampClosure: ReasonRampClosed, ReasonKey(SignalSlippery): ReasonSlippery,
	ReasonKey(SignalWarning): ReasonWarning, ReasonKey(SignalRoadworks): ReasonNarrowing, KeySight: ReasonLimitedSight,
}

// ReasonOf names one reason of the lamp.
func ReasonOf(r AmpelReason) Reason {
	if r.Signal != nil && r.Signal.Kind == SignalJam {
		if r.Lamp == LampRed {
			return ReasonLongJam
		}
		return ReasonJam
	}
	return reasonOfKey[ReasonKeyOf(r)]
}

// The sight groups of the service's lamp.
const (
	// GroupRoads is the sight of one roads family.
	GroupRoads = "roads"
	// GroupDWD is the sight of the DWD warnings.
	GroupDWD = "dwd"
)

// SourceSight is a family's sight from its freshness (feedSightOf): missing
// when it has published no data, stale when its data is older than its
// threshold, fresh otherwise.
func SourceSight(f provenance.Freshness) Sight {
	if f.DataAsOf == nil {
		return SightMissing
	}
	if f.Stale {
		return SightStale
	}
	return SightFresh
}

// LampOf is the lamp rule (FR-ROAD-004), the platform's motorway Ampel as
// check_autobahn_traffic judges it (toolAmpel): the signal of every
// situation in the question's scope (the road and direction, or the
// radius), whatever the list's filters keep, and of every DWD warning in
// force there (levels), ordered as the platform orders them, judged with
// the sight of every group read (ampelOf): red when a signal gives red;
// amber when a signal gives amber or a group's sight is limited; green
// otherwise, which is never a guarantee that the road is free. The reason
// names the first of the lamp's reasons; nil for green.
func LampOf(messages []Message, levels []int, sight []SightEntry) (Lamp, *Reason) {
	signals := make([]Signal, 0, len(messages)+len(levels))
	for _, m := range messages {
		signals = append(signals, m.Signal())
	}
	for _, l := range levels {
		signals = append(signals, Signal{Kind: SignalDWD, Level: l})
	}
	SortSignals(signals)
	a := AmpelOf(signals, sight)
	if a.Lamp == LampGreen {
		return LampGreen, nil
	}
	r := ReasonOf(a.Reasons[0])
	return a.Lamp, &r
}

// Families are the source families a conditions question reads: the roads
// families of its situations and the weather families of its DWD warnings.
type Families struct {
	Roads   []string
	Weather []string
}

// Warning is a DWD warning in force in a question's scope, as the lamp
// reads it.
type Warning struct {
	Family string
	Level  int
}

// Page is the answer of one conditions read, from one snapshot: a page of
// situations and what the lamp reads over the whole scope.
type Page struct {
	Items   []Situation
	HasMore bool
	// Messages is the lamp's view of every situation in the scope, whatever
	// the list's filters keep.
	Messages []Message
	// Warnings are the DWD warnings in force in the scope; nil when no
	// weather family was read.
	Warnings []Warning
	// Sources are the freshness of every family read.
	Sources []provenance.Family
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
