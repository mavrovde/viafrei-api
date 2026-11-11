package ampel_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The cases below are transcribed from the platform's own test of the rule
// this package ports, motorway-ampel.test.ts of its analysis module
// (cited as ts:LINE), and driven through the port. Where the platform's
// function takes a value Go cannot type (a non-string description entry, a
// delay as text, isBlocked as text), the case carries the value the port
// takes in its place, and says so.

const tsFile = "motorway-ampel.test.ts"

func str(s string) *string { return &s }

func secs(s float64) *float64 { return &s }

func strs(ss ...string) []*string {
	out := make([]*string, len(ss))
	for i := range ss {
		out[i] = &ss[i]
	}
	return out
}

// fresh is the platform's FRESH sight (ts:33).
var fresh = []ampel.SightEntry{{Group: "autobahn", State: ampel.SightFresh}, {Group: "dwd", State: ampel.SightFresh}}

type lampCase struct {
	at   int
	in   ampel.Signal
	lamp ampel.Lamp
	ok   bool
}

// signalCases are every row of the table and its edges (ts:48-102).
var signalCases = []lampCase{
	{50, ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}, ampel.LampRed, true},
	{51, ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordWrongWay}, ampel.LampRed, true},
	{52, ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordObject}, ampel.LampRed, true},
	{53, ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordPerson}, ampel.LampRed, true},
	{54, ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordAccidentClosure}, ampel.LampRed, true},
	{55, ampel.Signal{Kind: ampel.SignalDWD, Level: 3}, ampel.LampRed, true},
	{56, ampel.Signal{Kind: ampel.SignalDWD, Level: 4}, ampel.LampRed, true},
	{57, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(1800)}, ampel.LampRed, true},
	{58, ampel.Signal{Kind: ampel.SignalBridgeWind}, ampel.LampRed, true},
	{65, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(1799)}, ampel.LampAmber, true},
	{66, ampel.Signal{Kind: ampel.SignalJam}, ampel.LampAmber, true},
	{67, ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}, ampel.LampAmber, true},
	{68, ampel.Signal{Kind: ampel.SignalWarning}, ampel.LampAmber, true},
	{69, ampel.Signal{Kind: ampel.SignalDWD, Level: 1}, ampel.LampAmber, true},
	{70, ampel.Signal{Kind: ampel.SignalDWD, Level: 2}, ampel.LampAmber, true},
	{71, ampel.Signal{Kind: ampel.SignalSlippery}, ampel.LampAmber, true},
	{72, ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}, ampel.LampAmber, true},
	{79, ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeRamp}, "", false},
	{80, ampel.Signal{Kind: ampel.SignalDWD, Level: 0}, "", false},
	{87, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(29 * 60)}, ampel.LampAmber, true},
	{88, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(30 * 60)}, ampel.LampRed, true},
	{89, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(30*60 - 1)}, ampel.LampAmber, true},
	{92, ampel.Signal{Kind: ampel.SignalDWD, Level: 2}, ampel.LampAmber, true},
	{93, ampel.Signal{Kind: ampel.SignalDWD, Level: 3}, ampel.LampRed, true},
}

type ampelCase struct {
	at      int
	signals []ampel.Signal
	sight   []ampel.SightEntry
	lamp    ampel.Lamp
	// reasons are the keys of the reasons, in order.
	reasons []ampel.ReasonKey
}

// ampelCases are ampelOf's cases (ts:81, 95-147, 326-327).
var ampelCases = []ampelCase{
	{81, []ampel.Signal{{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeRamp}, {Kind: ampel.SignalDWD, Level: 0}}, fresh, ampel.LampGreen, nil},
	{96, nil, []ampel.SightEntry{{Group: "autobahn", State: ampel.SightStale}, {Group: "dwd", State: ampel.SightFresh}}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}},
	{99, nil, []ampel.SightEntry{{Group: "autobahn", State: ampel.SightFresh}, {Group: "dwd", State: ampel.SightMissing}}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}},
	{107, []ampel.Signal{{Kind: ampel.SignalJam, DelayS: secs(600)}, {Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}}, fresh, ampel.LampRed, []ampel.ReasonKey{"closure", "jam"}},
	{118, []ampel.Signal{{Kind: ampel.SignalSafety, Word: ampel.WordWrongWay}}, []ampel.SightEntry{{Group: "autobahn", State: ampel.SightStale}}, ampel.LampRed, []ampel.ReasonKey{"safety", ampel.KeySight}},
	{124, []ampel.Signal{{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}, {Kind: ampel.SignalWarning}, {Kind: ampel.SignalJam}}, fresh, ampel.LampAmber, []ampel.ReasonKey{"roadworks", "warning", "jam"}},
	{138, nil, []ampel.SightEntry{{Group: "autobahn", State: ampel.SightStale}, {Group: "dwd", State: ampel.SightFresh}}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}},
	{141, nil, fresh, ampel.LampGreen, nil},
	{144, nil, nil, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}},
	{310, nil, []ampel.SightEntry{{Group: "autobahn", State: ampel.SightFresh}, {Group: "police", State: ampel.SightMissing}}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}},
	{313, nil, []ampel.SightEntry{{Group: "autobahn", State: ampel.SightFresh}, {Group: "police", State: ampel.SightFresh}}, ampel.LampGreen, nil},
	{326, []ampel.Signal{ampel.AutobahnEventSignal(ampel.AutobahnRow{Kind: "roadworks", Title: str("A7 | Kassel - Göttingen"), Blocked: true})}, nil, ampel.LampRed, []ampel.ReasonKey{"closure"}},
	{327, []ampel.Signal{ampel.AutobahnEventSignal(ampel.AutobahnRow{Kind: "roadworks", Title: str("A7 | Kassel - Göttingen"), Subtitle: str("AS Kassel-Nord (aus Richtung Hannover)"), Blocked: true})}, nil, ampel.LampAmber, []ampel.ReasonKey{ampel.KeyRampClosure}},
}

type wordCase struct {
	at    int
	texts []*string
	word  ampel.SafetyWord
}

// wordCases are safetyWordOf's cases (ts:159-183).
var wordCases = []wordCase{
	{161, strs("Gefahr durch einen Falschfahrer"), ampel.WordWrongWay},
	{162, strs("Achtung, Geisterfahrer zwischen AS A und AS B"), ampel.WordWrongWay},
	{163, strs("Gefahr durch Gegenstände auf der Fahrbahn"), ampel.WordObject},
	{164, strs("verlorene Ladung"), ampel.WordObject},
	{165, strs("Personen auf der Fahrbahn"), ampel.WordPerson},
	{166, strs("Gefahr durch Tiere auf der Fahrbahn"), ampel.WordAnimal},
	{167, strs("Unfall", "Vollsperrung"), ampel.WordAccidentClosure},
	{168, strs("Ereignismeldung:", "- Unfall", "- Fahrbahn blockiert"), ampel.WordAccidentClosure},
	{169, strs("Richtungsfahrbahn gesperrt nach Unfall"), ampel.WordAccidentClosure},
	{174, strs("Ereignismeldung:", "- Unfall", "- rechter Fahrstreifen gesperrt"), ampel.WordNone},
	{175, strs("Unfall"), ampel.WordNone},
	{176, strs("Ereignismeldung:", "- Stockender Verkehr", "- Reisezeitverlust: 5 Minuten"), ampel.WordNone},
	{177, []*string{nil, nil}, ampel.WordNone},
	{182, strs("Falschfahrer", "Gegenstände auf der Fahrbahn"), ampel.WordWrongWay},
}

type scopeCase struct {
	at          int
	description []*string
	subtitle    *string
	scope       ampel.ClosureScope
}

// closureCases are closureScopeOf's cases (ts:186-202). ts:201 passes
// [7, null, {a: 1}]: entries that are not strings, skipped; the port takes
// absent entries (nil) in their place.
var closureCases = []scopeCase{
	{188, strs("Zeitraum dieser Bauphase:", "", "Auffahrt auf die A9: AS Weißenfels (aus Richtung Pörstental)"), str(" AS Weißenfels (aus Richtung Pörstental)"), ampel.ScopeRamp},
	{189, strs("Abfahrt von der A92: AK Neufahrn (aus Richtung Unterschleißheim)"), str(""), ampel.ScopeRamp},
	{190, strs("Auffahrt auf die B91"), str(""), ampel.ScopeRamp},
	{191, strs("Von Auffahrt auf die A9: AS Klein Marzehns (aus Richtung Rosselquelle) nach A9: Halle/Leipzig -> Berlin"), str(""), ampel.ScopeRamp},
	{194, strs("Beginn: 05.07.26", "A45: Hagen -> Gießen, zwischen 3.7 km hinter Brachtsiepen und 1.5 km vor AS Drolshagen"), str(" Hagen -> Gießen"), ampel.ScopeCarriageway},
	{195, strs("Von A9: Halle/Leipzig -> Nürnberg, zwischen 3.1 km hinter AS Bad Berneck/Himmelkron und AD Bayreuth/Kulmbach nach Abfahrt von der A9"), str(" Halle/Leipzig -> Nürnberg"), ampel.ScopeCarriageway},
	{198, nil, str(" AK Köln-Gremberg (aus Richtung Köln-Poll)"), ampel.ScopeRamp},
	{199, []*string{}, str(" Flensburg -> Füssen"), ampel.ScopeCarriageway},
	{200, nil, nil, ampel.ScopeCarriageway},
	{201, []*string{nil, nil, nil}, nil, ampel.ScopeCarriageway},
}

// messageScopeCases are messageScopeOf's cases (ts:228-232).
var messageScopeCases = []scopeCase{
	{229, strs("Auffahrt gesperrt", "Vollsperrung"), nil, ampel.ScopeCarriageway},
	{230, strs("gesperrt ist die Ausfahrt Seevetal"), nil, ampel.ScopeRamp},
	{231, []*string{nil, nil}, nil, ampel.ScopeCarriageway},
}

type messageCase struct {
	at   int
	in   ampel.Message
	want ampel.Signal
	// lamp is checked when set: the case asserts the lamp, not the signal.
	lamp *ampel.Lamp
}

func lampOf(l ampel.Lamp) *ampel.Lamp { return &l }

// none marks a case whose signal gives no colour.
var none = lampOf("")

// messageCases are messageSignal's cases (ts:205-227).
var messageCases = []messageCase{
	{208, ampel.Message{Kind: "warning", What: "Gefahr", DetailText: str("Falschfahrer zwischen AS Soltau-Ost und AS Bispingen")}, ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordWrongWay}, nil},
	{209, ampel.Message{Kind: "accident", What: "Unfall", DetailText: str("Unfall, Fahrbahn voll gesperrt")}, ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordAccidentClosure}, nil},
	{210, ampel.Message{Kind: "other", What: "Gegenstände auf der Fahrbahn"}, ampel.Signal{}, lampOf(ampel.LampRed)},
	{213, ampel.Message{Kind: "accident", What: "Unfall", DetailText: str("rechter Fahrstreifen gesperrt")}, ampel.Signal{Kind: ampel.SignalWarning}, nil},
	{214, ampel.Message{Kind: "warning", What: "Glätte"}, ampel.Signal{Kind: ampel.SignalWarning}, nil},
	{215, ampel.Message{Kind: "other", What: "Hinweis"}, ampel.Signal{Kind: ampel.SignalWarning}, nil},
	{218, ampel.Message{Kind: "closure", What: "A7 Hamburg Richtung Hannover", DetailText: str("zwischen AS Soltau-Ost und AS Bispingen Vollsperrung")}, ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}, nil},
	{219, ampel.Message{Kind: "closure", What: "A7", DetailText: str("Anschlussstelle Soltau-Ost: Auffahrt gesperrt")}, ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}, nil},
	{220, ampel.Message{Kind: "closure", What: "A7", DetailText: str("gesperrt")}, ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}, nil},
	{223, ampel.Message{Kind: "jam", What: "Stau", DelayS: secs(ampel.JamRedS)}, ampel.Signal{}, lampOf(ampel.LampRed)},
	{224, ampel.Message{Kind: "jam", What: "Stau"}, ampel.Signal{}, lampOf(ampel.LampAmber)},
	{225, ampel.Message{Kind: "restriction", What: "Fahrbahnverengung"}, ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}, nil},
	{226, ampel.Message{Kind: "roadworks", What: "Baustelle", DetailText: str("Ausfahrt gesperrt")}, ampel.Signal{}, none},
}

type rowCase struct {
	at   int
	in   ampel.AutobahnRow
	want ampel.Signal
}

// row is the platform's r() (ts:318): a warning titled "A7 | Kassel -
// Göttingen" with no traffic type, delay, subtitle or description.
func row(f func(*ampel.AutobahnRow)) ampel.AutobahnRow {
	r := ampel.AutobahnRow{Kind: "warning", Title: str("A7 | Kassel - Göttingen")}
	f(&r)
	return r
}

// rowCases are autobahnEventSignal's cases (ts:319-331). isBlocked "true" /
// "false" is the port's Blocked; the delay "1800" is the number the port
// takes.
var rowCases = []rowCase{
	{320, row(func(r *ampel.AutobahnRow) { r.Kind, r.Description = "closure", strs("Auffahrt auf die A7: gesperrt") }), ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}},
	{321, row(func(r *ampel.AutobahnRow) { r.Kind = "roadworks" }), ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}},
	{323, row(func(r *ampel.AutobahnRow) { r.Kind, r.Blocked = "roadworks", true }), ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}},
	{324, row(func(r *ampel.AutobahnRow) {
		r.Kind, r.Blocked, r.Subtitle = "roadworks", true, str("AS Kassel-Nord (aus Richtung Hannover)")
	}), ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}},
	{325, row(func(r *ampel.AutobahnRow) { r.Kind = "roadworks" }), ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}},
	{328, row(func(r *ampel.AutobahnRow) { r.AbnormalTrafficType, r.DelayS = str("STATIONARY_TRAFFIC"), secs(1800) }), ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(1800)}},
	{329, row(func(r *ampel.AutobahnRow) { r.Description = strs("Tiere auf der Fahrbahn") }), ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordAnimal}},
	{330, row(func(r *ampel.AutobahnRow) { r.Description = strs("Rechter Fahrstreifen gesperrt") }), ampel.Signal{Kind: ampel.SignalWarning}},
}

type sightCase struct {
	at       int
	readOK   bool
	warnings int
	// source is the sight of the warnings source; the platform passes its
	// source check (ts:334: checked s seconds ago at a cadence of 300 s,
	// fresh inside three cadences): ago(10) and ago(900) are fresh,
	// ago(901) is stale, no check is missing.
	source ampel.Sight
	want   ampel.Sight
}

// sightCases are dwdSightOf's cases (ts:332-340).
var sightCases = []sightCase{
	{335, false, 3, ampel.SightFresh, ampel.SightMissing},
	{336, true, 1, ampel.SightMissing, ampel.SightFresh},
	{337, true, 0, ampel.SightFresh, ampel.SightFresh},
	{338, true, 0, ampel.SightStale, ampel.SightMissing},
	{339, true, 0, ampel.SightMissing, ampel.SightMissing},
}

func sameSignal(a, b ampel.Signal) bool {
	return a.Kind == b.Kind && a.Scope == b.Scope && a.Word == b.Word && a.Level == b.Level &&
		(a.DelayS == nil) == (b.DelayS == nil) && (a.DelayS == nil || *a.DelayS == *b.DelayS)
}

func keys(a ampel.Ampel) []ampel.ReasonKey {
	var out []ampel.ReasonKey
	for _, r := range a.Reasons {
		out = append(out, ampel.ReasonKeyOf(r))
	}
	return out
}

// TranscribedCases is how many platform cases this file drives through the
// port.
func transcribedCases() int {
	return len(signalCases) + len(ampelCases) + len(wordCases) + len(closureCases) + len(messageScopeCases) +
		len(messageCases) + len(rowCases) + len(sightCases) + 1
}

func TestThePlatformsAmpelCases(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	at := func(line int) string { return fmt.Sprintf("%s:%d", tsFile, line) }
	for _, c := range signalCases {
		lamp, ok := ampel.SignalLamp(c.in)
		if lamp != c.lamp || ok != c.ok {
			t.Errorf("%s: signalLamp(%+v) = %q %v, want %q %v", at(c.at), c.in, lamp, ok, c.lamp, c.ok)
		}
		if c.ok {
			if a := ampel.Of([]ampel.Signal{c.in}, fresh); a.Lamp != c.lamp {
				t.Errorf("%s: ampelOf lamp %q, want %q", at(c.at), a.Lamp, c.lamp)
			}
		}
	}
	for _, c := range ampelCases {
		a := ampel.Of(c.signals, c.sight)
		if a.Lamp != c.lamp || !slices.Equal(keys(a), c.reasons) {
			t.Errorf("%s: ampelOf = %q %v, want %q %v", at(c.at), a.Lamp, keys(a), c.lamp, c.reasons)
		}
	}
	for _, c := range wordCases {
		if got := ampel.SafetyWordOf(c.texts); got != c.word {
			t.Errorf("%s: safetyWordOf = %q, want %q", at(c.at), got, c.word)
		}
	}
	for _, c := range closureCases {
		if got := ampel.ClosureScopeOf(c.description, c.subtitle); got != c.scope {
			t.Errorf("%s: closureScopeOf = %q, want %q", at(c.at), got, c.scope)
		}
	}
	for _, c := range messageScopeCases {
		if got := ampel.MessageScopeOf(c.description); got != c.scope {
			t.Errorf("%s: messageScopeOf = %q, want %q", at(c.at), got, c.scope)
		}
	}
	for _, c := range messageCases {
		got := ampel.MessageSignal(c.in)
		if c.lamp != nil {
			if l, _ := ampel.SignalLamp(got); l != *c.lamp {
				t.Errorf("%s: signalLamp(messageSignal) = %q, want %q", at(c.at), l, *c.lamp)
			}
			continue
		}
		if !sameSignal(got, c.want) {
			t.Errorf("%s: messageSignal = %+v, want %+v", at(c.at), got, c.want)
		}
	}
	for _, c := range rowCases {
		if got := ampel.AutobahnEventSignal(c.in); !sameSignal(got, c.want) {
			t.Errorf("%s: autobahnEventSignal = %+v, want %+v", at(c.at), got, c.want)
		}
	}
	for _, c := range sightCases {
		if got := ampel.DWDSightOf(c.readOK, c.warnings, c.source); got != c.want {
			t.Errorf("%s: dwdSightOf = %q, want %q", at(c.at), got, c.want)
		}
	}
	// ts:384: twelve ramp closures are twelve rampClosure reasons.
	var ramps []ampel.Signal
	for range 12 {
		ramps = append(ramps, ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp})
	}
	if n := len(slices.DeleteFunc(keys(ampel.Of(ramps, fresh)), func(k ampel.ReasonKey) bool { return k != ampel.KeyRampClosure })); n != 12 {
		t.Errorf("%s: %d rampClosure keys, want 12", at(384), n)
	}
	t.Logf("%d cases transcribed from %s", transcribedCases(), tsFile)
}

// TestTheTranscriptionCount pins the number of transcribed cases, so a case
// dropped from the tables is noticed.
func TestTheTranscriptionCount(t *testing.T) {
	if n := transcribedCases(); n != 91 {
		t.Errorf("%d transcribed cases, want 91", n)
	}
}

// TestTheSortIsThePlatformsOrder: ORDER of autobahn-ampel.ts (toolAmpel),
// jams by delay descending with no delay last, DWD warnings by level
// descending, every other tie in the caller's order.
func TestTheSortIsThePlatformsOrder(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	in := []ampel.Signal{
		{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway},
		{Kind: ampel.SignalWarning, Word: "a"},
		{Kind: ampel.SignalSlippery},
		{Kind: ampel.SignalDWD, Level: 1},
		{Kind: ampel.SignalJam},
		{Kind: ampel.SignalJam, DelayS: secs(0)},
		{Kind: ampel.SignalJam, DelayS: secs(60)},
		{Kind: ampel.SignalDWD, Level: 3},
		{Kind: ampel.SignalJam, DelayS: secs(600)},
		{Kind: ampel.SignalJam, DelayS: secs(60)},
		{Kind: ampel.SignalBridgeWind},
		{Kind: ampel.SignalSafety},
		{Kind: ampel.SignalWarning, Word: "b"},
		{Kind: ampel.SignalClosure},
	}
	ampel.SortSignals(in)
	var got []string
	for _, s := range in {
		d := "-"
		if s.DelayS != nil {
			d = fmt.Sprint(*s.DelayS)
		}
		got = append(got, fmt.Sprintf("%s/%s/%d/%s", s.Kind, d, s.Level, s.Word))
	}
	want := []string{"closure/-/0/", "safety/-/0/", "bridgeWind/-/0/", "jam/600/0/", "jam/60/0/", "jam/60/0/", "jam/0/0/", "jam/-/0/",
		"dwd/-/3/", "dwd/-/1/", "slippery/-/0/", "warning/-/0/a", "warning/-/0/b", "roadworks/-/0/"}
	if !slices.Equal(got, want) {
		t.Errorf("order\n got %v\nwant %v", got, want)
	}
}

// TestALineScopeSkipsAnAbsentLine: an absent line is skipped, never the end
// of the description.
func TestALineScopeSkipsAnAbsentLine(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	if s, ok := ampel.LineScope([]*string{nil, str("Auffahrt auf die A9: gesperrt")}); !ok || s != ampel.ScopeRamp {
		t.Errorf("a ramp line after an absent one: %v %v", s, ok)
	}
	if _, ok := ampel.LineScope([]*string{nil, str("Stau")}); ok {
		t.Error("no location line gave a scope")
	}
}

// TestAnUnknownSignalGivesNoColour: a kind outside the table gives none.
func TestAnUnknownSignalGivesNoColour(t *testing.T) {
	if l, ok := ampel.SignalLamp(ampel.Signal{Kind: "x"}); ok || l != "" {
		t.Errorf("unknown kind: %q %v", l, ok)
	}
}

// TestTheFoldIsJavaScripts: white space JavaScript's \s matches and Go's
// does not (a no-break space, a vertical tab) still separates the words,
// and the trim of a location line removes it.
func TestTheFoldIsJavaScripts(t *testing.T) {
	if w := ampel.SafetyWordOf(strs("Tiere\u00a0auf\vder Fahrbahn")); w != ampel.WordAnimal {
		t.Errorf("no-break space: %q", w)
	}
	if s := ampel.ClosureScopeOf(strs("\u00a0\ufeffAuffahrt auf die A9"), nil); s != ampel.ScopeRamp {
		t.Errorf("trimmed line: %q", s)
	}
	if w := ampel.SafetyWordOf(strs("\u0130N FALSCHER FAHRTRICHTUNG")); w != ampel.WordNone {
		t.Errorf("dotted capital I lower-cases to i and a combining dot, as in JavaScript: %q", w)
	}
}
