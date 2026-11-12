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

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
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
	LampGreen Lamp = Lamp(ampel.LampGreen)
	LampAmber Lamp = Lamp(ampel.LampAmber)
	LampRed   Lamp = Lamp(ampel.LampRed)
)

// Lamps lists every lamp an answer can carry.
var Lamps = []Lamp{LampGreen, LampAmber, LampRed}

// Record is how the lamp reads a situation's record.
type Record string

// The records.
const (
	// RecordOperatorEvent is the motorway operator's own event record, read
	// by its kind, its scope, its traffic type and its words.
	RecordOperatorEvent Record = "operator_event"
	// RecordTrafficMessage is any other traffic message, read by its kind
	// and its words.
	RecordTrafficMessage Record = "traffic_message"
	// RecordBridgeState is a bridge's state record, read by its state text
	// (the description) alone.
	RecordBridgeState Record = "bridge_state"
)

// MapRecord maps a stored record code; a code the service cannot map is a
// traffic message, the reading that needs no field of the others.
func MapRecord(raw string) Record {
	return mapCode(raw, []Record{RecordOperatorEvent, RecordBridgeState}, RecordTrafficMessage)
}

// Message is what the lamp reads of one situation in the question's scope.
type Message struct {
	Record   Record
	Kind     Kind
	Headline string
	// Subtitle is an operator event's subtitle; nil for any other record.
	Subtitle *string
	// Description is the publisher's description, its lines joined with one
	// newline; a bridge state's state text.
	Description *string
	// PlaceText is a traffic message's place words; nil for any other
	// record.
	PlaceText *string
	// Blocked is true when the publisher marks an operator event blocked.
	Blocked bool
	// TrafficType is an operator event's abnormal traffic type; nil when it
	// carries none.
	TrafficType *string
	// DelayS is the published delay in seconds; DelayMin the same in whole
	// minutes. Both nil when none is published.
	DelayS   *int
	DelayMin *int
}

// delay is the delay the lamp reads, in seconds: the published seconds,
// else the published minutes; nil when none is published.
func (m Message) delay() *float64 {
	var d float64
	switch {
	case m.DelayS != nil:
		d = float64(*m.DelayS)
	case m.DelayMin != nil:
		d = float64(*m.DelayMin) * 60
	default:
		return nil
	}
	return &d
}

// lines splits a description into its lines; nil for none.
func lines(s *string) []*string {
	if s == nil {
		return nil
	}
	parts := strings.Split(*s, "\n")
	out := make([]*string, len(parts))
	for i := range parts {
		out[i] = &parts[i]
	}
	return out
}

// Signal is the platform's signal of the situation, read as the platform
// reads its record: an operator event as the operator's row (its kind, its
// location line or subtitle, its traffic type, its words), a bridge state by
// its state text, any other message as a traffic message (its kind, its
// headline, place words and description, its delay). False: the record
// gives no signal (a bridge state that states nothing).
func (m Message) Signal() (ampel.Signal, bool) {
	switch m.Record {
	case RecordOperatorEvent:
		return ampel.OperatorEventSignal(ampel.OperatorEvent{
			Kind: string(m.Kind), TrafficType: m.TrafficType, DelayS: m.delay(),
			Title: &m.Headline, Subtitle: m.Subtitle, Description: lines(m.Description), Blocked: m.Blocked,
		}), true
	case RecordBridgeState:
		return ampel.BridgeSignal(ampel.BridgeStateOf(m.Description))
	}
	return ampel.MessageSignal(ampel.Message{
		Kind: string(m.Kind), What: m.Headline, PlaceText: m.PlaceText, DetailText: m.Description, DelayS: m.delay(),
	}), true
}

// Reason says why the lamp has its colour: the key of the lamp's first
// reason (reasonKeyOf), a jam split by its colour. Open enumeration.
type Reason string

// The reasons.
const (
	// ReasonRoadClosed: a closure of the carriageway (red).
	ReasonRoadClosed Reason = "road_closed"
	// ReasonRampClosed: an entry, an exit or a junction's link closed
	// (amber).
	ReasonRampClosed Reason = "ramp_closed"
	// ReasonSafety: a safety message by its words, on any kind: a wrong-way
	// driver, an object, a person or an animal on the road, an accident
	// with a closure of the road (red).
	ReasonSafety Reason = "safety"
	// ReasonBridgeClosed: a bridge closed by its state record (red). A
	// bridge that is only restricted (a wind warning, a limit) is a warning.
	ReasonBridgeClosed Reason = "bridge_closed"
	// ReasonLongJam: a jam with a delay of 30 minutes or more (red).
	ReasonLongJam Reason = "long_jam"
	// ReasonJam: a shorter jam, or one with no delay (amber).
	ReasonJam Reason = "jam"
	// ReasonWeatherWarning: an official weather warning in force on the road
	// or within the radius: red from level 3, amber at levels 1 and 2.
	ReasonWeatherWarning Reason = "weather_warning"
	// ReasonSlippery: slippery road from the road weather (amber). The read
	// contract holds no road-weather forecast, so it is never given today.
	ReasonSlippery Reason = "slippery"
	// ReasonWarning: any other message of the publisher, including an
	// accident with no closure of the road and a restricted bridge (amber).
	ReasonWarning Reason = "warning"
	// ReasonNarrowing: roadworks or a lane restriction on the carriageway
	// (amber); works on a ramp give no colour.
	ReasonNarrowing Reason = "narrowing"
	// ReasonLimitedSight: a group the lamp reads is stale or missing (amber):
	// a roads family stale or without data, a roads family the key may not
	// read, the weather warnings unread or their source not current, or a
	// state in scope whose traffic messages no current source covers.
	ReasonLimitedSight Reason = "limited_sight"
)

// Reasons lists every reason in the platform's order of the signals
// (closures, safety, bridges, jams, weather warnings, slippery road, other
// warnings, roadworks), the red one of a kind before its amber one, and the
// limited sight last. The lamp's reason is its first reason: a red one
// before any amber one, in this order inside each colour.
var Reasons = []Reason{ReasonRoadClosed, ReasonRampClosed, ReasonSafety, ReasonBridgeClosed, ReasonLongJam, ReasonJam,
	ReasonWeatherWarning, ReasonSlippery, ReasonWarning, ReasonNarrowing, ReasonLimitedSight}

// reasonOfKey names a reason by its key; a jam is named by its colour.
var reasonOfKey = map[ampel.ReasonKey]Reason{
	ampel.ReasonKey(ampel.SignalClosure): ReasonRoadClosed, ampel.KeyRampClosure: ReasonRampClosed,
	ampel.ReasonKey(ampel.SignalSafety): ReasonSafety, ampel.ReasonKey(ampel.SignalBridgeWind): ReasonBridgeClosed,
	ampel.ReasonKey(ampel.SignalWeather): ReasonWeatherWarning, ampel.ReasonKey(ampel.SignalSlippery): ReasonSlippery,
	ampel.ReasonKey(ampel.SignalWarning): ReasonWarning, ampel.ReasonKey(ampel.SignalRoadworks): ReasonNarrowing,
	ampel.KeySight: ReasonLimitedSight,
}

// ReasonOf names one reason of the lamp.
func ReasonOf(r ampel.Reason) Reason {
	if r.Signal != nil && r.Signal.Kind == ampel.SignalJam {
		if r.Lamp == ampel.LampRed {
			return ReasonLongJam
		}
		return ReasonJam
	}
	return reasonOfKey[ampel.ReasonKeyOf(r)]
}

// GroupRoads is the sight of one roads family: the service judges the
// situations it answers by the freshness of the family that publishes them.
const GroupRoads = "roads"

// SourceSight is a family's sight from its freshness: missing when it has
// published no data, stale when its data is older than its threshold, fresh
// otherwise.
func SourceSight(f provenance.Freshness) ampel.Sight {
	if f.DataAsOf == nil {
		return ampel.SightMissing
	}
	if f.Stale {
		return ampel.SightStale
	}
	return ampel.SightFresh
}

// LampOf is the lamp rule (FR-ROAD-004), the platform's motorway Ampel as
// its tool judges it (toolAmpel): the signal of every situation in the
// question's scope (the road and direction, or the radius), whatever the
// list's filters keep, and of every weather warning in force there
// (levels), ordered as the platform orders them, judged with the sight of
// every group read (ampelOf): red when a signal gives red; amber when a
// signal gives amber or a group's sight is limited; green otherwise, which
// is never a guarantee that the road is free. The reason names the first of
// the lamp's reasons; nil for green.
func LampOf(messages []Message, levels []int, sight []ampel.SightEntry) (Lamp, *Reason) {
	signals := make([]ampel.Signal, 0, len(messages)+len(levels))
	for _, m := range messages {
		if s, ok := m.Signal(); ok {
			signals = append(signals, s)
		}
	}
	for _, l := range levels {
		signals = append(signals, ampel.Signal{Kind: ampel.SignalWeather, Level: l})
	}
	ampel.SortSignals(signals)
	a := ampel.Of(signals, sight)
	if a.Lamp == ampel.LampGreen {
		return LampGreen, nil
	}
	r := ReasonOf(a.Reasons[0])
	return Lamp(a.Lamp), &r
}

// Families are the source families a conditions question reads: the roads
// families of its situations and the weather families of its warnings.
type Families struct {
	Roads   []string
	Weather []string
}

// Warning is an official weather warning in force in a question's scope, as
// the lamp reads it.
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
	// Warnings are the weather warnings in force in the scope; nil when no
	// weather family was read.
	Warnings []Warning
	// Sources are the freshness of every family read.
	Sources []provenance.Family
	// States are the codes of the states in scope, each once: the states of
	// the road's junctions, or of the junctions within the radius, else of
	// the junction nearest the position.
	States []string
	// Coverage is, by state in scope, the roads families that cover its
	// traffic messages; only the families read are named.
	Coverage map[string]ampel.Coverage
	// Confirmations are the newest confirmation of every family read that
	// has a freshness row, by family (its instant nil when it was never
	// confirmed); a family without one is absent.
	Confirmations map[string]ampel.Confirmation
	// NextChange is the earliest instant after the read at which a
	// situation or a warning of the scope starts or ends (its valid_from or
	// valid_to), whatever the list's filters keep; nil when none does.
	NextChange *time.Time
}

// ChangesAt is the earliest instant after now at which the answer read in p
// changes with the clock alone: a situation or a warning of the scope
// starting or ending (NextChange), a family read turning stale (the first
// whole second past its threshold, as Assess counts it), or a family's
// confirmation lapsing (ampel.LapsesAt). Nil when nothing in it changes
// with the clock; until then, only a new version of a family read, of its
// confirmation or of the lamp's reference rows changes it.
func (p Page) ChangesAt(now time.Time) *time.Time {
	var out *time.Time
	consider := func(t *time.Time) {
		if t != nil && t.After(now) && (out == nil || t.Before(*out)) {
			out = t
		}
	}
	consider(p.NextChange)
	for _, f := range p.Sources {
		if f.DataAsOf != nil {
			t := f.DataAsOf.Add((f.StaleAfter/time.Second + 1) * time.Second)
			consider(&t)
		}
	}
	for _, c := range p.Confirmations {
		consider(ampel.LapsesAt(&c))
	}
	return out
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
