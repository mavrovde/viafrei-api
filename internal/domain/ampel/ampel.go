// Package ampel is the platform's motorway Ampel: the lamp over what is held
// about a road now, its signals, its sight and its text rules. It is pure (no
// storage, no clock but the instant it is given, no language) and is read by
// every domain that shows a lamp, so two answers cannot give one road two
// colours.
package ampel

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"
)

// This file is a port of the platform's motorway Ampel module, line for
// line (signalLamp, ampelOf, reasonKeyOf, safetyWordOf, closureScopeOf,
// messageScopeOf, worksSignal, the signal of a motorway operator's event,
// the weather warnings' sight, messageSignal, bridgeStateOf, bridgeSignal,
// the sight of a source from its newest confirmation, and the traffic
// messages' sight per state), the platform's one alive-or-paused rule
// (checkAlive), and the order of the reasons inside a colour of the
// platform's tool (ORDER in toolAmpel). The regular expressions are copied
// verbatim. Three things are renamed and nothing else: the platform's
// yellow is the service's amber, the groups and signals the platform names
// after a publisher take the service's domain words (the operator's events,
// the weather warnings, the traffic messages), and a source's newest check
// is its newest confirmation.

// Lamp is the colour: the platform's motorway Ampel, whose yellow is amber
// here.
type Lamp string

// The lamps.
const (
	LampGreen Lamp = "green"
	LampAmber Lamp = "amber"
	LampRed   Lamp = "red"
)

// Lamps lists every lamp.
var Lamps = []Lamp{LampGreen, LampAmber, LampRed}

// The thresholds of the platform's table.
const (
	// JamRedS is the delay, in seconds, from which a jam is red; a shorter
	// one, or one with no stated delay, is amber.
	JamRedS = 30 * 60
	// WarningRedLevel is the weather warning level from which a warning is
	// red (levels 3 and 4).
	WarningRedLevel = 3
	// WarningAmberLevel is the weather warning level from which a warning
	// below red is amber (levels 1 and 2). Level 0 is a pre-information: no
	// colour.
	WarningAmberLevel = 1
	// OnRoadM is the distance, in metres, within which a position is on a
	// motorway. The service's lamps do not use it (their scope is the road,
	// or the radius asked); it is kept so every threshold is the platform's.
	OnRoadM = 2000
)

// SafetyWord is a safety message by its words.
type SafetyWord string

// The safety words; WordNone: an ordinary message.
const (
	WordNone            SafetyWord = ""
	WordWrongWay        SafetyWord = "wrongWay"
	WordObject          SafetyWord = "object"
	WordPerson          SafetyWord = "person"
	WordAnimal          SafetyWord = "animal"
	WordAccidentClosure SafetyWord = "accidentClosure"
)

// ClosureScope is a closure's scope: the carriageway, or a ramp (an entry,
// an exit, an interchange's link).
type ClosureScope string

// The scopes.
const (
	ScopeCarriageway ClosureScope = "carriageway"
	ScopeRamp        ClosureScope = "ramp"
)

// SignalKind is what one signal is.
type SignalKind string

// The signal kinds.
const (
	SignalClosure    SignalKind = "closure"
	SignalSafety     SignalKind = "safety"
	SignalBridgeWind SignalKind = "bridgeWind"
	SignalJam        SignalKind = "jam"
	// SignalWeather is an official weather warning in force.
	SignalWeather  SignalKind = "weather"
	SignalSlippery SignalKind = "slippery"
	// SignalWarning is any other message: an ordinary warning.
	SignalWarning   SignalKind = "warning"
	SignalRoadworks SignalKind = "roadworks"
)

// Signal is one thing found inside the question's scope.
type Signal struct {
	Kind SignalKind
	// Scope is the scope of a closure or roadworks.
	Scope ClosureScope
	// Word is the word of a safety message.
	Word SafetyWord
	// DelayS is a jam's delay in seconds; nil when none is stated.
	DelayS *float64
	// Level is a weather warning's level.
	Level int
}

// Sight is how fresh a safety-relevant group is.
type Sight string

// The sights.
const (
	SightFresh   Sight = "fresh"
	SightStale   Sight = "stale"
	SightMissing Sight = "missing"
)

// The groups whose sight green depends on.
const (
	// GroupOperator is the motorway operator's events.
	GroupOperator = "operator"
	// GroupWeather is the official weather warnings.
	GroupWeather = "weather"
	// GroupMessages is the traffic messages, one entry per state.
	GroupMessages = "messages"
)

// SightEntry is one group's sight; Land is the state of a group judged per
// state, empty for any other.
type SightEntry struct {
	Group string
	State Sight
	Land  string
}

// Reason is why a lamp: a signal and the colour it gives on its own, or a
// group whose sight is limited (always amber). Exactly one of Signal and
// Sight is set.
type Reason struct {
	Lamp   Lamp
	Signal *Signal
	Sight  *SightEntry
}

// Ampel is a lamp and every reason, red before amber, in the order of the
// signals inside each colour; empty exactly when green.
type Ampel struct {
	Lamp    Lamp
	Reasons []Reason
}

// SignalLamp is the colour one signal gives on its own (signalLamp); false:
// it gives none (a ramp's roadworks, a level-0 weather pre-information).
func SignalLamp(s Signal) (Lamp, bool) {
	switch s.Kind {
	case SignalClosure:
		if s.Scope == ScopeCarriageway {
			return LampRed, true
		}
		return LampAmber, true
	case SignalSafety, SignalBridgeWind:
		return LampRed, true
	case SignalJam:
		if s.DelayS != nil && *s.DelayS >= JamRedS {
			return LampRed, true
		}
		return LampAmber, true
	case SignalWeather:
		return WarningLamp(s.Level)
	case SignalRoadworks:
		// Works on a ramp narrow no carriageway; a closed ramp is a closure.
		if s.Scope == ScopeCarriageway {
			return LampAmber, true
		}
		return "", false
	case SignalWarning, SignalSlippery:
		return LampAmber, true
	}
	return "", false
}

// WarningLamp is the colour a weather warning of a level gives on its own
// (the weather row of signalLamp): red from WarningRedLevel, amber from
// WarningAmberLevel, none below (a level-0 pre-information, or any lower
// value).
func WarningLamp(level int) (Lamp, bool) {
	if level >= WarningRedLevel {
		return LampRed, true
	}
	if level >= WarningAmberLevel {
		return LampAmber, true
	}
	return "", false
}

// Of is the lamp (ampelOf): the first match from the top of the platform's
// table. sight lists the safety-relevant groups read and how fresh each is;
// a group that is not fresh is an amber reason of its own, so green needs
// every one fresh. An empty sight cannot be green: a lamp that read nothing
// has seen nothing.
func Of(signals []Signal, sight []SightEntry) Ampel {
	var red, amber []Reason
	for i := range signals {
		s := signals[i]
		switch l, _ := SignalLamp(s); l {
		case LampRed:
			red = append(red, Reason{Lamp: LampRed, Signal: &s})
		case LampAmber:
			amber = append(amber, Reason{Lamp: LampAmber, Signal: &s})
		}
	}
	for _, g := range sight {
		if g.State != SightFresh {
			amber = append(amber, Reason{Lamp: LampAmber, Sight: &SightEntry{Group: g.Group, State: g.State, Land: g.Land}})
		}
	}
	if len(sight) == 0 && len(red) == 0 && len(amber) == 0 {
		amber = append(amber, Reason{Lamp: LampAmber, Sight: &SightEntry{Group: GroupOperator, State: SightMissing}})
	}
	reasons := append(red, amber...)
	lamp := LampGreen
	if len(red) > 0 {
		lamp = LampRed
	} else if len(amber) > 0 {
		lamp = LampAmber
	}
	return Ampel{Lamp: lamp, Reasons: reasons}
}

// signalOrder is the order of the reasons inside a colour (ORDER of
// toolAmpel): closures first, then safety, jams, weather warnings, the rest.
var signalOrder = map[SignalKind]int{
	SignalClosure: 0, SignalSafety: 1, SignalBridgeWind: 2, SignalJam: 3,
	SignalWeather: 4, SignalSlippery: 5, SignalWarning: 6, SignalRoadworks: 7,
}

// SortSignals orders the signals as toolAmpel does before ampelOf, stably:
// by kind (signalOrder), jams by delay descending (none last), weather
// warnings by level descending.
func SortSignals(signals []Signal) {
	slices.SortStableFunc(signals, func(a, b Signal) int {
		if d := signalOrder[a.Kind] - signalOrder[b.Kind]; d != 0 {
			return d
		}
		if a.Kind == SignalJam && b.Kind == SignalJam {
			return cmp.Compare(delayOrNone(b.DelayS), delayOrNone(a.DelayS))
		}
		if a.Kind == SignalWeather && b.Kind == SignalWeather {
			return b.Level - a.Level
		}
		return 0
	})
}

// delayOrNone is a jam's delay as the order reads it: none is -1.
func delayOrNone(d *float64) float64 {
	if d == nil {
		return -1
	}
	return *d
}

// ReasonKey is what a reason is, for counting (reasonKeyOf): its signal's
// kind, a closure split into the carriageway's and a ramp's, or sight.
type ReasonKey string

// The reason keys beyond the signal kinds.
const (
	KeyRampClosure ReasonKey = "rampClosure"
	KeySight       ReasonKey = "sight"
)

// ReasonKeyOf is a reason's key (reasonKeyOf).
func ReasonKeyOf(r Reason) ReasonKey {
	if r.Sight != nil {
		return KeySight
	}
	if r.Signal.Kind == SignalClosure && r.Signal.Scope == ScopeRamp {
		return KeyRampClosure
	}
	return ReasonKey(r.Signal.Kind)
}

// ---------------------------------------------------------------------------
// The text rules over the publisher's words
// ---------------------------------------------------------------------------

// jsSpace is the set of characters JavaScript's \s and String.prototype.trim
// match: the folded text is the platform's, character for character.
const jsSpace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

var jsSpaces = regexp.MustCompile(`[` + regexp.QuoteMeta(jsSpace) + `]+`)

var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")

// fold lower-cases the text, writes the umlauts out and collapses every run
// of white space to one space. The one rune whose JavaScript lower case is
// two runes (U+0130) is written as JavaScript writes it. A rune outside the
// Basic Multilingual Plane is two UTF-16 code units in JavaScript, and the
// platform's patterns (no u flag) count code units: each such rune becomes
// two U+FFFD, which no pattern names and which are neither "|" nor ".", so
// every bounded repetition counts the text as JavaScript counts it.
func fold(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
	return codeUnits(jsSpaces.ReplaceAllString(umlauts.Replace(s), " "))
}

// codeUnits writes every rune above U+FFFF as two U+FFFD runes.
func codeUnits(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r > 0xFFFF }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r > 0xFFFF {
			b.WriteString("\ufffd\ufffd")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// trim removes leading and trailing white space as JavaScript's trim does.
func trim(s string) string { return strings.Trim(s, jsSpace) }

// joinTexts joins the texts that are present with " | ", as the platform
// joins its filtered string list.
func joinTexts(texts []*string) string {
	var present []string
	for _, t := range texts {
		if t != nil {
			present = append(present, *t)
		}
	}
	return strings.Join(present, " | ")
}

// closed are the words of a closed ROAD (never of one lane).
const closed = "(vollsperrung|voll gesperrt|vollgesperrt|fahrbahn (ist )?(voll )?(gesperrt|blockiert)|alle fahrstreifen (sind )?(gesperrt|blockiert)|autobahn (ist )?gesperrt|richtungsfahrbahn (ist )?gesperrt)"

// SafetyPatterns are the safety words, by keyword over the folded text
// (umlauts written out) (SAFETY_PATTERNS).
var SafetyPatterns = map[SafetyWord]*regexp.Regexp{
	WordWrongWay: regexp.MustCompile(`falschfahrer|geisterfahrer|falsch fahrend|entgegenkommendes fahrzeug|in falscher fahrtrichtung`),
	WordPerson:   regexp.MustCompile(`(personen?|fussgaenger(in)?|menschen) auf (der|die) (fahrbahn|autobahn)`),
	WordAnimal:   regexp.MustCompile(`tiere? auf (der|die) (fahrbahn|autobahn)`),
	WordObject:   regexp.MustCompile(`(gegenstaende?|gegenstand|hindernis(se)?|ladung|ladungsteile|fahrzeugteile) auf (der|die) (fahrbahn|autobahn)|verlorene ladung|ladung verloren`),
	// An accident WITH a closure of the road; a closed lane is a
	// restriction, amber.
	WordAccidentClosure: regexp.MustCompile(`unfall.*` + closed + `|` + closed + `.*unfall`),
}

// safetyOrder is the order the words are tested in: the most dangerous
// first.
var safetyOrder = []SafetyWord{WordWrongWay, WordPerson, WordAnimal, WordObject, WordAccidentClosure}

// SafetyWordOf is the safety word of a message from its texts (nil: absent);
// WordNone: an ordinary message.
func SafetyWordOf(texts []*string) SafetyWord {
	t := fold(joinTexts(texts))
	for _, w := range safetyOrder {
		if SafetyPatterns[w].MatchString(t) {
			return w
		}
	}
	return WordNone
}

// rampLine is a location line naming a ramp: "Auffahrt auf die A9: …",
// "Abfahrt von der A92: …", "Von Auffahrt auf …".
var rampLine = regexp.MustCompile(`^(von )?(auffahrt|abfahrt|ausfahrt|zufahrt|anschlussstelle gesperrt)\b`)

// carriagewayLine is a location line naming the carriageway: "A45: Hagen ->
// Gießen, zwischen …", "Von A9: …".
var carriagewayLine = regexp.MustCompile(`^(von )?a ?\d{1,3}[a-z]?: `)

var ausRichtung = regexp.MustCompile(`\(aus richtung `)

// ClosureScopeOf is whether an operator's closure (or roadworks) is the
// carriageway's or a ramp's (closureScopeOf): the publisher's location line
// decides (the first description line, nil skipped, that names either),
// else a subtitle "AS X (aus Richtung Y)", the shape the operator gives a
// ramp, else the carriageway, the conservative reading.
func ClosureScopeOf(description []*string, subtitle *string) ClosureScope {
	for _, raw := range description {
		if raw == nil {
			continue
		}
		l := fold(trim(*raw))
		if rampLine.MatchString(l) {
			return ScopeRamp
		}
		if carriagewayLine.MatchString(l) {
			return ScopeCarriageway
		}
	}
	if subtitle != nil && ausRichtung.MatchString(fold(*subtitle)) {
		return ScopeRamp
	}
	return ScopeCarriageway
}

var (
	closedRe     = regexp.MustCompile(closed)
	rampClosedRe = regexp.MustCompile(`(auffahrt|abfahrt|ausfahrt|zufahrt|anschlussstelle|verbindungsrampe|rampe)[^|.]{0,80}gesperrt|gesperrt[^|.]{0,40}(auffahrt|abfahrt|ausfahrt|zufahrt|rampe)`)
)

// MessageScopeOf is a traffic message's scope when it closes something
// (messageScopeOf): a ramp when its words close an entry, an exit or a
// junction's link and never the road itself, else the carriageway.
func MessageScopeOf(texts []*string) ClosureScope {
	t := fold(joinTexts(texts))
	if closedRe.MatchString(t) {
		return ScopeCarriageway
	}
	if rampClosedRe.MatchString(t) {
		return ScopeRamp
	}
	return ScopeCarriageway
}

// WorksSignal is a closure's or a roadworks row's signal (worksSignal): a
// closure, or a row marked blocked, is a closure by its scope; other
// roadworks are roadworks by theirs.
func WorksSignal(kind SignalKind, blocked bool, scope ClosureScope) Signal {
	if kind == SignalClosure || blocked {
		return Signal{Kind: SignalClosure, Scope: scope}
	}
	return Signal{Kind: SignalRoadworks, Scope: scope}
}

// OperatorEvent is what a motorway operator's event is judged by.
type OperatorEvent struct {
	// Kind is closure, roadworks or any other kind.
	Kind string
	// TrafficType is the event's abnormal traffic type; nil when it carries
	// none.
	TrafficType *string
	// DelayS is the event's delay in seconds; nil when none.
	DelayS      *float64
	Title       *string
	Subtitle    *string
	Description []*string
	// Blocked is true when the publisher marks the event blocked.
	Blocked bool
}

// OperatorEventSignal is the signal of one operator's event, the platform's
// rule for its motorway operator's row: a closure or roadworks by its scope
// (ClosureScopeOf, WorksSignal; marked blocked, a closure); an event with a
// traffic type a jam by its delay; any other a safety message by its words,
// else an ordinary warning.
func OperatorEventSignal(r OperatorEvent) Signal {
	if r.Kind == string(SignalClosure) || r.Kind == string(SignalRoadworks) {
		return WorksSignal(SignalKind(r.Kind), r.Blocked, ClosureScopeOf(r.Description, r.Subtitle))
	}
	if r.TrafficType != nil {
		return Signal{Kind: SignalJam, DelayS: r.DelayS}
	}
	if w := SafetyWordOf(append([]*string{r.Title, r.Subtitle}, r.Description...)); w != WordNone {
		return Signal{Kind: SignalSafety, Word: w}
	}
	return Signal{Kind: SignalWarning}
}

// Message is what a traffic message is judged by.
type Message struct {
	// Kind is closure, accident, jam, roadworks, restriction, warning or
	// other.
	Kind       string
	What       string
	PlaceText  *string
	DetailText *string
	// DelayS is nil when no delay is stated.
	DelayS *float64
}

// MessageSignal is the signal of one traffic message (messageSignal): a
// safety message only by its words; a closure by its scope; a jam by its
// delay; works and lane restrictions as roadworks by their scope; anything
// else an ordinary warning.
func MessageSignal(m Message) Signal {
	texts := []*string{&m.What, m.PlaceText, m.DetailText}
	if w := SafetyWordOf(texts); w != WordNone {
		return Signal{Kind: SignalSafety, Word: w}
	}
	switch m.Kind {
	case "closure":
		return Signal{Kind: SignalClosure, Scope: MessageScopeOf(texts)}
	case "jam":
		return Signal{Kind: SignalJam, DelayS: m.DelayS}
	case "roadworks", "restriction":
		return Signal{Kind: SignalRoadworks, Scope: MessageScopeOf(texts)}
	}
	return Signal{Kind: SignalWarning}
}

// BridgeState is a bridge's state by its words: closed, restricted (a wind
// warning with a limit), or BridgeNone.
type BridgeState string

// The bridge states.
const (
	BridgeNone       BridgeState = ""
	BridgeClosed     BridgeState = "closed"
	BridgeRestricted BridgeState = "restricted"
)

var (
	undefinedState = regexp.MustCompile(`^undefiniert\b`)
	bridgeClosing  = regexp.MustCompile(`(gesperrt|sperrung)`)
	bridgeLifted   = regexp.MustCompile(`(nicht gesperrt|aufgehoben|freigegeben|wieder (frei|befahrbar|offen)|keine sperrung)`)
	bridgeLimit    = regexp.MustCompile(`(windwarnung|km\/h|beschraenk|verbot|nur fuer|gewicht|\d ?t\b)`)
)

// BridgeStateOf is the state of a bridge record from its state text and
// nothing else (bridgeStateOf; nil: no text): "undefiniert" is no state;
// closed only when the words close it and do not lift a closure; a wind
// warning, a speed, weight or vehicle limit is a restriction.
func BridgeStateOf(text *string) BridgeState {
	if text == nil {
		return BridgeNone
	}
	t := fold(trim(*text))
	if t == "" || undefinedState.MatchString(t) {
		return BridgeNone
	}
	if bridgeClosing.MatchString(t) && !bridgeLifted.MatchString(t) {
		return BridgeClosed
	}
	if bridgeLimit.MatchString(t) {
		return BridgeRestricted
	}
	return BridgeNone
}

// BridgeSignal is the signal of a bridge in force (bridgeSignal): red only
// when closed; a restriction is a warning (amber); false: no state, no
// signal.
func BridgeSignal(state BridgeState) (Signal, bool) {
	switch state {
	case BridgeClosed:
		return Signal{Kind: SignalBridgeWind}, true
	case BridgeRestricted:
		return Signal{Kind: SignalWarning}, true
	}
	return Signal{}, false
}

// ---------------------------------------------------------------------------
// The sight
// ---------------------------------------------------------------------------

// Confirmation is a source's newest confirmation that it is current: At nil
// when it was never confirmed, IntervalS nil when the source states no
// interval, Paused when its updates are paused and the data held stands.
type Confirmation struct {
	At        *time.Time
	IntervalS *float64
	Paused    bool
}

// Alive is the platform's one alive-or-paused rule (checkAlive): true when
// the confirmation is inside intervals of its interval before now
// (inclusive), or the updates are paused and the source was confirmed. A
// confirmation without an interval is judged against defaultIntervalS; with
// none (nil), it is never inside.
func Alive(c *Confirmation, now time.Time, intervals float64, defaultIntervalS *float64) bool {
	if c == nil || c.At == nil {
		return false
	}
	if c.Paused {
		return true
	}
	interval := c.IntervalS
	if interval == nil {
		interval = defaultIntervalS
	}
	if interval == nil {
		return false
	}
	return float64(now.Sub(*c.At).Milliseconds()) <= intervals**interval*1000
}

// defaultIntervalS is the interval a confirmation without one is judged
// against (600 s, the platform's default).
var defaultIntervalS = 600.0

// ConfirmedSight is a source's sight from its newest confirmation: fresh
// inside three intervals (600 s when it states none) or while its updates
// are paused, stale after, missing when it was never confirmed.
func ConfirmedSight(c *Confirmation, now time.Time) Sight {
	if c == nil || c.At == nil {
		return SightMissing
	}
	if Alive(c, now, 3, &defaultIntervalS) {
		return SightFresh
	}
	return SightStale
}

// WarningSightOf is the weather warnings' sight: missing when the warnings
// read did not answer; fresh when it holds a warning, or when the warnings'
// source is fresh by its confirmation (a quiet read of a live source);
// missing otherwise: a quiet read of a source that is not current is no
// sight.
func WarningSightOf(readOK bool, warnings int, c *Confirmation, now time.Time) Sight {
	if !readOK {
		return SightMissing
	}
	if warnings > 0 || ConfirmedSight(c, now) == SightFresh {
		return SightFresh
	}
	return SightMissing
}

// Coverage are the sources that tell the lamp one state's traffic messages
// are seen: the state's own message sources (Own), the sources measured to
// carry the state's messages as well (Carried), and the own sources whose
// messages are answered only while current (CurrentOnly).
type Coverage struct {
	Own         []string
	Carried     []string
	CurrentOnly []string
}

var sightRank = map[Sight]int{SightFresh: 0, SightStale: 1, SightMissing: 2}

// MessageSightOf is the traffic messages' sight of each state in scope: the
// best of the state's own sources and those carrying it; a state with
// neither is missing; an own source answered only while current caps the
// state's sight. In the states' order, each once.
func MessageSightOf(lands []string, coverage map[string]Coverage, sightOf func(source string) Sight) []SightEntry {
	var out []SightEntry
	seen := map[string]bool{}
	for _, land := range lands {
		if seen[land] {
			continue
		}
		seen[land] = true
		c := coverage[land]
		best := SightMissing
		for _, k := range append(slices.Clone(c.Own), c.Carried...) {
			if s := sightOf(k); sightRank[s] < sightRank[best] {
				best = s
			}
		}
		for _, k := range c.Own {
			if !slices.Contains(c.CurrentOnly, k) {
				continue
			}
			if s := sightOf(k); sightRank[s] > sightRank[best] {
				best = s
			}
		}
		out = append(out, SightEntry{Group: GroupMessages, State: best, Land: land})
	}
	return out
}

// MessageSight is the traffic messages' sight of the states in scope as the
// platform's tool reads it: one missing entry when no state is in scope (a
// lamp never turns green on messages nobody could place), else
// MessageSightOf with each source's sight from its newest confirmation
// (ConfirmedSight; a source with none is missing).
func MessageSight(lands []string, coverage map[string]Coverage, confirmations map[string]Confirmation, now time.Time) []SightEntry {
	if len(lands) == 0 {
		return []SightEntry{{Group: GroupMessages, State: SightMissing}}
	}
	return MessageSightOf(lands, coverage, func(source string) Sight {
		c, ok := confirmations[source]
		if !ok {
			return SightMissing
		}
		return ConfirmedSight(&c, now)
	})
}
