package roads_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestStoredCodesMapToTheDomain(t *testing.T) {
	req.Covers(t, "FR-ROAD-002", "ENV-051")
	for raw, want := range map[string]roads.Kind{
		"closure": roads.KindClosure, "roadworks": roads.KindRoadworks, "jam": roads.KindJam, "accident": roads.KindAccident,
		"hazard": roads.KindHazard, "restriction": roads.KindRestriction, "weather": roads.KindWeather,
		"other": roads.KindOther, "x_unmapped": roads.KindOther, "": roads.KindOther,
	} {
		if got := roads.MapKind(raw); got != want {
			t.Errorf("kind %q -> %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]roads.Severity{
		"info": roads.SeverityInfo, "minor": roads.SeverityMinor, "lane_closed": roads.SeverityLaneClosed,
		"major": roads.SeverityMajor, "road_closed": roads.SeverityRoadClosed, "x_unmapped": roads.SeverityOther,
	} {
		if got := roads.MapSeverity(raw); got != want {
			t.Errorf("severity %q -> %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]roads.Direction{
		"north": roads.DirectionNorth, "east": roads.DirectionEast, "south": roads.DirectionSouth, "west": roads.DirectionWest,
		"both": roads.DirectionBoth, "unknown": roads.DirectionUnknown, "x_unmapped": roads.DirectionUnknown,
	} {
		if got := roads.MapDirection(raw); got != want {
			t.Errorf("direction %q -> %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]roads.Class{
		"motorway": roads.ClassMotorway, "federal": roads.ClassFederal, "state": roads.ClassState,
		"urban": roads.ClassUrban, "x_unmapped": roads.ClassOther,
	} {
		if got := roads.MapClass(raw); got != want {
			t.Errorf("class %q -> %q, want %q", raw, got, want)
		}
	}
	if len(roads.Kinds) != 8 || len(roads.Severities) != 6 || len(roads.Directions) != 6 || len(roads.Classes) != 5 || len(roads.Lamps) != 3 {
		t.Error("an enumeration lost or gained a value")
	}
}

func TestSeveritiesAreOrdered(t *testing.T) {
	req.Covers(t, "FR-ROAD-002")
	want := []roads.Severity{roads.SeverityInfo, roads.SeverityMinor, roads.SeverityLaneClosed, roads.SeverityMajor, roads.SeverityRoadClosed}
	got := roads.Ordered()
	if !slices.Equal(got, want) {
		t.Fatalf("ordered %v", got)
	}
	got[0] = "changed"
	if roads.Ordered()[0] != roads.SeverityInfo {
		t.Error("Ordered hands out the package's own list")
	}
	for i, s := range want {
		if s.Rank() != i+1 {
			t.Errorf("%s rank %d, want %d", s, s.Rank(), i+1)
		}
	}
	if roads.SeverityOther.Rank() != 0 || roads.SeverityInfo.Floor() != 0 || roads.SeverityMinor.Floor() != 2 || roads.SeverityRoadClosed.Floor() != 5 {
		t.Error("the rank of other or a floor is wrong")
	}
	if c := roads.Compass(); !slices.Equal(c, []roads.Direction{roads.DirectionNorth, roads.DirectionEast, roads.DirectionSouth, roads.DirectionWest}) {
		t.Errorf("compass %v", c)
	} else if c[0] = "changed"; roads.Compass()[0] != roads.DirectionNorth {
		t.Error("Compass hands out the package's own list")
	}
}

func TestRoadReferencesAreCanonical(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	for in, want := range map[string]string{"A8": "A8", "a8": "A8", "a 8": "A8", "A 100": "A100", "b27": "B27", "B 2700": "B2700", "A08": "A8", "a 008": "A8", "A0": "A0", "A000": "A0", "B 0270": "B270"} {
		got, err := roads.ParseRoad(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "A", "8", "C8", "A  8", "A12345", " A8", "A8 ", "A-8", "A8a"} {
		if _, err := roads.ParseRoad(in); !errors.Is(err, roads.ErrRoadReference) {
			t.Errorf("%q read without error", in)
		}
	}
	if roads.Canonical("a 101") != "A101" || roads.Canonical("") != "" || roads.Canonical("A") != "A" {
		t.Error("a stored reference is not shown canonically")
	}
}

func why(r roads.Reason) *roads.Reason { return &r }

func delay(m int) *int { return &m }

func msg(k roads.Kind, headline string) roads.Message {
	return roads.Message{Record: roads.RecordTrafficMessage, Kind: k, Headline: headline}
}

func text(s string) *string { return &s }

var allFresh = []ampel.SightEntry{{Group: roads.GroupRoads, State: ampel.SightFresh}, {Group: ampel.GroupWeather, State: ampel.SightFresh}}

// TestTheLampRule: the service's situations judged by the platform's rule
// (each record as the platform reads it, ampelOf with the sight of every
// group), first reason named.
func TestTheLampRule(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "ENV-051")
	jam := func(d *int) roads.Message { m := msg(roads.KindJam, "Stau"); m.DelayMin = d; return m }
	desc := func(m roads.Message, d string) roads.Message { m.Description = &d; return m }
	place := func(m roads.Message, p string) roads.Message { m.PlaceText = &p; return m }
	op := func(k roads.Kind, f func(*roads.Message)) roads.Message {
		m := roads.Message{Record: roads.RecordOperatorEvent, Kind: k, Headline: "A7 | Kassel - Göttingen"}
		f(&m)
		return m
	}
	bridge := func(state *string) roads.Message {
		return roads.Message{Record: roads.RecordBridgeState, Kind: roads.KindRestriction, Headline: "Brücke", Description: state}
	}
	stale := []ampel.SightEntry{{Group: roads.GroupRoads, State: ampel.SightStale}, {Group: ampel.GroupWeather, State: ampel.SightFresh}}
	partial := []ampel.SightEntry{{Group: roads.GroupRoads, State: ampel.SightFresh}, {Group: roads.GroupRoads, State: ampel.SightMissing}, {Group: ampel.GroupWeather, State: ampel.SightFresh}}
	unseen := []ampel.SightEntry{{Group: roads.GroupRoads, State: ampel.SightFresh}, {Group: ampel.GroupMessages, State: ampel.SightMissing, Land: "DE-NI"}}
	for name, c := range map[string]struct {
		in     []roads.Message
		levels []int
		sight  []ampel.SightEntry
		lamp   roads.Lamp
		reason *roads.Reason
	}{
		"nothing":                     {nil, nil, allFresh, roads.LampGreen, nil},
		"no sight at all":             {nil, nil, nil, roads.LampAmber, why(roads.ReasonLimitedSight)},
		"roadworks":                   {[]roads.Message{msg(roads.KindRoadworks, "Baustelle")}, nil, allFresh, roads.LampAmber, why(roads.ReasonNarrowing)},
		"restriction":                 {[]roads.Message{msg(roads.KindRestriction, "Fahrbahnverengung")}, nil, allFresh, roads.LampAmber, why(roads.ReasonNarrowing)},
		"works on a ramp give none":   {[]roads.Message{desc(msg(roads.KindRoadworks, "Baustelle"), "Ausfahrt gesperrt")}, nil, allFresh, roads.LampGreen, nil},
		"closure":                     {[]roads.Message{msg(roads.KindClosure, "A7 gesperrt")}, nil, allFresh, roads.LampRed, why(roads.ReasonRoadClosed)},
		"ramp closure":                {[]roads.Message{desc(msg(roads.KindClosure, "A7"), "Anschlussstelle Soltau-Ost: Auffahrt gesperrt")}, nil, allFresh, roads.LampAmber, why(roads.ReasonRampClosed)},
		"wrong-way on any kind":       {[]roads.Message{msg(roads.KindWeather, "Falschfahrer")}, nil, allFresh, roads.LampRed, why(roads.ReasonSafety)},
		"object in the description":   {[]roads.Message{desc(msg(roads.KindOther, "Gefahr"), "Gegenstände auf der Fahrbahn")}, nil, allFresh, roads.LampRed, why(roads.ReasonSafety)},
		"person in the place words":   {[]roads.Message{place(msg(roads.KindOther, "Gefahr"), "Personen auf der Fahrbahn")}, nil, allFresh, roads.LampRed, why(roads.ReasonSafety)},
		"accident with a closure":     {[]roads.Message{desc(msg(roads.KindAccident, "Unfall"), "Fahrbahn voll gesperrt")}, nil, allFresh, roads.LampRed, why(roads.ReasonSafety)},
		"accident with a closed lane": {[]roads.Message{desc(msg(roads.KindAccident, "Unfall"), "rechter Fahrstreifen gesperrt")}, nil, allFresh, roads.LampAmber, why(roads.ReasonWarning)},
		"hazard without words":        {[]roads.Message{msg(roads.KindHazard, "Gefahr")}, nil, allFresh, roads.LampAmber, why(roads.ReasonWarning)},
		"weather situation":           {[]roads.Message{msg(roads.KindWeather, "Glätte")}, nil, allFresh, roads.LampAmber, why(roads.ReasonWarning)},
		"unmapped kind":               {[]roads.Message{msg(roads.KindOther, "Hinweis")}, nil, allFresh, roads.LampAmber, why(roads.ReasonWarning)},
		"jam at the threshold":        {[]roads.Message{jam(delay(30))}, nil, allFresh, roads.LampRed, why(roads.ReasonLongJam)},
		"jam under the threshold":     {[]roads.Message{jam(delay(29))}, nil, allFresh, roads.LampAmber, why(roads.ReasonJam)},
		"jam without a delay":         {[]roads.Message{jam(nil)}, nil, allFresh, roads.LampAmber, why(roads.ReasonJam)},
		"seconds before minutes": {[]roads.Message{func() roads.Message { m := jam(delay(30)); m.DelayS = delay(1799); return m }()},
			nil, allFresh, roads.LampAmber, why(roads.ReasonJam)},
		"operator ramp by the location line": {[]roads.Message{op(roads.KindClosure, func(m *roads.Message) {
			m.Description = text("Zeitraum dieser Bauphase:\nAuffahrt auf die A9: AS Weißenfels")
		})}, nil, allFresh, roads.LampAmber, why(roads.ReasonRampClosed)},
		"operator road by the location line": {[]roads.Message{op(roads.KindClosure, func(m *roads.Message) {
			m.Description = text("A45: Hagen -> Gießen, zwischen AS A und AS B")
		})}, nil, allFresh, roads.LampRed, why(roads.ReasonRoadClosed)},
		"operator ramp by the subtitle": {[]roads.Message{op(roads.KindRoadworks, func(m *roads.Message) {
			m.Blocked, m.Subtitle = true, text("AS Kassel-Nord (aus Richtung Hannover)")
		})}, nil, allFresh, roads.LampAmber, why(roads.ReasonRampClosed)},
		"operator works marked blocked": {[]roads.Message{op(roads.KindRoadworks, func(m *roads.Message) { m.Blocked = true })},
			nil, allFresh, roads.LampRed, why(roads.ReasonRoadClosed)},
		"operator works": {[]roads.Message{op(roads.KindRoadworks, func(*roads.Message) {})}, nil, allFresh, roads.LampAmber, why(roads.ReasonNarrowing)},
		"operator jam by its traffic type": {[]roads.Message{op(roads.KindOther, func(m *roads.Message) {
			m.TrafficType, m.DelayS = text("STATIONARY_TRAFFIC"), delay(1800)
		})}, nil, allFresh, roads.LampRed, why(roads.ReasonLongJam)},
		"operator jam without a type is a warning": {[]roads.Message{op(roads.KindJam, func(m *roads.Message) { m.DelayS = delay(1800) })},
			nil, allFresh, roads.LampAmber, why(roads.ReasonWarning)},
		"operator animal": {[]roads.Message{op(roads.KindHazard, func(m *roads.Message) { m.Description = text("Tiere auf der Fahrbahn") })},
			nil, allFresh, roads.LampRed, why(roads.ReasonSafety)},
		"bridge closed":            {[]roads.Message{bridge(text("Brücke gesperrt"))}, nil, allFresh, roads.LampRed, why(roads.ReasonBridgeClosed)},
		"bridge restricted":        {[]roads.Message{bridge(text("Windwarnung 100 km/h"))}, nil, allFresh, roads.LampAmber, why(roads.ReasonWarning)},
		"bridge undefined":         {[]roads.Message{bridge(text("undefiniert"))}, nil, allFresh, roads.LampGreen, nil},
		"bridge without a state":   {[]roads.Message{bridge(nil)}, nil, allFresh, roads.LampGreen, nil},
		"weather level 3":          {nil, []int{3}, allFresh, roads.LampRed, why(roads.ReasonWeatherWarning)},
		"weather level 1":          {nil, []int{1}, allFresh, roads.LampAmber, why(roads.ReasonWeatherWarning)},
		"weather level 0":          {nil, []int{0}, allFresh, roads.LampGreen, nil},
		"closure before safety":    {[]roads.Message{msg(roads.KindHazard, "Falschfahrer"), msg(roads.KindClosure, "gesperrt")}, nil, allFresh, roads.LampRed, why(roads.ReasonRoadClosed)},
		"safety before a bridge":   {[]roads.Message{bridge(text("gesperrt")), msg(roads.KindHazard, "Falschfahrer")}, nil, allFresh, roads.LampRed, why(roads.ReasonSafety)},
		"bridge before a long jam": {[]roads.Message{jam(delay(90)), bridge(text("gesperrt"))}, nil, allFresh, roads.LampRed, why(roads.ReasonBridgeClosed)},
		"long jam before weather":  {[]roads.Message{jam(delay(90))}, []int{4}, allFresh, roads.LampRed, why(roads.ReasonLongJam)},
		"ramp before a jam":        {[]roads.Message{jam(nil), desc(msg(roads.KindClosure, "A7"), "Ausfahrt gesperrt")}, nil, allFresh, roads.LampAmber, why(roads.ReasonRampClosed)},
		"jam before weather amber": {[]roads.Message{jam(nil)}, []int{2}, allFresh, roads.LampAmber, why(roads.ReasonJam)},
		"weather before a warning": {[]roads.Message{msg(roads.KindOther, "Hinweis")}, []int{1}, allFresh, roads.LampAmber, why(roads.ReasonWeatherWarning)},
		"warning before roadworks": {[]roads.Message{msg(roads.KindRoadworks, "Baustelle"), msg(roads.KindOther, "Hinweis")}, nil, allFresh, roads.LampAmber, why(roads.ReasonWarning)},
		"roadworks before sight":   {[]roads.Message{msg(roads.KindRoadworks, "Baustelle")}, nil, stale, roads.LampAmber, why(roads.ReasonNarrowing)},
		"stale alone":              {nil, nil, stale, roads.LampAmber, why(roads.ReasonLimitedSight)},
		"partial is limited sight": {nil, nil, partial, roads.LampAmber, why(roads.ReasonLimitedSight)},
		"an unseen state":          {nil, nil, unseen, roads.LampAmber, why(roads.ReasonLimitedSight)},
		"red beats a partial":      {[]roads.Message{msg(roads.KindClosure, "gesperrt")}, nil, partial, roads.LampRed, why(roads.ReasonRoadClosed)},
	} {
		lamp, reason := roads.LampOf(c.in, c.levels, c.sight)
		if lamp != c.lamp || (reason == nil) != (c.reason == nil) || reason != nil && *reason != *c.reason {
			t.Errorf("%s: %s %v, want %s %v", name, lamp, reason, c.lamp, c.reason)
		}
	}
}

// TestARecordIsMapped: the three stored record codes map one to one; any
// other is a traffic message.
func TestARecordIsMapped(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "ENV-051")
	for raw, want := range map[string]roads.Record{
		"operator_event": roads.RecordOperatorEvent, "traffic_message": roads.RecordTrafficMessage,
		"bridge_state": roads.RecordBridgeState, "x": roads.RecordTrafficMessage,
	} {
		if got := roads.MapRecord(raw); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
}

// TestEveryReasonIsNamed: each reason key and colour has its name, and the
// list of reasons holds each once, in the order a reason is chosen.
func TestEveryReasonIsNamed(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	red, amber := ampel.LampRed, ampel.LampAmber
	var named []roads.Reason
	for _, c := range []struct {
		r    ampel.Reason
		want roads.Reason
	}{
		{ampel.Reason{Lamp: red, Signal: &ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeCarriageway}}, roads.ReasonRoadClosed},
		{ampel.Reason{Lamp: amber, Signal: &ampel.Signal{Kind: ampel.SignalClosure, Scope: ampel.ScopeRamp}}, roads.ReasonRampClosed},
		{ampel.Reason{Lamp: red, Signal: &ampel.Signal{Kind: ampel.SignalSafety}}, roads.ReasonSafety},
		{ampel.Reason{Lamp: red, Signal: &ampel.Signal{Kind: ampel.SignalBridgeWind}}, roads.ReasonBridgeClosed},
		{ampel.Reason{Lamp: red, Signal: &ampel.Signal{Kind: ampel.SignalJam}}, roads.ReasonLongJam},
		{ampel.Reason{Lamp: amber, Signal: &ampel.Signal{Kind: ampel.SignalJam}}, roads.ReasonJam},
		{ampel.Reason{Lamp: red, Signal: &ampel.Signal{Kind: ampel.SignalWeather}}, roads.ReasonWeatherWarning},
		{ampel.Reason{Lamp: amber, Signal: &ampel.Signal{Kind: ampel.SignalSlippery}}, roads.ReasonSlippery},
		{ampel.Reason{Lamp: amber, Signal: &ampel.Signal{Kind: ampel.SignalWarning}}, roads.ReasonWarning},
		{ampel.Reason{Lamp: amber, Signal: &ampel.Signal{Kind: ampel.SignalRoadworks}}, roads.ReasonNarrowing},
		{ampel.Reason{Lamp: amber, Sight: &ampel.SightEntry{Group: ampel.GroupWeather, State: ampel.SightMissing}}, roads.ReasonLimitedSight},
	} {
		got := roads.ReasonOf(c.r)
		if got != c.want {
			t.Errorf("%+v: %q, want %q", c.r, got, c.want)
		}
		named = append(named, got)
	}
	if !slices.Equal(named, roads.Reasons) {
		t.Errorf("reasons %v, want the platform's order %v", roads.Reasons, named)
	}
}

// TestASourcesSightIsItsFreshness: no data is missing, past its threshold
// stale, otherwise fresh.
func TestASourcesSightIsItsFreshness(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		f    provenance.Freshness
		want ampel.Sight
	}{
		{provenance.Freshness{Stale: true}, ampel.SightMissing},
		{provenance.Freshness{DataAsOf: &at, Stale: true}, ampel.SightStale},
		{provenance.Freshness{DataAsOf: &at}, ampel.SightFresh},
	} {
		if got := roads.SourceSight(c.f); got != c.want {
			t.Errorf("%+v: %q, want %q", c.f, got, c.want)
		}
	}
}

func TestFiltersSplitTheUnmappedValue(t *testing.T) {
	req.Covers(t, "FR-ROAD-006", "ENV-051")
	known, other := roads.Filter{}.KnownKinds()
	if !slices.Equal(known, roads.Kinds[:7]) || !other {
		t.Errorf("no kinds keeps every kind: %v %v", known, other)
	}
	known[0] = "changed"
	if roads.Kinds[0] != roads.KindClosure {
		t.Error("an empty filter hands out the package's own list")
	}
	known, other = roads.Filter{Kinds: []roads.Kind{roads.KindJam, roads.KindOther, roads.KindClosure}}.KnownKinds()
	if !slices.Equal(known, []roads.Kind{roads.KindJam, roads.KindClosure}) || !other {
		t.Errorf("with other: %v %v", known, other)
	}
	known, other = roads.Filter{Kinds: []roads.Kind{roads.KindJam}}.KnownKinds()
	if !slices.Equal(known, []roads.Kind{roads.KindJam}) || other {
		t.Errorf("without other: %v %v", known, other)
	}
	classes, otherClass := roads.AreaQuery{Classes: []roads.Class{roads.ClassOther}}.KnownClasses()
	if classes == nil || len(classes) != 0 || !otherClass {
		t.Errorf("other alone keeps no mapped class: %v %v", classes, otherClass)
	}
	classes, otherClass = roads.AreaQuery{}.KnownClasses()
	if !slices.Equal(classes, roads.Classes[:4]) || !otherClass {
		t.Errorf("no classes: %v %v", classes, otherClass)
	}
}
