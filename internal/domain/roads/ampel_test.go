package roads_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The cases below are transcribed from the platform's own test of the rule
// this package ports, packages/ingest/src/analysis/motorway-ampel.test.ts
// (cited as ts:LINE), and driven through the port. Where the platform's
// function takes a value Go cannot type (a non-string description entry, a
// delay as text, isBlocked as text), the case carries the value the port
// takes in its place, and says so.

const tsFile = "packages/ingest/src/analysis/motorway-ampel.test.ts"

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
var fresh = []roads.SightEntry{{Group: "autobahn", State: roads.SightFresh}, {Group: "dwd", State: roads.SightFresh}}

type lampCase struct {
	at   int
	in   roads.Signal
	lamp roads.Lamp
	ok   bool
}

// signalCases are every row of the table and its edges (ts:48-102).
var signalCases = []lampCase{
	{50, roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeCarriageway}, roads.LampRed, true},
	{51, roads.Signal{Kind: roads.SignalSafety, Word: roads.WordWrongWay}, roads.LampRed, true},
	{52, roads.Signal{Kind: roads.SignalSafety, Word: roads.WordObject}, roads.LampRed, true},
	{53, roads.Signal{Kind: roads.SignalSafety, Word: roads.WordPerson}, roads.LampRed, true},
	{54, roads.Signal{Kind: roads.SignalSafety, Word: roads.WordAccidentClosure}, roads.LampRed, true},
	{55, roads.Signal{Kind: roads.SignalDWD, Level: 3}, roads.LampRed, true},
	{56, roads.Signal{Kind: roads.SignalDWD, Level: 4}, roads.LampRed, true},
	{57, roads.Signal{Kind: roads.SignalJam, DelayS: secs(1800)}, roads.LampRed, true},
	{58, roads.Signal{Kind: roads.SignalBridgeWind}, roads.LampRed, true},
	{65, roads.Signal{Kind: roads.SignalJam, DelayS: secs(1799)}, roads.LampAmber, true},
	{66, roads.Signal{Kind: roads.SignalJam}, roads.LampAmber, true},
	{67, roads.Signal{Kind: roads.SignalRoadworks, Scope: roads.ScopeCarriageway}, roads.LampAmber, true},
	{68, roads.Signal{Kind: roads.SignalWarning}, roads.LampAmber, true},
	{69, roads.Signal{Kind: roads.SignalDWD, Level: 1}, roads.LampAmber, true},
	{70, roads.Signal{Kind: roads.SignalDWD, Level: 2}, roads.LampAmber, true},
	{71, roads.Signal{Kind: roads.SignalSlippery}, roads.LampAmber, true},
	{72, roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeRamp}, roads.LampAmber, true},
	{79, roads.Signal{Kind: roads.SignalRoadworks, Scope: roads.ScopeRamp}, "", false},
	{80, roads.Signal{Kind: roads.SignalDWD, Level: 0}, "", false},
	{87, roads.Signal{Kind: roads.SignalJam, DelayS: secs(29 * 60)}, roads.LampAmber, true},
	{88, roads.Signal{Kind: roads.SignalJam, DelayS: secs(30 * 60)}, roads.LampRed, true},
	{89, roads.Signal{Kind: roads.SignalJam, DelayS: secs(30*60 - 1)}, roads.LampAmber, true},
	{92, roads.Signal{Kind: roads.SignalDWD, Level: 2}, roads.LampAmber, true},
	{93, roads.Signal{Kind: roads.SignalDWD, Level: 3}, roads.LampRed, true},
}

type ampelCase struct {
	at      int
	signals []roads.Signal
	sight   []roads.SightEntry
	lamp    roads.Lamp
	// reasons are the keys of the reasons, in order.
	reasons []roads.ReasonKey
}

// ampelCases are ampelOf's cases (ts:81, 95-147, 326-327).
var ampelCases = []ampelCase{
	{81, []roads.Signal{{Kind: roads.SignalRoadworks, Scope: roads.ScopeRamp}, {Kind: roads.SignalDWD, Level: 0}}, fresh, roads.LampGreen, nil},
	{96, nil, []roads.SightEntry{{Group: "autobahn", State: roads.SightStale}, {Group: "dwd", State: roads.SightFresh}}, roads.LampAmber, []roads.ReasonKey{roads.KeySight}},
	{99, nil, []roads.SightEntry{{Group: "autobahn", State: roads.SightFresh}, {Group: "dwd", State: roads.SightMissing}}, roads.LampAmber, []roads.ReasonKey{roads.KeySight}},
	{107, []roads.Signal{{Kind: roads.SignalJam, DelayS: secs(600)}, {Kind: roads.SignalClosure, Scope: roads.ScopeCarriageway}}, fresh, roads.LampRed, []roads.ReasonKey{"closure", "jam"}},
	{118, []roads.Signal{{Kind: roads.SignalSafety, Word: roads.WordWrongWay}}, []roads.SightEntry{{Group: "autobahn", State: roads.SightStale}}, roads.LampRed, []roads.ReasonKey{"safety", roads.KeySight}},
	{124, []roads.Signal{{Kind: roads.SignalRoadworks, Scope: roads.ScopeCarriageway}, {Kind: roads.SignalWarning}, {Kind: roads.SignalJam}}, fresh, roads.LampAmber, []roads.ReasonKey{"roadworks", "warning", "jam"}},
	{138, nil, []roads.SightEntry{{Group: "autobahn", State: roads.SightStale}, {Group: "dwd", State: roads.SightFresh}}, roads.LampAmber, []roads.ReasonKey{roads.KeySight}},
	{141, nil, fresh, roads.LampGreen, nil},
	{144, nil, nil, roads.LampAmber, []roads.ReasonKey{roads.KeySight}},
	{310, nil, []roads.SightEntry{{Group: "autobahn", State: roads.SightFresh}, {Group: "police", State: roads.SightMissing}}, roads.LampAmber, []roads.ReasonKey{roads.KeySight}},
	{313, nil, []roads.SightEntry{{Group: "autobahn", State: roads.SightFresh}, {Group: "police", State: roads.SightFresh}}, roads.LampGreen, nil},
	{326, []roads.Signal{roads.AutobahnEventSignal(roads.AutobahnRow{Kind: "roadworks", Title: str("A7 | Kassel - Göttingen"), Blocked: true})}, nil, roads.LampRed, []roads.ReasonKey{"closure"}},
	{327, []roads.Signal{roads.AutobahnEventSignal(roads.AutobahnRow{Kind: "roadworks", Title: str("A7 | Kassel - Göttingen"), Subtitle: str("AS Kassel-Nord (aus Richtung Hannover)"), Blocked: true})}, nil, roads.LampAmber, []roads.ReasonKey{roads.KeyRampClosure}},
}

type wordCase struct {
	at    int
	texts []*string
	word  roads.SafetyWord
}

// wordCases are safetyWordOf's cases (ts:159-183).
var wordCases = []wordCase{
	{161, strs("Gefahr durch einen Falschfahrer"), roads.WordWrongWay},
	{162, strs("Achtung, Geisterfahrer zwischen AS A und AS B"), roads.WordWrongWay},
	{163, strs("Gefahr durch Gegenstände auf der Fahrbahn"), roads.WordObject},
	{164, strs("verlorene Ladung"), roads.WordObject},
	{165, strs("Personen auf der Fahrbahn"), roads.WordPerson},
	{166, strs("Gefahr durch Tiere auf der Fahrbahn"), roads.WordAnimal},
	{167, strs("Unfall", "Vollsperrung"), roads.WordAccidentClosure},
	{168, strs("Ereignismeldung:", "- Unfall", "- Fahrbahn blockiert"), roads.WordAccidentClosure},
	{169, strs("Richtungsfahrbahn gesperrt nach Unfall"), roads.WordAccidentClosure},
	{174, strs("Ereignismeldung:", "- Unfall", "- rechter Fahrstreifen gesperrt"), roads.WordNone},
	{175, strs("Unfall"), roads.WordNone},
	{176, strs("Ereignismeldung:", "- Stockender Verkehr", "- Reisezeitverlust: 5 Minuten"), roads.WordNone},
	{177, []*string{nil, nil}, roads.WordNone},
	{182, strs("Falschfahrer", "Gegenstände auf der Fahrbahn"), roads.WordWrongWay},
}

type scopeCase struct {
	at          int
	description []*string
	subtitle    *string
	scope       roads.ClosureScope
}

// closureCases are closureScopeOf's cases (ts:186-202). ts:201 passes
// [7, null, {a: 1}]: entries that are not strings, skipped; the port takes
// absent entries (nil) in their place.
var closureCases = []scopeCase{
	{188, strs("Zeitraum dieser Bauphase:", "", "Auffahrt auf die A9: AS Weißenfels (aus Richtung Pörstental)"), str(" AS Weißenfels (aus Richtung Pörstental)"), roads.ScopeRamp},
	{189, strs("Abfahrt von der A92: AK Neufahrn (aus Richtung Unterschleißheim)"), str(""), roads.ScopeRamp},
	{190, strs("Auffahrt auf die B91"), str(""), roads.ScopeRamp},
	{191, strs("Von Auffahrt auf die A9: AS Klein Marzehns (aus Richtung Rosselquelle) nach A9: Halle/Leipzig -> Berlin"), str(""), roads.ScopeRamp},
	{194, strs("Beginn: 05.07.26", "A45: Hagen -> Gießen, zwischen 3.7 km hinter Brachtsiepen und 1.5 km vor AS Drolshagen"), str(" Hagen -> Gießen"), roads.ScopeCarriageway},
	{195, strs("Von A9: Halle/Leipzig -> Nürnberg, zwischen 3.1 km hinter AS Bad Berneck/Himmelkron und AD Bayreuth/Kulmbach nach Abfahrt von der A9"), str(" Halle/Leipzig -> Nürnberg"), roads.ScopeCarriageway},
	{198, nil, str(" AK Köln-Gremberg (aus Richtung Köln-Poll)"), roads.ScopeRamp},
	{199, []*string{}, str(" Flensburg -> Füssen"), roads.ScopeCarriageway},
	{200, nil, nil, roads.ScopeCarriageway},
	{201, []*string{nil, nil, nil}, nil, roads.ScopeCarriageway},
}

// messageScopeCases are messageScopeOf's cases (ts:228-232).
var messageScopeCases = []scopeCase{
	{229, strs("Auffahrt gesperrt", "Vollsperrung"), nil, roads.ScopeCarriageway},
	{230, strs("gesperrt ist die Ausfahrt Seevetal"), nil, roads.ScopeRamp},
	{231, []*string{nil, nil}, nil, roads.ScopeCarriageway},
}

type messageCase struct {
	at   int
	in   roads.AmpelMessage
	want roads.Signal
	// lamp is checked when set: the case asserts the lamp, not the signal.
	lamp *roads.Lamp
}

func lampOf(l roads.Lamp) *roads.Lamp { return &l }

// none marks a case whose signal gives no colour.
var none = lampOf("")

// messageCases are messageSignal's cases (ts:205-227).
var messageCases = []messageCase{
	{208, roads.AmpelMessage{Kind: "warning", What: "Gefahr", DetailText: str("Falschfahrer zwischen AS Soltau-Ost und AS Bispingen")}, roads.Signal{Kind: roads.SignalSafety, Word: roads.WordWrongWay}, nil},
	{209, roads.AmpelMessage{Kind: "accident", What: "Unfall", DetailText: str("Unfall, Fahrbahn voll gesperrt")}, roads.Signal{Kind: roads.SignalSafety, Word: roads.WordAccidentClosure}, nil},
	{210, roads.AmpelMessage{Kind: "other", What: "Gegenstände auf der Fahrbahn"}, roads.Signal{}, lampOf(roads.LampRed)},
	{213, roads.AmpelMessage{Kind: "accident", What: "Unfall", DetailText: str("rechter Fahrstreifen gesperrt")}, roads.Signal{Kind: roads.SignalWarning}, nil},
	{214, roads.AmpelMessage{Kind: "warning", What: "Glätte"}, roads.Signal{Kind: roads.SignalWarning}, nil},
	{215, roads.AmpelMessage{Kind: "other", What: "Hinweis"}, roads.Signal{Kind: roads.SignalWarning}, nil},
	{218, roads.AmpelMessage{Kind: "closure", What: "A7 Hamburg Richtung Hannover", DetailText: str("zwischen AS Soltau-Ost und AS Bispingen Vollsperrung")}, roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeCarriageway}, nil},
	{219, roads.AmpelMessage{Kind: "closure", What: "A7", DetailText: str("Anschlussstelle Soltau-Ost: Auffahrt gesperrt")}, roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeRamp}, nil},
	{220, roads.AmpelMessage{Kind: "closure", What: "A7", DetailText: str("gesperrt")}, roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeCarriageway}, nil},
	{223, roads.AmpelMessage{Kind: "jam", What: "Stau", DelayS: secs(roads.JamRedS)}, roads.Signal{}, lampOf(roads.LampRed)},
	{224, roads.AmpelMessage{Kind: "jam", What: "Stau"}, roads.Signal{}, lampOf(roads.LampAmber)},
	{225, roads.AmpelMessage{Kind: "restriction", What: "Fahrbahnverengung"}, roads.Signal{Kind: roads.SignalRoadworks, Scope: roads.ScopeCarriageway}, nil},
	{226, roads.AmpelMessage{Kind: "roadworks", What: "Baustelle", DetailText: str("Ausfahrt gesperrt")}, roads.Signal{}, none},
}

type rowCase struct {
	at   int
	in   roads.AutobahnRow
	want roads.Signal
}

// row is the platform's r() (ts:318): a warning titled "A7 | Kassel -
// Göttingen" with no traffic type, delay, subtitle or description.
func row(f func(*roads.AutobahnRow)) roads.AutobahnRow {
	r := roads.AutobahnRow{Kind: "warning", Title: str("A7 | Kassel - Göttingen")}
	f(&r)
	return r
}

// rowCases are autobahnEventSignal's cases (ts:319-331). isBlocked "true" /
// "false" is the port's Blocked; the delay "1800" is the number the port
// takes.
var rowCases = []rowCase{
	{320, row(func(r *roads.AutobahnRow) { r.Kind, r.Description = "closure", strs("Auffahrt auf die A7: gesperrt") }), roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeRamp}},
	{321, row(func(r *roads.AutobahnRow) { r.Kind = "roadworks" }), roads.Signal{Kind: roads.SignalRoadworks, Scope: roads.ScopeCarriageway}},
	{323, row(func(r *roads.AutobahnRow) { r.Kind, r.Blocked = "roadworks", true }), roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeCarriageway}},
	{324, row(func(r *roads.AutobahnRow) {
		r.Kind, r.Blocked, r.Subtitle = "roadworks", true, str("AS Kassel-Nord (aus Richtung Hannover)")
	}), roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeRamp}},
	{325, row(func(r *roads.AutobahnRow) { r.Kind = "roadworks" }), roads.Signal{Kind: roads.SignalRoadworks, Scope: roads.ScopeCarriageway}},
	{328, row(func(r *roads.AutobahnRow) { r.AbnormalTrafficType, r.DelayS = str("STATIONARY_TRAFFIC"), secs(1800) }), roads.Signal{Kind: roads.SignalJam, DelayS: secs(1800)}},
	{329, row(func(r *roads.AutobahnRow) { r.Description = strs("Tiere auf der Fahrbahn") }), roads.Signal{Kind: roads.SignalSafety, Word: roads.WordAnimal}},
	{330, row(func(r *roads.AutobahnRow) { r.Description = strs("Rechter Fahrstreifen gesperrt") }), roads.Signal{Kind: roads.SignalWarning}},
}

type sightCase struct {
	at       int
	readOK   bool
	warnings int
	// source is the sight of the warnings source; the platform passes its
	// feed check (ts:334: checked s seconds ago at a cadence of 300 s,
	// fresh inside three cadences): ago(10) and ago(900) are fresh,
	// ago(901) is stale, no check is missing.
	source roads.Sight
	want   roads.Sight
}

// sightCases are dwdSightOf's cases (ts:332-340).
var sightCases = []sightCase{
	{335, false, 3, roads.SightFresh, roads.SightMissing},
	{336, true, 1, roads.SightMissing, roads.SightFresh},
	{337, true, 0, roads.SightFresh, roads.SightFresh},
	{338, true, 0, roads.SightStale, roads.SightMissing},
	{339, true, 0, roads.SightMissing, roads.SightMissing},
}

func sameSignal(a, b roads.Signal) bool {
	return a.Kind == b.Kind && a.Scope == b.Scope && a.Word == b.Word && a.Level == b.Level &&
		(a.DelayS == nil) == (b.DelayS == nil) && (a.DelayS == nil || *a.DelayS == *b.DelayS)
}

func keys(a roads.Ampel) []roads.ReasonKey {
	var out []roads.ReasonKey
	for _, r := range a.Reasons {
		out = append(out, roads.ReasonKeyOf(r))
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
		lamp, ok := roads.SignalLamp(c.in)
		if lamp != c.lamp || ok != c.ok {
			t.Errorf("%s: signalLamp(%+v) = %q %v, want %q %v", at(c.at), c.in, lamp, ok, c.lamp, c.ok)
		}
		if c.ok {
			if a := roads.AmpelOf([]roads.Signal{c.in}, fresh); a.Lamp != c.lamp {
				t.Errorf("%s: ampelOf lamp %q, want %q", at(c.at), a.Lamp, c.lamp)
			}
		}
	}
	for _, c := range ampelCases {
		a := roads.AmpelOf(c.signals, c.sight)
		if a.Lamp != c.lamp || !slices.Equal(keys(a), c.reasons) {
			t.Errorf("%s: ampelOf = %q %v, want %q %v", at(c.at), a.Lamp, keys(a), c.lamp, c.reasons)
		}
	}
	for _, c := range wordCases {
		if got := roads.SafetyWordOf(c.texts); got != c.word {
			t.Errorf("%s: safetyWordOf = %q, want %q", at(c.at), got, c.word)
		}
	}
	for _, c := range closureCases {
		if got := roads.ClosureScopeOf(c.description, c.subtitle); got != c.scope {
			t.Errorf("%s: closureScopeOf = %q, want %q", at(c.at), got, c.scope)
		}
	}
	for _, c := range messageScopeCases {
		if got := roads.MessageScopeOf(c.description); got != c.scope {
			t.Errorf("%s: messageScopeOf = %q, want %q", at(c.at), got, c.scope)
		}
	}
	for _, c := range messageCases {
		got := roads.MessageSignal(c.in)
		if c.lamp != nil {
			if l, _ := roads.SignalLamp(got); l != *c.lamp {
				t.Errorf("%s: signalLamp(messageSignal) = %q, want %q", at(c.at), l, *c.lamp)
			}
			continue
		}
		if !sameSignal(got, c.want) {
			t.Errorf("%s: messageSignal = %+v, want %+v", at(c.at), got, c.want)
		}
	}
	for _, c := range rowCases {
		if got := roads.AutobahnEventSignal(c.in); !sameSignal(got, c.want) {
			t.Errorf("%s: autobahnEventSignal = %+v, want %+v", at(c.at), got, c.want)
		}
	}
	for _, c := range sightCases {
		if got := roads.DWDSightOf(c.readOK, c.warnings, c.source); got != c.want {
			t.Errorf("%s: dwdSightOf = %q, want %q", at(c.at), got, c.want)
		}
	}
	// ts:384: twelve ramp closures are twelve rampClosure reasons.
	var ramps []roads.Signal
	for range 12 {
		ramps = append(ramps, roads.Signal{Kind: roads.SignalClosure, Scope: roads.ScopeRamp})
	}
	if n := len(slices.DeleteFunc(keys(roads.AmpelOf(ramps, fresh)), func(k roads.ReasonKey) bool { return k != roads.KeyRampClosure })); n != 12 {
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
	in := []roads.Signal{
		{Kind: roads.SignalRoadworks, Scope: roads.ScopeCarriageway},
		{Kind: roads.SignalWarning, Word: "a"},
		{Kind: roads.SignalSlippery},
		{Kind: roads.SignalDWD, Level: 1},
		{Kind: roads.SignalJam},
		{Kind: roads.SignalJam, DelayS: secs(60)},
		{Kind: roads.SignalDWD, Level: 3},
		{Kind: roads.SignalJam, DelayS: secs(600)},
		{Kind: roads.SignalJam, DelayS: secs(60)},
		{Kind: roads.SignalBridgeWind},
		{Kind: roads.SignalSafety},
		{Kind: roads.SignalWarning, Word: "b"},
		{Kind: roads.SignalClosure},
	}
	roads.SortSignals(in)
	var got []string
	for _, s := range in {
		d := "-"
		if s.DelayS != nil {
			d = fmt.Sprint(*s.DelayS)
		}
		got = append(got, fmt.Sprintf("%s/%s/%d/%s", s.Kind, d, s.Level, s.Word))
	}
	want := []string{"closure/-/0/", "safety/-/0/", "bridgeWind/-/0/", "jam/600/0/", "jam/60/0/", "jam/60/0/", "jam/-/0/",
		"dwd/-/3/", "dwd/-/1/", "slippery/-/0/", "warning/-/0/a", "warning/-/0/b", "roadworks/-/0/"}
	if !slices.Equal(got, want) {
		t.Errorf("order\n got %v\nwant %v", got, want)
	}
}

// TestAnUnknownSignalGivesNoColour: a kind outside the table gives none.
func TestAnUnknownSignalGivesNoColour(t *testing.T) {
	if l, ok := roads.SignalLamp(roads.Signal{Kind: "x"}); ok || l != "" {
		t.Errorf("unknown kind: %q %v", l, ok)
	}
}

// TestTheFoldIsJavaScripts: white space JavaScript's \s matches and Go's
// does not (a no-break space, a vertical tab) still separates the words,
// and the trim of a location line removes it.
func TestTheFoldIsJavaScripts(t *testing.T) {
	if w := roads.SafetyWordOf(strs("Tiere\u00a0auf\vder Fahrbahn")); w != roads.WordAnimal {
		t.Errorf("no-break space: %q", w)
	}
	if s := roads.ClosureScopeOf(strs("\u00a0\ufeffAuffahrt auf die A9"), nil); s != roads.ScopeRamp {
		t.Errorf("trimmed line: %q", s)
	}
	if w := roads.SafetyWordOf(strs("\u0130N FALSCHER FAHRTRICHTUNG")); w != roads.WordNone {
		t.Errorf("dotted capital I lower-cases to i and a combining dot, as in JavaScript: %q", w)
	}
}
