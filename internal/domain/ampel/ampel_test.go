package ampel_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The cases below are transcribed from the platform's own test of the rule
// this package ports, the test file of its motorway Ampel module (cited as
// ts:LINE), and driven through the port. Where the platform's
// function takes a value Go cannot type (a non-string description entry, a
// delay as text, isBlocked as text), the case carries the value the port
// takes in its place, and says so.

const tsFile = "ts"

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
var fresh = []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightFresh}, {Group: ampel.GroupWeather, State: ampel.SightFresh}}

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
	{55, ampel.Signal{Kind: ampel.SignalWeather, Level: 3}, ampel.LampRed, true},
	{56, ampel.Signal{Kind: ampel.SignalWeather, Level: 4}, ampel.LampRed, true},
	{57, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(1800)}, ampel.LampRed, true},
	{58, ampel.Signal{Kind: ampel.SignalBridgeWind}, ampel.LampRed, true},
	{65, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(1799)}, ampel.LampAmber, true},
	{66, ampel.Signal{Kind: ampel.SignalJam}, ampel.LampAmber, true},
	{67, ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}, ampel.LampAmber, true},
	{68, ampel.Signal{Kind: ampel.SignalWarning}, ampel.LampAmber, true},
	{69, ampel.Signal{Kind: ampel.SignalWeather, Level: 1}, ampel.LampAmber, true},
	{70, ampel.Signal{Kind: ampel.SignalWeather, Level: 2}, ampel.LampAmber, true},
	{71, ampel.Signal{Kind: ampel.SignalSlippery}, ampel.LampAmber, true},
	{72, ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}, ampel.LampAmber, true},
	{79, ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeRamp}, "", false},
	{80, ampel.Signal{Kind: ampel.SignalWeather, Level: 0}, "", false},
	{87, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(29 * 60)}, ampel.LampAmber, true},
	{88, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(30 * 60)}, ampel.LampRed, true},
	{89, ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(30*60 - 1)}, ampel.LampAmber, true},
	{92, ampel.Signal{Kind: ampel.SignalWeather, Level: 2}, ampel.LampAmber, true},
	{93, ampel.Signal{Kind: ampel.SignalWeather, Level: 3}, ampel.LampRed, true},
}

type ampelCase struct {
	at      int
	signals []ampel.Signal
	sight   []ampel.SightEntry
	lamp    ampel.Lamp
	// reasons are the keys of the reasons, in order.
	reasons []ampel.ReasonKey
	// sights are the entries of the sight reasons, in order, where the
	// platform asserts them whole.
	sights []ampel.SightEntry
}

// ampelCases are ampelOf's cases (ts:81, 95-147, 307-313, 326-327).
var ampelCases = []ampelCase{
	{81, []ampel.Signal{{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeRamp}, {Kind: ampel.SignalWeather, Level: 0}}, fresh, ampel.LampGreen, nil, nil},
	{96, nil, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightStale}, {Group: ampel.GroupWeather, State: ampel.SightFresh}}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightStale}}},
	{99, nil, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightFresh}, {Group: ampel.GroupWeather, State: ampel.SightMissing}}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}, []ampel.SightEntry{{Group: ampel.GroupWeather, State: ampel.SightMissing}}},
	{107, []ampel.Signal{{Kind: ampel.SignalJam, DelayS: secs(600)}, {Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}}, fresh, ampel.LampRed, []ampel.ReasonKey{"closure", "jam"}, nil},
	{118, []ampel.Signal{{Kind: ampel.SignalSafety, Word: ampel.WordWrongWay}}, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightStale}}, ampel.LampRed, []ampel.ReasonKey{"safety", ampel.KeySight}, nil},
	{124, []ampel.Signal{{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}, {Kind: ampel.SignalWarning}, {Kind: ampel.SignalJam}}, fresh, ampel.LampAmber, []ampel.ReasonKey{"roadworks", "warning", "jam"}, nil},
	{138, nil, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightStale}, {Group: ampel.GroupWeather, State: ampel.SightFresh}}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}, nil},
	{141, nil, fresh, ampel.LampGreen, nil, nil},
	{144, nil, nil, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightMissing}}},
	{307, nil, append([]ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightFresh}}, ampel.MessageSightOf([]string{"08"}, coverage(), sightBy(fr, st, mi))...), ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}, nil},
	{310, nil, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightFresh}, entry(mi, "03")}, ampel.LampAmber, []ampel.ReasonKey{ampel.KeySight}, []ampel.SightEntry{entry(mi, "03")}},
	{313, nil, []ampel.SightEntry{{Group: ampel.GroupOperator, State: ampel.SightFresh}, entry(fr, "08")}, ampel.LampGreen, nil, nil},
	{326, []ampel.Signal{ampel.OperatorEventSignal(ampel.OperatorEvent{Kind: "roadworks", Title: str("A7 | Kassel - Göttingen"), Blocked: true})}, nil, ampel.LampRed, []ampel.ReasonKey{"closure"}, nil},
	{327, []ampel.Signal{ampel.OperatorEventSignal(ampel.OperatorEvent{Kind: "roadworks", Title: str("A7 | Kassel - Göttingen"), Subtitle: str("AS Kassel-Nord (aus Richtung Hannover)"), Blocked: true})}, nil, ampel.LampAmber, []ampel.ReasonKey{ampel.KeyRampClosure}, nil},
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
	in   ampel.OperatorEvent
	want ampel.Signal
}

// row is the platform's r() (ts:318): a warning titled "A7 | Kassel -
// Göttingen" with no traffic type, delay, subtitle or description.
func row(f func(*ampel.OperatorEvent)) ampel.OperatorEvent {
	r := ampel.OperatorEvent{Kind: "warning", Title: str("A7 | Kassel - Göttingen")}
	f(&r)
	return r
}

// rowCases are autobahnEventSignal's cases (ts:319-331). isBlocked "true" /
// "false" is the port's Blocked; the delay "1800" is the number the port
// takes.
var rowCases = []rowCase{
	{320, row(func(r *ampel.OperatorEvent) { r.Kind, r.Description = "closure", strs("Auffahrt auf die A7: gesperrt") }), ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}},
	{321, row(func(r *ampel.OperatorEvent) { r.Kind = "roadworks" }), ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}},
	{323, row(func(r *ampel.OperatorEvent) { r.Kind, r.Blocked = "roadworks", true }), ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}},
	{324, row(func(r *ampel.OperatorEvent) {
		r.Kind, r.Blocked, r.Subtitle = "roadworks", true, str("AS Kassel-Nord (aus Richtung Hannover)")
	}), ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}},
	{325, row(func(r *ampel.OperatorEvent) { r.Kind = "roadworks" }), ampel.Signal{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway}},
	{328, row(func(r *ampel.OperatorEvent) { r.TrafficType, r.DelayS = str("STATIONARY_TRAFFIC"), secs(1800) }), ampel.Signal{Kind: ampel.SignalJam, DelayS: secs(1800)}},
	{329, row(func(r *ampel.OperatorEvent) { r.Description = strs("Tiere auf der Fahrbahn") }), ampel.Signal{Kind: ampel.SignalSafety, Word: ampel.WordAnimal}},
	{330, row(func(r *ampel.OperatorEvent) { r.Description = strs("Rechter Fahrstreifen gesperrt") }), ampel.Signal{Kind: ampel.SignalWarning}},
}

type sightCase struct {
	at       int
	readOK   bool
	warnings int
	// confirmation is the warnings source's newest confirmation: the
	// platform's ago(s), confirmed s seconds before now at an interval of
	// 300 s; nil when there is none.
	confirmation *ampel.Confirmation
	want         ampel.Sight
}

// sightNow is the instant of the platform's weather-sight cases (ts:333).
var sightNow = time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)

// ago is the platform's ago(s) (ts:334).
func ago(s int) *ampel.Confirmation {
	at := sightNow.Add(-time.Duration(s) * time.Second)
	return &ampel.Confirmation{At: &at, IntervalS: secs(300)}
}

// sightCases are the weather warnings' sight cases (ts:332-340).
var sightCases = []sightCase{
	{335, false, 3, ago(10), ampel.SightMissing},
	{336, true, 1, nil, ampel.SightFresh},
	{337, true, 0, ago(900), ampel.SightFresh},
	{338, true, 0, ago(901), ampel.SightMissing},
	{339, true, 0, nil, ampel.SightMissing},
}

type thresholdCase struct {
	at        int
	got, want int
}

// thresholdCases are the thresholds (ts:40-43).
var thresholdCases = []thresholdCase{
	{40, ampel.JamRedS, 1800},
	{41, ampel.WarningRedLevel, 3},
	{42, ampel.WarningAmberLevel, 1},
	{43, ampel.OnRoadM, 2000},
}

type bridgeCase struct {
	at    int
	text  *string
	state ampel.BridgeState
}

// bridgeCases are bridgeStateOf's cases (ts:237-250); null is BridgeNone.
var bridgeCases = []bridgeCase{
	{237, str("Brücke gesperrt"), ampel.BridgeClosed},
	{238, str("Sperrung für alle Fahrzeuge"), ampel.BridgeClosed},
	{242, str("Windwarnung 100 km/h"), ampel.BridgeRestricted},
	{244, str("Sperrung aufgehoben"), ampel.BridgeNone},
	{245, str("nicht gesperrt"), ampel.BridgeNone},
	{248, str("undefiniert"), ampel.BridgeNone},
	{249, str("  "), ampel.BridgeNone},
	{250, nil, ampel.BridgeNone},
}

type bridgeSignalCase struct {
	at    int
	state ampel.BridgeState
	want  ampel.Signal
	ok    bool
}

// bridgeSignalCases are bridgeSignal's cases (ts:239, 243, 251).
var bridgeSignalCases = []bridgeSignalCase{
	{239, ampel.BridgeClosed, ampel.Signal{Kind: ampel.SignalBridgeWind}, true},
	{243, ampel.BridgeStateOf(str("Windwarnung 100 km/h")), ampel.Signal{Kind: ampel.SignalWarning}, true},
	{251, ampel.BridgeNone, ampel.Signal{}, false},
}

// confirmNow is the instant of the platform's per-state cases (ts:256).
var confirmNow = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

func confirmedAgo(ms int) *ampel.Confirmation {
	at := confirmNow.Add(-time.Duration(ms) * time.Millisecond)
	return &ampel.Confirmation{At: &at, IntervalS: secs(600)}
}

type confirmedCase struct {
	at   int
	in   *ampel.Confirmation
	want ampel.Sight
}

// confirmedCases are the sight of a source from its newest confirmation
// (ts:258-261). ts:262 passes a time that is not one, which Go cannot type;
// the port reads it as ts:260 does.
var confirmedCases = []confirmedCase{
	{258, confirmedAgo(1_799_000), ampel.SightFresh},
	{259, confirmedAgo(1_801_000), ampel.SightStale},
	{260, &ampel.Confirmation{IntervalS: secs(600)}, ampel.SightMissing},
	{261, nil, ampel.SightMissing},
}

// The platform's planted table (ts:265-275), in the service's words: three
// states with an own message source each, one of them answered only while
// current, and a source measured to carry the states' messages.
const (
	ownSH     = "own-01"
	ownBW     = "own-08"
	ownBerlin = "own-11"
	carrier   = "carrier"
)

// coverage is the platform's per-state table with the carrier's coverage
// limited to lands (none: all sixteen, the platform's measured list).
func coverage(lands ...string) map[string]ampel.Coverage {
	if lands == nil {
		for i := 1; i <= 16; i++ {
			lands = append(lands, fmt.Sprintf("%02d", i))
		}
	}
	own := map[string]string{"01": ownSH, "08": ownBW, "11": ownBerlin}
	out := map[string]ampel.Coverage{}
	for l, o := range own {
		c := ampel.Coverage{Own: []string{o}}
		if l == "08" {
			c.CurrentOnly = []string{ownBW}
		}
		out[l] = c
	}
	for _, l := range lands {
		c := out[l]
		c.Carried = []string{carrier}
		out[l] = c
	}
	return out
}

// sightBy is a source's sight: the carrier's, the BW source's, and every
// other source's.
func sightBy(carried, bw, other ampel.Sight) func(string) ampel.Sight {
	return func(k string) ampel.Sight {
		switch k {
		case carrier:
			return carried
		case ownBW:
			return bw
		}
		return other
	}
}

type coverageCase struct {
	at       int
	lands    []string
	coverage map[string]ampel.Coverage
	sightOf  func(string) ampel.Sight
	want     []ampel.SightEntry
}

func entry(state ampel.Sight, land string) ampel.SightEntry {
	return ampel.SightEntry{Group: ampel.GroupMessages, State: state, Land: land}
}

const (
	fr = ampel.SightFresh
	st = ampel.SightStale
	mi = ampel.SightMissing
)

// coverageCases are the traffic messages' per-state sight (ts:278-306).
var coverageCases = []coverageCase{
	{278, []string{"03", "16", "11"}, coverage(), sightBy(fr, mi, mi), []ampel.SightEntry{entry(fr, "03"), entry(fr, "16"), entry(fr, "11")}},
	{284, []string{"16"}, coverage("03"), sightBy(fr, mi, mi), []ampel.SightEntry{entry(mi, "16")}},
	{288, []string{"08", "03", "03"}, coverage("03"), sightBy(fr, st, mi), []ampel.SightEntry{entry(st, "08"), entry(fr, "03")}},
	{294, []string{"11"}, coverage(), sightBy(fr, st, st), []ampel.SightEntry{entry(fr, "11")}},
	{301, []string{"08"}, coverage("08"), sightBy(fr, st, mi), []ampel.SightEntry{entry(st, "08")}},
	{304, []string{"08"}, coverage(), sightBy(fr, mi, mi), []ampel.SightEntry{entry(mi, "08")}},
	{306, []string{"08"}, coverage(), sightBy(fr, fr, fr), []ampel.SightEntry{entry(fr, "08")}},
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
		len(messageCases) + len(rowCases) + len(sightCases) + len(thresholdCases) + len(bridgeCases) +
		len(bridgeSignalCases) + len(confirmedCases) + len(coverageCases) + 1
}

func sights(a ampel.Ampel) []ampel.SightEntry {
	var out []ampel.SightEntry
	for _, r := range a.Reasons {
		if r.Sight != nil {
			out = append(out, *r.Sight)
		}
	}
	return out
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
		if c.sights != nil && !slices.Equal(sights(a), c.sights) {
			t.Errorf("%s: ampelOf sight reasons = %v, want %v", at(c.at), sights(a), c.sights)
		}
	}
	for _, c := range thresholdCases {
		if c.got != c.want {
			t.Errorf("%s: threshold %d, want %d", at(c.at), c.got, c.want)
		}
	}
	for _, c := range bridgeCases {
		if got := ampel.BridgeStateOf(c.text); got != c.state {
			t.Errorf("%s: bridgeStateOf = %q, want %q", at(c.at), got, c.state)
		}
	}
	for _, c := range bridgeSignalCases {
		got, ok := ampel.BridgeSignal(c.state)
		if ok != c.ok || !sameSignal(got, c.want) {
			t.Errorf("%s: bridgeSignal = %+v %v, want %+v %v", at(c.at), got, ok, c.want, c.ok)
		}
		if l, _ := ampel.SignalLamp(got); c.at == 239 && l != ampel.LampRed {
			t.Errorf("%s: a closed bridge is %q, want red", at(c.at), l)
		}
	}
	for _, c := range confirmedCases {
		if got := ampel.ConfirmedSight(c.in, confirmNow); got != c.want {
			t.Errorf("%s: sight of a confirmation = %q, want %q", at(c.at), got, c.want)
		}
	}
	for _, c := range coverageCases {
		if got := ampel.MessageSightOf(c.lands, c.coverage, c.sightOf); !slices.Equal(got, c.want) {
			t.Errorf("%s: per-state sight = %v, want %v", at(c.at), got, c.want)
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
		if got := ampel.OperatorEventSignal(c.in); !sameSignal(got, c.want) {
			t.Errorf("%s: autobahnEventSignal = %+v, want %+v", at(c.at), got, c.want)
		}
	}
	for _, c := range sightCases {
		if got := ampel.WarningSightOf(c.readOK, c.warnings, c.confirmation, sightNow); got != c.want {
			t.Errorf("%s: weather sight = %q, want %q", at(c.at), got, c.want)
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
	if n := transcribedCases(); n != 118 {
		t.Errorf("%d transcribed cases, want 118", n)
	}
}

// TestTheSortIsThePlatformsOrder: ORDER of the platform's tool (toolAmpel),
// jams by delay descending with no delay last, weather warnings by level
// descending, every other tie in the caller's order.
func TestTheSortIsThePlatformsOrder(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	in := []ampel.Signal{
		{Kind: ampel.SignalRoadworks, Scope: ampel.ScopeCarriageway},
		{Kind: ampel.SignalWarning, Word: "a"},
		{Kind: ampel.SignalSlippery},
		{Kind: ampel.SignalWeather, Level: 1},
		{Kind: ampel.SignalJam},
		{Kind: ampel.SignalJam, DelayS: secs(0)},
		{Kind: ampel.SignalJam, DelayS: secs(60)},
		{Kind: ampel.SignalWeather, Level: 3},
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
		"weather/-/3/", "weather/-/1/", "slippery/-/0/", "warning/-/0/a", "warning/-/0/b", "roadworks/-/0/"}
	if !slices.Equal(got, want) {
		t.Errorf("order\n got %v\nwant %v", got, want)
	}
}

// TestTheMessageSightReadsTheConfirmations: no state in scope is one missing
// entry; a source without a confirmation is missing; a confirmed source is
// judged by its confirmation.
func TestTheMessageSightReadsTheConfirmations(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	if got := ampel.MessageSight(nil, coverage(), nil, confirmNow); !slices.Equal(got, []ampel.SightEntry{{Group: ampel.GroupMessages, State: mi}}) {
		t.Errorf("no state: %v", got)
	}
	confirmations := map[string]ampel.Confirmation{carrier: *confirmedAgo(1_000), ownBW: *confirmedAgo(1_801_000)}
	got := ampel.MessageSight([]string{"03", "08", "11"}, coverage("03", "08"), confirmations, confirmNow)
	if want := []ampel.SightEntry{entry(fr, "03"), entry(st, "08"), entry(mi, "11")}; !slices.Equal(got, want) {
		t.Errorf("by confirmation: %v, want %v", got, want)
	}
}

// TestTheAliveRule: paused updates keep a confirmed source alive however
// old; a confirmation without an interval takes the default; with no
// default it is never inside; one inside the window, inclusive, is alive.
func TestTheAliveRule(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	old := confirmNow.Add(-24 * time.Hour)
	if !ampel.Alive(&ampel.Confirmation{At: &old, Paused: true}, confirmNow, 3, nil) {
		t.Error("paused and confirmed: not alive")
	}
	if ampel.Alive(&ampel.Confirmation{Paused: true}, confirmNow, 3, nil) {
		t.Error("paused and never confirmed: alive")
	}
	edge := confirmNow.Add(-1800 * time.Second)
	if !ampel.Alive(&ampel.Confirmation{At: &edge}, confirmNow, 3, secs(600)) {
		t.Error("no interval, at the default's edge: not alive")
	}
	if ampel.Alive(&ampel.Confirmation{At: &edge}, confirmNow, 3, nil) {
		t.Error("no interval and no default: alive")
	}
	if got := ampel.ConfirmedSight(&ampel.Confirmation{At: &edge}, confirmNow); got != fr {
		t.Errorf("no interval, 1800 s old: %q, want fresh (600 s default)", got)
	}
	if got := ampel.ConfirmedSight(&ampel.Confirmation{At: &old, Paused: true}, confirmNow); got != fr {
		t.Errorf("paused: %q, want fresh", got)
	}
}

// TestAnAstralRuneCountsAsTwo: the platform's bounded repetitions count
// UTF-16 code units, so a rune above U+FFFF takes two of the forty a
// closed ramp may lie after "gesperrt".
func TestAnAstralRuneCountsAsTwo(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	within := "gesperrt" + strings.Repeat("a", 38) + "\U0001F6A7" + "rampe"
	beyond := "gesperrt" + strings.Repeat("a", 39) + "\U0001F6A7" + "rampe"
	if s := ampel.MessageScopeOf(strs(within)); s != ampel.ScopeRamp {
		t.Errorf("forty code units: %q, want ramp", s)
	}
	if s := ampel.MessageScopeOf(strs(beyond)); s != ampel.ScopeCarriageway {
		t.Errorf("forty-one code units (forty runes): %q, want carriageway", s)
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
