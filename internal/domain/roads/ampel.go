package roads

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// This file is a port of the platform's motorway Ampel, line for line:
// packages/ingest/src/analysis/motorway-ampel.ts (signalLamp, ampelOf,
// reasonKeyOf, safetyWordOf, closureScopeOf, messageScopeOf, worksSignal,
// autobahnEventSignal, dwdSightOf, messageSignal) and the order of the
// reasons inside a colour of packages/mcp/src/tools/autobahn-ampel.ts
// (ORDER in toolAmpel). The regular expressions are copied verbatim. The
// platform's yellow is the service's amber; nothing else is renamed.

// The thresholds (AMPEL_JAM_RED_S, AMPEL_DWD_RED_LEVEL,
// AMPEL_DWD_YELLOW_LEVEL).
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

// SightEntry is one group's sight (AmpelSightEntry).
type SightEntry struct {
	Group string
	State Sight
}

// AmpelReason is why a lamp: a signal and the colour it gives on its own, or
// a group whose sight is limited (always amber). Exactly one of Signal and
// Sight is set.
type AmpelReason struct {
	Lamp   Lamp
	Signal *Signal
	Sight  *SightEntry
}

// Ampel is a lamp and every reason, red before amber, in the order of the
// signals inside each colour; empty exactly when green.
type Ampel struct {
	Lamp    Lamp
	Reasons []AmpelReason
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
		if s.Level >= DWDRedLevel {
			return LampRed, true
		}
		if s.Level >= DWDAmberLevel {
			return LampAmber, true
		}
		return "", false
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

// AmpelOf is the lamp: the first match from the top of the platform's table.
// sight lists the safety-relevant groups read and how fresh each is; a group
// that is not fresh is an amber reason of its own, so green needs every one
// fresh. An empty sight cannot be green: a lamp that read nothing has seen
// nothing.
func AmpelOf(signals []Signal, sight []SightEntry) Ampel {
	var red, amber []AmpelReason
	for i := range signals {
		s := signals[i]
		switch l, _ := SignalLamp(s); l {
		case LampRed:
			red = append(red, AmpelReason{Lamp: LampRed, Signal: &s})
		case LampAmber:
			amber = append(amber, AmpelReason{Lamp: LampAmber, Signal: &s})
		}
	}
	for _, g := range sight {
		if g.State != SightFresh {
			amber = append(amber, AmpelReason{Lamp: LampAmber, Sight: &SightEntry{Group: g.Group, State: g.State}})
		}
	}
	if len(sight) == 0 && len(red) == 0 && len(amber) == 0 {
		amber = append(amber, AmpelReason{Lamp: LampAmber, Sight: &SightEntry{Group: "autobahn", State: SightMissing}})
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

// SortSignals orders the signals as toolAmpel does before AmpelOf, stably:
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
func ReasonKeyOf(r AmpelReason) ReasonKey {
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
// two runes (İ) is written as JavaScript writes it.
func fold(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
	return jsSpaces.ReplaceAllString(umlauts.Replace(s), " ")
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

// LineScope is the scope the first description line that names a ramp or
// the carriageway gives (nil: absent, skipped); false when no line names
// either.
func LineScope(description []*string) (ClosureScope, bool) {
	for _, raw := range description {
		if raw == nil {
			continue
		}
		l := fold(trim(*raw))
		if rampLine.MatchString(l) {
			return ScopeRamp, true
		}
		if carriagewayLine.MatchString(l) {
			return ScopeCarriageway, true
		}
	}
	return "", false
}

// ClosureScopeOf is whether a closure (or roadworks) of the Autobahn GmbH is
// the carriageway's or a ramp's (closureScopeOf): the publisher's location
// line decides, else a subtitle "AS X (aus Richtung Y)", else the
// carriageway, the conservative reading.
func ClosureScopeOf(description []*string, subtitle *string) ClosureScope {
	if s, ok := LineScope(description); ok {
		return s
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

// DWDSightOf is the DWD group's sight (dwdSightOf): missing when the
// warnings read did not answer; fresh when it holds a warning, or when the
// warnings source itself is fresh (a quiet read of a live source); missing
// otherwise: a quiet read of a dead source is no sight.
func DWDSightOf(readOK bool, warnings int, source Sight) Sight {
	if !readOK {
		return SightMissing
	}
	if warnings > 0 || source == SightFresh {
		return SightFresh
	}
	return SightMissing
}

// AmpelMessage is what a police or Land message is judged by (AmpelMessage).
type AmpelMessage struct {
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
func MessageSignal(m AmpelMessage) Signal {
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
