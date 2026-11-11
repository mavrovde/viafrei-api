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

// This file is a port of the platform's motorway Ampel, line for line:
// motorway-ampel.ts of its analysis module (signalLamp, ampelOf,
// reasonKeyOf, safetyWordOf, closureScopeOf, messageScopeOf, worksSignal,
// autobahnEventSignal, dwdSightOf, messageSignal, bridgeStateOf,
// bridgeSignal, feedSightOf, policeSightOf), checkAlive of its feed-alive.ts,
// and the order of the reasons inside a colour of the MCP server's
// autobahn-ampel.ts (ORDER in toolAmpel). The regular expressions are copied
// verbatim. The platform's yellow is the service's amber; nothing else is
// renamed.

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

// The thresholds (AMPEL_JAM_RED_S, AMPEL_DWD_RED_LEVEL,
// AMPEL_DWD_YELLOW_LEVEL, AMPEL_ON_ROAD_M).
const (
	// JamRedS is the delay, in seconds, from which a jam is red; a shorter
	// one, or one with no stated delay, is amber.
	JamRedS = 30 * 60
	// DWDRedLevel is the DWD warning level from which a warning is red
	// (levels 3 and 4).
	DWDRedLevel = 3
	// DWDAmberLevel is the DWD warning level from which a warning below red
	// is amber (levels 1 and 2). Level 0 is a pre-information: no colour.
	DWDAmberLevel = 1
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

// SignalKind is what one signal is (AmpelSignal's kind).
type SignalKind string

// The signal kinds.
const (
	SignalClosure    SignalKind = "closure"
	SignalSafety     SignalKind = "safety"
	SignalBridgeWind SignalKind = "bridgeWind"
	SignalJam        SignalKind = "jam"
	SignalDWD        SignalKind = "dwd"
	SignalSlippery   SignalKind = "slippery"
	SignalWarning    SignalKind = "warning"
	SignalRoadworks  SignalKind = "roadworks"
)

// Signal is one thing found inside the question's scope (AmpelSignal).
type Signal struct {
	Kind SignalKind
	// Scope is the scope of a closure or roadworks.
	Scope ClosureScope
	// Word is the word of a safety message.
	Word SafetyWord
	// DelayS is a jam's delay in seconds; nil when none is stated.
	DelayS *float64
	// Level is a DWD warning's level.
	Level int
}

// Sight is how fresh a safety-relevant group is (AmpelSight).
type Sight string

// The sights.
const (
	SightFresh   Sight = "fresh"
	SightStale   Sight = "stale"
	SightMissing Sight = "missing"
)

// The groups whose sight green depends on (AmpelGroup).
const (
	// GroupAutobahn is the Autobahn GmbH's events.
	GroupAutobahn = "autobahn"
	// GroupDWD is the DWD warnings.
	GroupDWD = "dwd"
	// GroupPolice is the police and Land messages, one entry per state.
	GroupPolice = "police"
)

// SightEntry is one group's sight (AmpelSightEntry); Land is the state of a
// group judged per state, empty for any other.
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

// SignalLamp is the colour one signal gives on its own; false: it gives none
// (a ramp's roadworks, a DWD level 0).
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
	case SignalDWD:
		return DWDLamp(s.Level)
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

// DWDLamp is the colour a DWD warning of a level gives on its own (the dwd
// row of signalLamp): red from DWDRedLevel, amber from DWDAmberLevel, none
// below (a level-0 pre-information, or any lower value).
func DWDLamp(level int) (Lamp, bool) {
	if level >= DWDRedLevel {
		return LampRed, true
	}
	if level >= DWDAmberLevel {
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
		amber = append(amber, Reason{Lamp: LampAmber, Sight: &SightEntry{Group: GroupAutobahn, State: SightMissing}})
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
// autobahn-ampel.ts): closures first, then safety, jams, DWD, the rest.
var signalOrder = map[SignalKind]int{
	SignalClosure: 0, SignalSafety: 1, SignalBridgeWind: 2, SignalJam: 3,
	SignalDWD: 4, SignalSlippery: 5, SignalWarning: 6, SignalRoadworks: 7,
}

// SortSignals orders the signals as toolAmpel does before ampelOf, stably:
// by kind (signalOrder), jams by delay descending (none last), DWD warnings
// by level descending.
func SortSignals(signals []Signal) {
	slices.SortStableFunc(signals, func(a, b Signal) int {
		if d := signalOrder[a.Kind] - signalOrder[b.Kind]; d != 0 {
			return d
		}
		if a.Kind == SignalJam && b.Kind == SignalJam {
			return cmp.Compare(delayOrNone(b.DelayS), delayOrNone(a.DelayS))
		}
		if a.Kind == SignalDWD && b.Kind == SignalDWD {
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

// ReasonKey is what a reason is, for counting (AmpelReasonKey): its signal's
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
const jsSpace = "\t\n\v\f\r                  　﻿"

var jsSpaces = regexp.MustCompile(`[` + regexp.QuoteMeta(jsSpace) + `]+`)

var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")

// fold lower-cases the text, writes the umlauts out and collapses every run
// of white space to one space. The one rune whose JavaScript lower case is
// two runes (İ) is written as JavaScript writes it. A rune outside the Basic
// Multilingual Plane is two UTF-16 code units in JavaScript, and the
// platform's patterns (no u flag) count code units: each such rune becomes
// two U+FFFD, which no pattern names and which are neither "|" nor ".", so
// every bounded repetition counts the text as JavaScript counts it.
func fold(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "İ", "i̇"))
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
			b.WriteString("��")
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

// ClosureScopeOf is whether a closure (or roadworks) of the Autobahn GmbH is
// the carriageway's or a ramp's (closureScopeOf): the publisher's location
// line decides (the first description line, nil skipped, that names
// either), else a subtitle "AS X (aus Richtung Y)", else the carriageway,
// the conservative reading.
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

// MessageScopeOf is a police or Land message's scope when it closes
// something (messageScopeOf): a ramp when its words close an entry, an exit
// or a junction's link and never the road itself, else the carriageway.
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

// AutobahnRow is what an Autobahn GmbH row is judged by (AutobahnSignalRow).
type AutobahnRow struct {
	Kind string
	// AbnormalTrafficType is nil when the row carries none.
	AbnormalTrafficType *string
	// DelayS is the row's delay as a number; nil when none.
	DelayS      *float64
	Title       *string
	Subtitle    *string
	Description []*string
	// Blocked is the publisher's isBlocked.
	Blocked bool
}

// AutobahnEventSignal is the signal of one Autobahn GmbH row
// (autobahnEventSignal).
func AutobahnEventSignal(r AutobahnRow) Signal {
	if r.Kind == string(SignalClosure) || r.Kind == string(SignalRoadworks) {
		return WorksSignal(SignalKind(r.Kind), r.Blocked, ClosureScopeOf(r.Description, r.Subtitle))
	}
	if r.AbnormalTrafficType != nil {
		return Signal{Kind: SignalJam, DelayS: r.DelayS}
	}
	if w := SafetyWordOf(append([]*string{r.Title, r.Subtitle}, r.Description...)); w != WordNone {
		return Signal{Kind: SignalSafety, Word: w}
	}
	return Signal{Kind: SignalWarning}
}

// Message is what a police or Land message is judged by (AmpelMessage).
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

// MessageSignal is the signal of one police or Land message
// (messageSignal): a safety message only by its words; a closure by its
// scope; a jam by its delay; works and lane restrictions as roadworks by
// their scope; anything else an ordinary warning.
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

// BridgeState is a bridge's state by its words (BridgeState): closed,
// restricted (a wind warning with a limit), or BridgeNone.
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

// FeedCheck is a feed's newest check (FeedCheckLike): CheckedAt nil when it
// was never checked, CadenceS nil when it states no cadence.
type FeedCheck struct {
	CheckedAt *time.Time
	CadenceS  *float64
	Paused    bool
}

// CheckAlive is the platform's one alive-or-paused rule (checkAlive): true
// when the check is inside cadences of its cadence before now (inclusive),
// or the refresh is paused and the feed was checked. A check without a
// cadence is judged against defaultCadenceS; with none (nil), it is never
// inside.
func CheckAlive(c *FeedCheck, now time.Time, cadences float64, defaultCadenceS *float64) bool {
	if c == nil || c.CheckedAt == nil {
		return false
	}
	if c.Paused {
		return true
	}
	cadence := c.CadenceS
	if cadence == nil {
		cadence = defaultCadenceS
	}
	if cadence == nil {
		return false
	}
	return float64(now.Sub(*c.CheckedAt).Milliseconds()) <= cadences**cadence*1000
}

// defaultCadenceS is the cadence a check without one is judged against
// (feedSightOf's default, 600 s).
var defaultCadenceS = 600.0

// FeedSightOf is a feed's sight from its newest check (feedSightOf): fresh
// inside three cadences (600 s when it states none) or while paused, stale
// after, missing with no check.
func FeedSightOf(c *FeedCheck, now time.Time) Sight {
	if c == nil || c.CheckedAt == nil {
		return SightMissing
	}
	if CheckAlive(c, now, 3, &defaultCadenceS) {
		return SightFresh
	}
	return SightStale
}

// DWDSightOf is the DWD group's sight (dwdSightOf): missing when the
// warnings read did not answer; fresh when it holds a warning, or when the
// warnings feed was checked inside three cadences (a quiet read of a live
// feed); missing otherwise: a quiet read of a dead feed is no sight.
func DWDSightOf(readOK bool, warnings int, check *FeedCheck, now time.Time) Sight {
	if !readOK {
		return SightMissing
	}
	if warnings > 0 || FeedSightOf(check, now) == SightFresh {
		return SightFresh
	}
	return SightMissing
}

// PoliceFeeds are the feeds that tell the lamp one state's police and Land
// messages are seen: the state's own message feeds (Land), the relay where
// it was measured to carry the state (Relay; the platform's inventory), and
// the own feeds whose rows are answered only while current (CurrentOnly).
type PoliceFeeds struct {
	Land        []string
	Relay       []string
	CurrentOnly []string
}

var sightRank = map[Sight]int{SightFresh: 0, SightStale: 1, SightMissing: 2}

// PoliceSightOf is the police sight of each state in scope (policeSightOf):
// the best of the state's own message feeds and its relay; a state with
// neither is missing; an own feed answered only while current caps the
// state's sight. In the states' order, each once.
func PoliceSightOf(lands []string, feeds map[string]PoliceFeeds, sightOfFeed func(feed string) Sight) []SightEntry {
	var out []SightEntry
	seen := map[string]bool{}
	for _, land := range lands {
		if seen[land] {
			continue
		}
		seen[land] = true
		f := feeds[land]
		best := SightMissing
		for _, k := range append(slices.Clone(f.Land), f.Relay...) {
			if s := sightOfFeed(k); sightRank[s] < sightRank[best] {
				best = s
			}
		}
		for _, k := range f.Land {
			if !slices.Contains(f.CurrentOnly, k) {
				continue
			}
			if s := sightOfFeed(k); sightRank[s] > sightRank[best] {
				best = s
			}
		}
		out = append(out, SightEntry{Group: GroupPolice, State: best, Land: land})
	}
	return out
}

// PoliceSight is the police sight of the states in scope as the tool reads
// it (policeSightFromChecks): one missing entry when no state is in scope,
// else PoliceSightOf with each feed's sight from its newest check
// (FeedSightOf; a feed with no check is missing).
func PoliceSight(lands []string, feeds map[string]PoliceFeeds, checks map[string]FeedCheck, now time.Time) []SightEntry {
	if len(lands) == 0 {
		return []SightEntry{{Group: GroupPolice, State: SightMissing}}
	}
	return PoliceSightOf(lands, feeds, func(feed string) Sight {
		c, ok := checks[feed]
		if !ok {
			return SightMissing
		}
		return FeedSightOf(&c, now)
	})
}
